// Package agent orchestrates the DeFi agent lifecycle on Base blockchain.
//
// Lifecycle:
//
//  1. Initialize: Load config, create Base chain clients, create HCS handler
//  2. Register: Register agent identity via ERC-8004 on Base Sepolia
//  3. Subscribe: Start HCS subscription for task assignments
//  4. Run: Enter main loops — trading loop + P&L report loop + health loop
//  5. Shutdown: Graceful shutdown on context cancellation or signal
//
// Trading pipeline (periodic via TradingInterval):
//
//	Fetch market state from DEX
//	→ Evaluate mean reversion strategy
//	→ Execute trade if buy/sell signal
//	→ Record P&L, gas, and fees
//
// The agent is designed to be self-sustaining: revenue from successful trades
// must cover gas costs and protocol fees over time.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/daemon"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/identity"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/payment"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/trading"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/hcs"
)

// Agent orchestrates the DeFi agent's full lifecycle.
// All dependencies are injected at construction time.
type Agent struct {
	cfg      Config
	log      *slog.Logger
	daemon   daemon.DaemonClient
	identity identity.IdentityRegistry
	payment  payment.PaymentProtocol
	strategy trading.Strategy
	executor trading.TradeExecutor
	pnl      *trading.PnLTracker
	handler  *hcs.Handler

	daemonReg       *daemon.RegisterResponse
	startTime       time.Time
	completedTrades atomic.Int64
	failedTrades    atomic.Int64
}

// New creates a DeFi Agent with all required dependencies injected.
func New(
	cfg Config,
	log *slog.Logger,
	dc daemon.DaemonClient,
	id identity.IdentityRegistry,
	pay payment.PaymentProtocol,
	executor trading.TradeExecutor,
	strategy trading.Strategy,
	pnl *trading.PnLTracker,
	handler *hcs.Handler,
) *Agent {
	return &Agent{
		cfg:      cfg,
		log:      log,
		daemon:   dc,
		identity: id,
		payment:  pay,
		strategy: strategy,
		executor: executor,
		pnl:      pnl,
		handler:  handler,
	}
}

// Run starts the agent and blocks until the context is cancelled.
// It registers identity, starts HCS subscription, trading loop, P&L report
// loop, and health loop concurrently.
func (a *Agent) Run(ctx context.Context) error {
	a.startTime = time.Now()
	a.log.Info("starting DeFi agent", "agent_id", a.cfg.AgentID, "strategy", a.strategy.Name())

	// Step 1: Register agent identity on Base via ERC-8004.
	id, err := a.identity.Register(ctx, identity.RegistrationRequest{
		AgentID:   a.cfg.AgentID,
		AgentType: "defi",
	})
	if err != nil {
		// If already registered, retrieve the existing identity.
		a.log.Warn("identity registration failed, checking if already registered", "error", err)
		existing, verifyErr := a.identity.GetIdentity(ctx, a.cfg.AgentID)
		if verifyErr != nil {
			return fmt.Errorf("agent: failed to register or retrieve identity: %w", err)
		}
		id = existing
	}
	a.log.Info("agent identity ready", "agent_id", id.AgentID, "tx", id.TxHash)

	// Step 1.5: Register with daemon runtime (optional).
	reg, regErr := a.daemon.Register(ctx, daemon.RegisterRequest{
		AgentName:    a.cfg.AgentID,
		AgentType:    "defi",
		Capabilities: []string{"trading", "base", "erc8004", "x402", "erc8021"},
	})
	if regErr != nil {
		a.log.Warn("daemon registration failed, running standalone", "error", regErr)
		a.daemon = daemon.Noop()
	} else {
		a.daemonReg = reg
		a.log.Info("registered with daemon", "agent_id", reg.AgentID, "session_id", reg.SessionID)
	}

	// Step 2: Start HCS subscription for incoming task assignments.
	go func() {
		if err := a.handler.StartSubscription(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("HCS subscription failed", "error", err)
		}
	}()

	// Step 3: Start background goroutines.
	go a.tradingLoop(ctx)
	go a.pnlReportLoop(ctx)
	go a.healthLoop(ctx)

	// Step 4: Process coordinator commands from HCS.
	for {
		select {
		case <-ctx.Done():
			a.log.Info("shutting down DeFi agent",
				"completed_trades", a.completedTrades.Load(),
				"failed_trades", a.failedTrades.Load(),
				"uptime", time.Since(a.startTime))
			return ctx.Err()
		case task := <-a.handler.Tasks():
			a.handleCoordinatorTask(ctx, task)
		}
	}
}

// tradingLoop periodically evaluates the strategy and executes trades.
func (a *Agent) tradingLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.TradingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.executeTradingCycle(ctx); err != nil {
				a.log.Warn("trading cycle failed", "error", err)
				a.failedTrades.Add(1)
			}
		}
	}
}

// pnlReportLoop periodically publishes P&L reports to the coordinator via HCS.
func (a *Agent) pnlReportLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.PnLReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report := a.pnl.Report(a.startTime, time.Now())

			msg := hcs.PnLReportMessage{
				AgentID:          a.cfg.AgentID,
				TotalRevenue:     report.TotalRevenue,
				TotalGasCosts:    report.TotalGasCosts,
				TotalFees:        report.TotalFees,
				NetPnL:           report.NetPnL,
				TradeCount:       report.TradeCount,
				WinRate:          report.WinRate,
				IsSelfSustaining: report.IsSelfSustaining,
				PeriodStart:      report.PeriodStart,
				PeriodEnd:        report.PeriodEnd,
				ActiveStrategy:   a.strategy.Name(),
			}

			if err := a.handler.PublishPnL(ctx, msg); err != nil {
				a.log.Error("failed to publish P&L report", "error", err)
			} else {
				a.log.Info("P&L report published",
					"net_pnl", report.NetPnL,
					"self_sustaining", report.IsSelfSustaining,
					"trades", report.TradeCount,
				)
			}
		}
	}
}

// healthLoop periodically publishes the agent's health status and daemon heartbeat.
func (a *Agent) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report := a.pnl.Report(a.startTime, time.Now())
			a.handler.PublishHealth(ctx, hcs.HealthStatus{
				AgentID:        a.cfg.AgentID,
				Status:         "trading",
				ActiveStrategy: a.strategy.Name(),
				CurrentPnL:     report.NetPnL,
				UptimeSeconds:  int64(time.Since(a.startTime).Seconds()),
				TradeCount:     int(a.completedTrades.Load()),
			})

			// Daemon heartbeat on the same tick.
			hbReq := daemon.HeartbeatRequest{Timestamp: time.Now()}
			if a.daemonReg != nil {
				hbReq.AgentID = a.daemonReg.AgentID
				hbReq.SessionID = a.daemonReg.SessionID
			}
			if err := a.daemon.Heartbeat(ctx, hbReq); err != nil {
				a.log.Warn("daemon heartbeat failed", "error", err)
			}
		}
	}
}

// executeTradingCycle runs one complete strategy evaluation and optional trade.
func (a *Agent) executeTradingCycle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("agent: context cancelled before trading cycle: %w", err)
	}

	// 1. Pay for market data via x402 (if configured).
	a.payForMarketData(ctx)

	// 2. Fetch current market state.
	market, err := a.executor.GetMarketState(ctx, a.cfg.TokenIn, a.cfg.TokenOut)
	if err != nil {
		return fmt.Errorf("agent: market state fetch failed: %w", err)
	}

	// 3. Evaluate strategy.
	signal, err := a.strategy.Evaluate(ctx, *market)
	if err != nil {
		return fmt.Errorf("agent: strategy evaluation failed: %w", err)
	}

	a.log.Info("strategy signal",
		"type", signal.Type,
		"confidence", signal.Confidence,
		"reason", signal.Reason)

	// 4. Execute trade if signal is actionable.
	if signal.Type == trading.SignalHold {
		return nil
	}

	return a.processTrade(ctx, signal, market)
}

// processTrade executes a trade based on the given signal and records the result.
func (a *Agent) processTrade(ctx context.Context, signal *trading.Signal, market *trading.MarketState) error {
	if signal.SuggestedSize > a.strategy.MaxPosition() {
		return fmt.Errorf("agent: %w: signal size %.4f > max %.4f",
			trading.ErrPositionExceedsMax, signal.SuggestedSize, a.strategy.MaxPosition())
	}

	// Calculate slippage-protected MinAmountOut (0.5% tolerance).
	expectedOut := signal.SuggestedSize * market.Price
	minOut := expectedOut * 0.995

	trade := trading.Trade{
		TokenIn:      signal.TokenIn,
		TokenOut:     signal.TokenOut,
		AmountIn:     fmt.Sprintf("%.6f", signal.SuggestedSize),
		MinAmountOut: fmt.Sprintf("%.6f", minOut),
		Signal:       *signal,
		Deadline:     time.Now().Add(5 * time.Minute),
	}

	result, err := a.executor.Execute(ctx, trade)
	if err != nil {
		return fmt.Errorf("agent: trade execution failed: %w", err)
	}

	// Revenue: estimated swap output minus Uniswap V3 fee tier (0.3%).
	cost := signal.SuggestedSize * market.Price
	revenue := cost * 0.003 // net of 0.3% fee tier
	if !result.Profitable {
		revenue = 0
	}

	a.pnl.RecordTrade(trading.TradeRecord{
		TradeResult: *result,
		Revenue:     revenue,
		Cost:        cost,
	})

	// Compute gas cost USD from receipt: GasCostWei / 1e18 * ETH price.
	gasCostUSD := gasCostFromWei(result.GasCostWei)

	a.pnl.RecordGasCost(trading.GasCost{
		TxHash:  result.TxHash,
		GasUsed: result.GasUsed,
		CostWei: result.GasCostWei,
		CostUSD: gasCostUSD,
	})

	a.completedTrades.Add(1)
	a.log.Info("trade executed",
		"tx_hash", result.TxHash,
		"signal", signal.Type,
		"profitable", result.Profitable)

	return nil
}

// payForMarketData sends an x402 payment for market data access if configured.
// Payment failures are logged but do not block the trading cycle.
func (a *Agent) payForMarketData(ctx context.Context) {
	if a.payment == nil || a.cfg.MarketDataRecipient == "" {
		return
	}

	amount := new(big.Int)
	if _, ok := amount.SetString(a.cfg.MarketDataCostWei, 10); !ok || amount.Sign() <= 0 {
		a.log.Warn("x402: invalid market data cost, skipping payment", "cost", a.cfg.MarketDataCostWei)
		return
	}

	receipt, err := a.payment.Pay(ctx, payment.PaymentRequest{
		RecipientAddress: a.cfg.MarketDataRecipient,
		Amount:           amount,
		Token:            "ETH",
		InvoiceID:        fmt.Sprintf("mktdata-%d", time.Now().UnixNano()),
		Memo:             "x402 market data access",
	})
	if err != nil {
		a.log.Warn("x402: market data payment failed", "err", err)
		return
	}

	// Record the payment as a fee so it appears in P&L self-sustainability.
	costUSD := 0.0
	if receipt.GasCost != nil {
		costFloat, _ := new(big.Float).SetInt(receipt.GasCost).Float64()
		costUSD = costFloat / 1e18 * 2500 // rough ETH/USD estimate
	}
	a.pnl.RecordFee(trading.Fee{
		TxHash:    receipt.TxHash,
		Type:      "x402_market_data",
		AmountUSD: costUSD,
	})

	a.log.Info("x402: market data payment sent", "tx", receipt.TxHash)
}

// gasCostFromWei converts a hex wei string to an approximate USD cost.
// Uses a fixed ETH/USD rate suitable for testnet P&L tracking.
func gasCostFromWei(hexWei string) float64 {
	const ethPriceUSD = 2500.0

	wei := new(big.Int)
	if _, ok := wei.SetString(hexWei, 0); !ok {
		return 0
	}
	ethFloat, _ := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		new(big.Float).SetFloat64(1e18),
	).Float64()
	return ethFloat * ethPriceUSD
}

// handleCoordinatorTask processes an incoming task assignment from the coordinator.
func (a *Agent) handleCoordinatorTask(ctx context.Context, task hcs.TaskAssignment) {
	a.log.Info("processing coordinator task", "task_id", task.TaskID, "type", task.TaskType)
	start := time.Now()

	var txHash string
	var taskErr error

	switch task.TaskType {
	case "execute_trade":
		taskErr = a.executeTradingCycle(ctx)
	default:
		taskErr = fmt.Errorf("agent: unknown task type: %s", task.TaskType)
	}

	duration := time.Since(start)
	status := "completed"
	errMsg := ""
	if taskErr != nil {
		status = "failed"
		errMsg = taskErr.Error()
		a.failedTrades.Add(1)
		a.log.Error("coordinator task failed", "task_id", task.TaskID, "error", taskErr)
	}

	a.handler.PublishResult(ctx, hcs.TaskResult{
		TaskID:     task.TaskID,
		Status:     status,
		TxHash:     txHash,
		Error:      errMsg,
		DurationMs: duration.Milliseconds(),
	})
}

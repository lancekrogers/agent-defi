package loop

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi/internal/base/trading"
	"github.com/lancekrogers/agent-defi/internal/risk"
	"github.com/lancekrogers/agent-defi/internal/vault"
)

// Config holds trading loop parameters.
type Config struct {
	Interval time.Duration
	TokenIn  common.Address
	TokenOut common.Address
	AgentLog AgentLogRefresher
}

// AgentLogRefresher rebuilds the aggregate Protocol Labs evidence log.
type AgentLogRefresher interface {
	Refresh(ctx context.Context) (int, error)
}

// Runner orchestrates the trading loop: fetch market data, evaluate strategy,
// check risk, and execute swaps through the vault.
type Runner struct {
	cfg      Config
	log      *slog.Logger
	vault    vault.Client
	executor trading.TradeExecutor
	strategy trading.Strategy
	risk     *risk.Manager
	agentLog AgentLogRefresher
}

// New creates a trading loop runner.
func New(cfg Config, log *slog.Logger, v vault.Client, exec trading.TradeExecutor, strat trading.Strategy, r *risk.Manager) *Runner {
	return &Runner{cfg: cfg, log: log, vault: v, executor: exec, strategy: strat, risk: r, agentLog: cfg.AgentLog}
}

// Run starts the trading loop, executing cycles at the configured interval.
func (r *Runner) Run(ctx context.Context) error {
	if r.log != nil {
		r.log.Info("starting trading loop", "interval", r.cfg.Interval)
	}

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if r.log != nil {
				r.log.Info("trading loop stopped")
			}
			return ctx.Err()
		case <-ticker.C:
			if err := r.cycle(ctx); err != nil {
				if r.log != nil {
					r.log.Warn("trading cycle failed", "error", err)
				}
			}
		}
	}
}

func (r *Runner) cycle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	market, err := r.executor.GetMarketState(ctx, r.cfg.TokenIn.Hex(), r.cfg.TokenOut.Hex())
	if err != nil {
		return fmt.Errorf("cycle: market state: %w", err)
	}

	nav, err := r.vault.TotalAssets(ctx)
	if err == nil && nav != nil {
		navFloat, _ := new(big.Float).SetInt(nav).Float64()
		r.risk.UpdateNAV(navFloat)
	}

	signal, err := r.strategy.Evaluate(ctx, *market)
	if err != nil {
		return fmt.Errorf("cycle: strategy: %w", err)
	}

	if r.log != nil {
		args := []any{"type", signal.Type, "confidence", signal.Confidence, "reason", signal.Reason}
		if signal.Ritual != nil {
			args = append(args,
				"campaign", signal.Ritual.Campaign,
				"ritual_id", signal.Ritual.RitualID,
				"ritual_run_id", signal.Ritual.RitualRunID,
				"session_id", signal.Ritual.SessionID,
				"provider", signal.Ritual.Provider,
				"model", signal.Ritual.Model,
				"workdir", signal.Ritual.Workdir,
				"trade_allowed", signal.Ritual.Guardrails.TradeAllowed,
				"blocking_factors", signal.Ritual.BlockingFactors,
			)
		}
		r.log.Info("signal", args...)
	}

	if signal.Type == trading.SignalHold {
		if r.log != nil && signal.Ritual != nil && len(signal.Ritual.BlockingFactors) > 0 {
			r.log.Info("ritual denied trade", "ritual_run_id", signal.Ritual.RitualRunID, "blocking_factors", signal.Ritual.BlockingFactors)
		}
		r.refreshAgentLog(ctx)
		return nil
	}

	if err := r.risk.Check(ctx, signal, market.Price); err != nil {
		if r.log != nil {
			r.log.Info("risk rejected trade", "error", err)
		}
		return nil
	}

	r.risk.Clamp(signal, market.Price)

	var amountIn, minOut *big.Int
	switch signal.Type {
	case trading.SignalBuy:
		amountIn = new(big.Int).SetInt64(int64(signal.SuggestedSize * market.Price * 1e6))
		minOut = new(big.Int).SetInt64(int64(signal.SuggestedSize * 0.99 * 1e18))
	case trading.SignalSell:
		amountIn = new(big.Int).SetInt64(int64(signal.SuggestedSize * 1e18))
		minOut = new(big.Int).SetInt64(int64(signal.SuggestedSize * market.Price * 0.99 * 1e6))
	default:
		return fmt.Errorf("cycle: unsupported signal type %q", signal.Type)
	}

	txHash, err := r.vault.ExecuteSwap(ctx, vault.SwapParams{
		TokenIn:      common.HexToAddress(signal.TokenIn),
		TokenOut:     common.HexToAddress(signal.TokenOut),
		AmountIn:     amountIn,
		MinAmountOut: minOut,
		Reason:       []byte(signal.Reason),
	})
	if err != nil {
		return fmt.Errorf("cycle: swap failed: %w", err)
	}

	r.risk.RecordTrade(signal.SuggestedSize, market.Price)
	if r.log != nil {
		r.log.Info("swap executed", "tx", txHash.Hex(), "size", signal.SuggestedSize)
	}
	r.refreshAgentLog(ctx)
	return nil
}

func (r *Runner) refreshAgentLog(ctx context.Context) {
	if r.agentLog == nil {
		return
	}
	count, err := r.agentLog.Refresh(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Warn("agent log refresh failed", "error", err)
		}
		return
	}
	if r.log != nil {
		r.log.Info("agent log refreshed", "entries", count)
	}
}

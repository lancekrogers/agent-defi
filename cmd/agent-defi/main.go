// Command agent-defi is the entry point for the Base DeFi trading agent.
//
// The agent:
//   - Registers its identity via ERC-8004 on Base Sepolia
//   - Executes mean reversion trading strategies on Base Sepolia DEX
//   - Pays for compute via x402 machine-to-machine payments
//   - Attributes transactions via ERC-8021 builder codes
//   - Reports P&L to the coordinator via HCS
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/pkg/daemon"
	"github.com/lancekrogers/agent-defi/internal/agent"
	"github.com/lancekrogers/agent-defi/internal/base/attribution"
	"github.com/lancekrogers/agent-defi/internal/base/identity"
	"github.com/lancekrogers/agent-defi/internal/base/payment"
	"github.com/lancekrogers/agent-defi/internal/base/trading"
	"github.com/lancekrogers/agent-defi/internal/guard"
	"github.com/lancekrogers/agent-defi/internal/hcs"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := agent.LoadConfig()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var idRegistry identity.IdentityRegistry
	var pay payment.PaymentProtocol
	var executor trading.TradeExecutor

	if cfg.MockMode {
		log.Info("MOCK MODE ENABLED - no real blockchain transactions")
		idRegistry = identity.NewMockRegistry()
		pay = payment.NewMockProtocol()
		executor = trading.NewMockExecutor()
	} else {
		idRegistry = identity.NewRegistry(cfg.Identity)
		pay = payment.NewProtocol(cfg.Payment)

		enc, err := attribution.NewEncoder(cfg.Attribution)
		if err != nil {
			log.Warn("attribution encoder disabled", "error", err)
		} else {
			cfg.Trading.Attribution = enc
		}

		executor = trading.NewExecutor(cfg.Trading)
	}

	// Initialize trading strategy.
	strategy := trading.NewMeanReversionStrategy(cfg.StrategyConfig())

	// Initialize P&L tracker.
	pnl := trading.NewPnLTracker()

	// Initialize HCS transport with Hedera SDK.
	transport := initHCSTransport(log)
	cfg.HCS.Transport = transport
	handler := hcs.NewHandler(cfg.HCS)

	// Connect to daemon runtime (optional — agent works standalone if unavailable).
	daemonClient := connectDaemon(log, cfg.DaemonAddr)
	defer func() { _ = daemonClient.Close() }()

	// Wire the agent with all dependencies.
	a := agent.New(*cfg, log, daemonClient, idRegistry, pay, executor, strategy, pnl, handler)

	creGuard := guard.NewCREGuard(log)
	a.SetCREGuard(creGuard)
	if cfg.CREMaxPositionUSD > 0 {
		log.Info("CRE position guard enabled", "max_position_usd", cfg.CREMaxPositionUSD)
	} else {
		log.Info("CRE position guard enabled without global max (per-task constraints only)")
	}

	log.Info("DeFi agent starting",
		"agent_id", cfg.AgentID,
		"chain_id", cfg.Trading.ChainID,
		"rpc_url", cfg.Trading.RPCURL,
		"strategy", strategy.Name())

	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Error("agent exited with error", "error", err)
		os.Exit(1)
	}

	log.Info("DeFi agent stopped gracefully")
}

func initHCSTransport(log *slog.Logger) hcs.Transport {
	accountIDStr := os.Getenv("HEDERA_ACCOUNT_ID")
	privateKeyStr := os.Getenv("HEDERA_PRIVATE_KEY")

	if accountIDStr == "" || privateKeyStr == "" {
		log.Warn("HEDERA_ACCOUNT_ID or HEDERA_PRIVATE_KEY not set, HCS transport disabled")
		return &fallbackTransport{log: log}
	}

	accountID, err := hiero.AccountIDFromString(accountIDStr)
	if err != nil {
		log.Error("failed to parse HEDERA_ACCOUNT_ID", "error", err)
		return &fallbackTransport{log: log}
	}

	privateKey, err := hiero.PrivateKeyFromString(privateKeyStr)
	if err != nil {
		log.Error("failed to parse HEDERA_PRIVATE_KEY", "error", err)
		return &fallbackTransport{log: log}
	}

	hederaClient := hiero.ClientForTestnet()
	hederaClient.SetOperator(accountID, privateKey)

	log.Info("HCS transport initialized", "account_id", accountIDStr)
	return hcs.NewHCSTransport(hcs.HCSTransportConfig{Client: hederaClient})
}

// fallbackTransport is a no-op HCS transport used when Hedera credentials are unavailable.
type fallbackTransport struct {
	log *slog.Logger
}

func (f *fallbackTransport) Publish(_ context.Context, topicID string, data []byte) error {
	f.log.Debug("fallback HCS publish", "topic", topicID, "bytes", len(data))
	return nil
}

func (f *fallbackTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return make(chan []byte), make(chan error)
}

func connectDaemon(log *slog.Logger, addr string) daemon.DaemonClient {
	daemonCfg := daemon.DefaultConfig()
	daemonCfg.Address = addr

	client, err := daemon.NewGRPCClient(context.Background(), daemonCfg)
	if err != nil {
		log.Warn("daemon connection failed, running standalone", "error", err)
		return daemon.Noop()
	}
	return client
}

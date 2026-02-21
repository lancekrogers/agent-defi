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

	"github.com/lancekrogers/agent-defi-ethden-2026/internal/agent"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/attribution"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/identity"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/payment"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/trading"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/hcs"
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

	// Initialize ERC-8004 identity registry.
	idRegistry := identity.NewRegistry(cfg.Identity)

	// Initialize x402 payment protocol.
	pay := payment.NewProtocol(cfg.Payment)

	// Initialize ERC-8021 attribution encoder and inject into trading config.
	enc, err := attribution.NewEncoder(cfg.Attribution)
	if err != nil {
		log.Warn("attribution encoder disabled", "error", err)
	} else {
		cfg.Trading.Attribution = enc
	}

	// Initialize trading strategy.
	strategy := trading.NewMeanReversionStrategy(cfg.StrategyConfig())

	// Initialize trade executor for Base Sepolia.
	executor := trading.NewExecutor(cfg.Trading)

	// Initialize P&L tracker.
	pnl := trading.NewPnLTracker()

	// HCS handler requires a transport implementation.
	// In production, use the Hedera SDK transport.
	// For now, use a stub transport that logs and no-ops.
	var transport hcs.Transport
	if transport == nil {
		log.Warn("no HCS transport configured, using stub")
		transport = &stubTransport{log: log}
	}
	cfg.HCS.Transport = transport
	handler := hcs.NewHandler(cfg.HCS)

	// Wire the agent with all dependencies.
	a := agent.New(*cfg, log, idRegistry, pay, executor, strategy, pnl, handler)

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

// stubTransport is a no-op HCS transport for development when
// no Hedera network is available. All publishes are logged and discarded.
type stubTransport struct {
	log *slog.Logger
}

func (s *stubTransport) Publish(_ context.Context, topicID string, data []byte) error {
	s.log.Debug("stub HCS publish", "topic", topicID, "bytes", len(data))
	return nil
}

func (s *stubTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return make(chan []byte), make(chan error)
}

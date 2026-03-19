package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi/internal/base/trading"
	"github.com/lancekrogers/agent-defi/internal/festruntime"
	agentloggen "github.com/lancekrogers/agent-defi/internal/loggen"
	"github.com/lancekrogers/agent-defi/internal/loop"
	"github.com/lancekrogers/agent-defi/internal/risk"
	"github.com/lancekrogers/agent-defi/internal/strategy"
	"github.com/lancekrogers/agent-defi/internal/vault"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	vaultCfg := vault.Config{
		RPCURL:       envOr("VAULT_RPC_URL", "https://sepolia.base.org"),
		ChainID:      84532,
		VaultAddress: os.Getenv("VAULT_ADDRESS"),
		PrivateKey:   os.Getenv("AGENT_PRIVATE_KEY"),
	}

	vaultClient := vault.NewClient(vaultCfg)

	executor := trading.NewExecutor(trading.ExecutorConfig{
		RPCURL:           vaultCfg.RPCURL,
		ChainID:          vaultCfg.ChainID,
		WalletAddress:    os.Getenv("AGENT_WALLET_ADDRESS"),
		PrivateKey:       vaultCfg.PrivateKey,
		DEXRouterAddress: envOr("DEX_ROUTER", "0x2626664c2603336E57B271c5C0b26F421741e481"),
	})

	obeyClient := &strategy.ObeyClient{
		Socket:   envOr("OBEY_SOCKET", "/tmp/obey.sock"),
		Campaign: envOr("OBEY_CAMPAIGN", "Obey-Agent-Economy"),
		Provider: envOr("OBEY_PROVIDER", "claude-code"),
		Model:    envOr("LLM_MODEL", "claude-sonnet-4-6"),
		Agent:    envOr("OBEY_AGENT", "vault-trader"),
	}

	strat, err := festruntime.New(festruntime.Config{
		CampaignRoot:    envOr("OBEY_CAMPAIGN_ROOT", envOr("CAMP_ROOT", "")),
		RitualID:        envOr("OBEY_RITUAL_ID", "agent-market-research-RI-AM0001"),
		FestBinary:      envOr("FEST_BINARY", "fest"),
		TokenIn:         os.Getenv("TOKEN_IN"),
		TokenOut:        os.Getenv("TOKEN_OUT"),
		Timeout:         durationOr("OBEY_RITUAL_TIMEOUT", 2*time.Minute),
		PollInterval:    durationOr("OBEY_RITUAL_POLL_INTERVAL", time.Second),
		MaxPositionSize: 100.0,
		Logger:          log,
	}, obeyClient)
	if err != nil {
		log.Error("ritual runtime init failed", "error", err)
		os.Exit(1)
	}
	campaignRoot := strat.CampaignRoot()

	riskMgr := risk.NewManager(risk.Config{
		MaxPositionUSD:    1000,
		MaxDailyVolumeUSD: 10000,
		MaxDrawdownPct:    0.10,
		InitialNAV:        10000,
	})

	runner := loop.New(loop.Config{
		Interval: 5 * time.Minute,
		TokenIn:  common.HexToAddress(os.Getenv("TOKEN_IN")),
		TokenOut: common.HexToAddress(os.Getenv("TOKEN_OUT")),
		AgentLog: agentloggen.Refresher{
			Config: agentloggen.Config{
				RPCURL:       vaultCfg.RPCURL,
				VaultAddress: vaultCfg.VaultAddress,
				RitualsDir:   filepath.Join(campaignRoot, "festivals"),
				AgentName:    envOr("AGENT_LOG_NAME", "OBEY Vault Agent"),
				AgentID:      envOr("AGENT_LOG_IDENTITY", "0x0C97820abBdD2562645DaE92D35eD581266CCe70"),
			},
			OutFile: filepath.Join(campaignRoot, "projects", "agent-defi", "agent_log.json"),
		},
	}, log, vaultClient, executor, strat, riskMgr)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info("vault agent starting",
		"vault", vaultCfg.VaultAddress,
		"strategy", strat.Name(),
	)

	if err := runner.Run(ctx); err != nil && err != context.Canceled {
		log.Error("agent exited with error", "error", err)
		os.Exit(1)
	}
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func durationOr(key string, defaultVal time.Duration) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err == nil {
			return parsed
		}
	}
	return defaultVal
}

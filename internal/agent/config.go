package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/attribution"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/identity"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/payment"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/trading"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/hcs"
)

// Config holds all configuration for the DeFi agent.
type Config struct {
	// AgentID is the unique identifier for this agent instance.
	AgentID string

	// DaemonAddr is the address of the daemon client for coordination.
	DaemonAddr string

	// TradingInterval is how often to evaluate the strategy and execute trades.
	TradingInterval time.Duration

	// PnLReportInterval is how often P&L reports are published via HCS.
	PnLReportInterval time.Duration

	// HealthInterval is how often to send heartbeat messages.
	HealthInterval time.Duration

	// Identity holds ERC-8004 identity registration configuration.
	Identity identity.RegistryConfig

	// Payment holds x402 payment protocol configuration.
	Payment payment.ProtocolConfig

	// Trading holds trade executor configuration.
	Trading trading.ExecutorConfig

	// Attribution holds ERC-8021 builder attribution configuration.
	Attribution attribution.Config

	// HCS holds Hedera Consensus Service handler configuration.
	HCS hcs.HandlerConfig

	// TokenIn is the address of the token to sell (e.g., USDC on Base Sepolia).
	TokenIn string

	// TokenOut is the address of the token to buy (e.g., WETH on Base Sepolia).
	TokenOut string

	// MarketDataRecipient is the x402 payment recipient for market data access.
	// When set, the agent pays this address before each market data fetch.
	MarketDataRecipient string

	// MarketDataCostWei is the x402 payment amount per market data request in wei.
	// Defaults to 1000000000000000 (0.001 ETH).
	MarketDataCostWei string
}

// StrategyConfig builds a MeanReversionConfig from the agent's trading config.
func (c *Config) StrategyConfig() trading.MeanReversionConfig {
	return trading.MeanReversionConfig{
		TokenIn:  c.Trading.WalletAddress, // will be overridden
		TokenOut: c.Trading.WalletAddress, // will be overridden
	}
}

// LoadConfig reads DeFi agent configuration from environment variables.
// All env vars use the DEFI_ prefix.
func LoadConfig() (*Config, error) {
	cfg := &Config{}

	cfg.AgentID = os.Getenv("DEFI_AGENT_ID")
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("config: DEFI_AGENT_ID is required")
	}

	cfg.DaemonAddr = envOr("DEFI_DAEMON_ADDR", "localhost:50051")
	cfg.TradingInterval = parseDurationOrDefault(os.Getenv("DEFI_TRADING_INTERVAL"), 60*time.Second)
	cfg.PnLReportInterval = parseDurationOrDefault(os.Getenv("DEFI_PNL_REPORT_INTERVAL"), 5*time.Minute)
	cfg.HealthInterval = parseDurationOrDefault(os.Getenv("DEFI_HEALTH_INTERVAL"), 30*time.Second)

	// Identity configuration (ERC-8004).
	cfg.Identity.RPCURL = envOr("DEFI_BASE_RPC_URL", "https://sepolia.base.org")
	cfg.Identity.ChainID = 84532
	cfg.Identity.ContractAddress = os.Getenv("DEFI_ERC8004_CONTRACT")
	cfg.Identity.PrivateKey = os.Getenv("DEFI_PRIVATE_KEY")

	// Payment configuration (x402).
	cfg.Payment.RPCURL = envOr("DEFI_BASE_RPC_URL", "https://sepolia.base.org")
	cfg.Payment.ChainID = 84532
	cfg.Payment.WalletAddress = os.Getenv("DEFI_WALLET_ADDRESS")
	cfg.Payment.PrivateKey = os.Getenv("DEFI_PRIVATE_KEY")

	// Trading configuration.
	cfg.Trading.RPCURL = envOr("DEFI_BASE_RPC_URL", "https://sepolia.base.org")
	cfg.Trading.ChainID = 84532
	cfg.Trading.WalletAddress = os.Getenv("DEFI_WALLET_ADDRESS")
	cfg.Trading.PrivateKey = os.Getenv("DEFI_PRIVATE_KEY")
	cfg.Trading.DEXRouterAddress = envOr("DEFI_DEX_ROUTER", "0x0000000000000000000000000000000000000000")

	// Attribution configuration (ERC-8021).
	builderCode := os.Getenv("DEFI_BUILDER_CODE")
	if len(builderCode) >= 20 {
		copy(cfg.Attribution.BuilderCode[:], builderCode)
	}
	cfg.Attribution.Enabled = os.Getenv("DEFI_ATTRIBUTION_ENABLED") != "false"

	// HCS configuration.
	cfg.HCS.TaskTopicID = os.Getenv("HCS_TASK_TOPIC")
	cfg.HCS.ResultTopicID = os.Getenv("HCS_RESULT_TOPIC")
	cfg.HCS.AgentID = cfg.AgentID

	// Trading pair.
	cfg.TokenIn = envOr("DEFI_TOKEN_IN", "0x036CbD53842c5426634e7929541eC2318f3dCF7e")  // USDC on Base Sepolia
	cfg.TokenOut = envOr("DEFI_TOKEN_OUT", "0x4200000000000000000000000000000000000006") // WETH on Base Sepolia

	// x402 market data payment.
	cfg.MarketDataRecipient = os.Getenv("DEFI_MARKET_DATA_RECIPIENT")
	cfg.MarketDataCostWei = envOr("DEFI_MARKET_DATA_COST_WEI", "1000000000000000") // 0.001 ETH

	return cfg, nil
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func parseDurationOrDefault(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

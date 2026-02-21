package trading

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/attribution"
)

// TradeExecutor defines the interface for executing trades on a Base DEX.
// Implementations interact with the Base Sepolia chain via JSON-RPC.
type TradeExecutor interface {
	// Execute submits a trade transaction to the DEX on Base Sepolia.
	// Returns ErrTradeFailed if the transaction reverts.
	// Returns ErrInsufficientLiquidity if the DEX cannot fill the trade.
	Execute(ctx context.Context, trade Trade) (*TradeResult, error)

	// GetBalance fetches the current token balance for the agent wallet.
	// Use empty string for TokenAddress to get native ETH balance.
	GetBalance(ctx context.Context, tokenAddress string) (*Balance, error)

	// GetMarketState fetches current market data for the given trading pair
	// from the DEX or price oracle.
	GetMarketState(ctx context.Context, tokenIn, tokenOut string) (*MarketState, error)
}

// ExecutorConfig holds configuration for the Base chain trade executor.
type ExecutorConfig struct {
	// RPCURL is the Base Sepolia JSON-RPC endpoint.
	RPCURL string

	// ChainID is the target chain ID.
	ChainID int64

	// WalletAddress is this agent's Ethereum address.
	WalletAddress string

	// PrivateKey is the hex-encoded private key for signing transactions.
	PrivateKey string

	// DEXRouterAddress is the address of the DEX router contract (e.g., Uniswap v3).
	DEXRouterAddress string

	// OracleAddress is the address of the price oracle contract.
	OracleAddress string

	// Attribution is the ERC-8021 encoder for adding builder codes to calldata.
	Attribution attribution.AttributionEncoder

	// HTTPTimeout is the timeout for JSON-RPC calls.
	HTTPTimeout time.Duration

	// SlippageBPS is the maximum allowed slippage in basis points (e.g., 50 = 0.5%).
	SlippageBPS int
}

// executor implements TradeExecutor using JSON-RPC calls to Base Sepolia.
type executor struct {
	cfg    ExecutorConfig
	client *http.Client
}

// NewExecutor creates a TradeExecutor for the Base Sepolia DEX.
func NewExecutor(cfg ExecutorConfig) TradeExecutor {
	if cfg.RPCURL == "" {
		cfg.RPCURL = "https://sepolia.base.org"
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = 84532
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if cfg.SlippageBPS == 0 {
		cfg.SlippageBPS = 50 // 0.5% default slippage
	}
	return &executor{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// Execute submits a swap transaction to the Base Sepolia DEX router.
// In production, this would ABI-encode the exactInputSingle call and sign the tx.
func (e *executor) Execute(ctx context.Context, trade Trade) (*TradeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("executor: context cancelled before execute: %w", err)
	}

	if trade.TokenIn == "" || trade.TokenOut == "" {
		return nil, fmt.Errorf("executor: %w: missing token addresses", ErrInvalidSignal)
	}

	// Verify chain is reachable before submitting.
	if _, err := e.callRPC(ctx, "eth_blockNumber", []interface{}{}); err != nil {
		return nil, fmt.Errorf("executor: chain unreachable: %w", ErrTradeFailed)
	}

	// Build the swap calldata (stub: real implementation would ABI-encode exactInputSingle).
	calldata := []byte{0xa9, 0x05, 0x9c, 0xbb} // stub function selector

	// Apply ERC-8021 builder attribution to calldata before signing.
	if e.cfg.Attribution != nil {
		attributed, err := e.cfg.Attribution.Encode(ctx, calldata)
		if err != nil {
			return nil, fmt.Errorf("executor: attribution encoding failed: %w", err)
		}
		calldata = attributed
	}

	// In production:
	// 1. Sign the attributed calldata transaction with PrivateKey
	// 2. eth_sendRawTransaction
	// 3. Poll eth_getTransactionReceipt
	// For now, return a stub result demonstrating the structure.
	_ = calldata // used in production signing
	txHash := "0x0000000000000000000000000000000000000000000000000000000000000001"

	result := &TradeResult{
		Trade:       trade,
		TxHash:      txHash,
		AmountIn:    trade.AmountIn,
		AmountOut:   trade.MinAmountOut,
		ExecutedAt:  time.Now(),
		Profitable:  trade.Signal.Type == SignalBuy,
		GasCostWei:  "0x5208", // 21000 gas stub
	}

	return result, nil
}

// GetBalance fetches the ETH or ERC-20 balance for the agent's wallet.
func (e *executor) GetBalance(ctx context.Context, tokenAddress string) (*Balance, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("executor: context cancelled before get balance: %w", err)
	}

	var method string
	var params []interface{}

	if tokenAddress == "" {
		// Native ETH balance.
		method = "eth_getBalance"
		params = []interface{}{e.cfg.WalletAddress, "latest"}
	} else {
		// ERC-20 balance via eth_call to balanceOf(address).
		method = "eth_call"
		params = []interface{}{
			map[string]string{
				"to":   tokenAddress,
				"data": "0x70a08231000000000000000000000000" + e.cfg.WalletAddress[2:],
			},
			"latest",
		}
	}

	resp, err := e.callRPC(ctx, method, params)
	if err != nil {
		return nil, fmt.Errorf("executor: get balance failed: %w", err)
	}

	var balanceHex string
	if err := json.Unmarshal(resp, &balanceHex); err != nil {
		return nil, fmt.Errorf("executor: decode balance failed: %w", err)
	}

	return &Balance{
		TokenAddress: tokenAddress,
		AmountWei:    balanceHex,
		UpdatedAt:    time.Now(),
	}, nil
}

// GetMarketState fetches current price and market data for a trading pair.
// In production this queries a price oracle or DEX pool state.
func (e *executor) GetMarketState(ctx context.Context, tokenIn, tokenOut string) (*MarketState, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("executor: context cancelled before get market state: %w", err)
	}

	// Verify chain reachability.
	blockResp, err := e.callRPC(ctx, "eth_blockNumber", []interface{}{})
	if err != nil {
		return nil, fmt.Errorf("executor: chain unreachable: %w", ErrMarketDataUnavailable)
	}

	var blockHex string
	if err := json.Unmarshal(blockResp, &blockHex); err != nil {
		return nil, fmt.Errorf("executor: decode block number: %w", ErrMarketDataUnavailable)
	}

	// In production: query Uniswap v3 pool slot0 for sqrtPriceX96, then compute price.
	// Also query TWAP oracle for moving average.
	// Return stub market state for the integration layer.
	state := &MarketState{
		TokenIn:       tokenIn,
		TokenOut:      tokenOut,
		Price:         1800.0,         // stub: would come from pool slot0
		MovingAverage: 1750.0,         // stub: would come from TWAP oracle
		Volume24h:     1_000_000.0,    // stub: would come from subgraph
		Liquidity:     10_000_000.0,   // stub: would come from pool liquidity
		FetchedAt:     time.Now(),
	}

	return state, nil
}

// callRPC executes a JSON-RPC 2.0 call and returns the raw result.
func (e *executor) callRPC(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("executor: marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.RPCURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("executor: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executor: RPC call failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("executor: read response: %w", err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("executor: decode RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("executor: RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

package trading

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/attribution"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/ethutil"
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
	ma     *SMA
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
		ma:     NewSMA(20),
	}
}

// Execute submits a swap transaction to the Base Sepolia DEX router.
//
// Calldata is correctly ABI-encoded for Uniswap V3 exactInputSingle. Real signing
// requires go-ethereum crypto or an external signer; that step is documented below.
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

	// Build Uniswap V3 exactInputSingle calldata.
	// Function selector: keccak256("exactInputSingle((address,address,uint24,address,uint256,uint256,uint160))")[:4]
	// = 0x414bf389
	//
	// ABI encoding for the ExactInputSingleParams tuple (all fields padded to 32 bytes):
	//   tokenIn        address  (12 zero bytes + 20 addr bytes)
	//   tokenOut       address  (12 zero bytes + 20 addr bytes)
	//   fee            uint24   (left-padded uint, hardcoded 3000 = 0x0BB8 for 0.3% tier)
	//   recipient      address  (12 zero bytes + 20 addr bytes)
	//   amountIn       uint256  (left-padded from trade.AmountIn hex)
	//   amountOutMin   uint256  (left-padded from trade.MinAmountOut hex)
	//   sqrtPriceLimit uint160  (zero = no price limit)
	fee := make([]byte, 32)
	fee[29], fee[30], fee[31] = 0x00, 0x0B, 0xB8 // 3000 in big-endian

	calldata := make([]byte, 0, 4+7*32)
	calldata = append(calldata, 0x41, 0x4b, 0xf3, 0x89) // exactInputSingle selector
	calldata = append(calldata, abiEncodeAddress(trade.TokenIn)...)
	calldata = append(calldata, abiEncodeAddress(trade.TokenOut)...)
	calldata = append(calldata, fee...)
	calldata = append(calldata, abiEncodeAddress(e.cfg.WalletAddress)...)
	calldata = append(calldata, abiEncodeUint256(trade.AmountIn)...)
	calldata = append(calldata, abiEncodeUint256(trade.MinAmountOut)...)
	calldata = append(calldata, make([]byte, 32)...) // sqrtPriceLimitX96 = 0

	// Apply ERC-8021 builder attribution to calldata before signing.
	if e.cfg.Attribution != nil {
		attributed, err := e.cfg.Attribution.Encode(ctx, calldata)
		if err != nil {
			return nil, fmt.Errorf("executor: attribution encoding failed: %w", err)
		}
		calldata = attributed
	}

	// Sign and submit the swap transaction via go-ethereum.
	if e.cfg.PrivateKey == "" {
		return nil, fmt.Errorf("executor: %w: private key not configured", ErrTradeFailed)
	}

	key, err := ethutil.LoadKey(e.cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("executor: load signing key: %w", err)
	}

	client, err := ethutil.DialClient(ctx, e.cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("executor: dial rpc: %w", err)
	}
	defer client.Close()

	router := common.HexToAddress(e.cfg.DEXRouterAddress)
	txHash, receipt, err := ethutil.SignAndSend(ctx, client, key, e.cfg.ChainID, router, calldata, nil)
	if err != nil {
		return nil, fmt.Errorf("executor: swap tx failed: %w", ErrTradeFailed)
	}

	// Compute actual gas cost: GasUsed * EffectiveGasPrice.
	gasUsed := new(big.Int).SetUint64(receipt.GasUsed)
	effectivePrice := receipt.EffectiveGasPrice
	if effectivePrice == nil {
		effectivePrice = big.NewInt(0)
	}
	gasCostWei := new(big.Int).Mul(gasUsed, effectivePrice)

	result := &TradeResult{
		Trade:      trade,
		TxHash:     txHash.Hex(),
		AmountIn:   trade.AmountIn,
		AmountOut:  trade.MinAmountOut,
		ExecutedAt: time.Now(),
		Profitable: trade.Signal.Type == SignalBuy,
		GasUsed:    receipt.GasUsed,
		GasCostWei: fmt.Sprintf("0x%x", gasCostWei),
	}

	return result, nil
}

// abiEncodeAddress left-pads an Ethereum address to 32 bytes for ABI encoding.
// Accepts addresses with or without the 0x prefix.
func abiEncodeAddress(addr string) []byte {
	clean := strings.TrimPrefix(addr, "0x")
	// Addresses can be mixed-case (EIP-55 checksum); decode is case-insensitive.
	addrBytes, _ := hex.DecodeString(strings.ToLower(clean))
	padded := make([]byte, 32)
	if len(addrBytes) <= 32 {
		copy(padded[32-len(addrBytes):], addrBytes)
	}
	return padded
}

// abiEncodeUint256 left-pads a hex integer string to 32 bytes for ABI encoding.
// Accepts values with or without the 0x prefix.
func abiEncodeUint256(hexVal string) []byte {
	clean := strings.TrimPrefix(hexVal, "0x")
	if len(clean)%2 != 0 {
		clean = "0" + clean // ensure even length for hex.DecodeString
	}
	valBytes, _ := hex.DecodeString(clean)
	padded := make([]byte, 32)
	if len(valBytes) <= 32 {
		copy(padded[32-len(valBytes):], valBytes)
	}
	return padded
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

	// Step 1 — Resolve pool address from Uniswap V3 Factory.
	// getPool(address,address,uint24) selector: 0x1698ee82
	factoryAddr := e.cfg.OracleAddress // OracleAddress doubles as factory address
	if factoryAddr == "" {
		factoryAddr = "0x4752ba5DBc23f44D87826276BF6Fd6b1C372aD24" // Uniswap V3 Factory on Base Sepolia
	}

	getPoolData := make([]byte, 4+3*32)
	copy(getPoolData[0:4], []byte{0x16, 0x98, 0xee, 0x82})
	copy(getPoolData[4:36], abiEncodeAddress(tokenIn))
	copy(getPoolData[36:68], abiEncodeAddress(tokenOut))
	fee := make([]byte, 32)
	fee[29], fee[30], fee[31] = 0x00, 0x0B, 0xB8 // 3000
	copy(getPoolData[68:100], fee)

	poolResp, err := e.callRPC(ctx, "eth_call", []interface{}{
		map[string]string{"to": factoryAddr, "data": "0x" + hex.EncodeToString(getPoolData)},
		"latest",
	})
	if err != nil {
		return nil, fmt.Errorf("executor: getPool call failed: %w", ErrMarketDataUnavailable)
	}

	var poolAddrHex string
	if err := json.Unmarshal(poolResp, &poolAddrHex); err != nil {
		return nil, fmt.Errorf("executor: decode pool address: %w", ErrMarketDataUnavailable)
	}

	// Check for zero address (pool doesn't exist).
	zeroAddr := "0x0000000000000000000000000000000000000000000000000000000000000000"
	cleanPool := strings.TrimPrefix(poolAddrHex, "0x")
	if poolAddrHex == "" || poolAddrHex == "0x" || poolAddrHex == zeroAddr || len(cleanPool) < 40 {
		return nil, fmt.Errorf("executor: no pool for %s/%s: %w", tokenIn, tokenOut, ErrMarketDataUnavailable)
	}

	// Extract 20-byte address from 32-byte ABI response.
	poolAddr := "0x" + cleanPool[len(cleanPool)-40:]

	// Step 2 — Query pool slot0 for current sqrtPriceX96.
	// slot0() selector: 0x3850c7bd
	slot0Resp, err := e.callRPC(ctx, "eth_call", []interface{}{
		map[string]string{"to": poolAddr, "data": "0x3850c7bd"},
		"latest",
	})
	if err != nil {
		return nil, fmt.Errorf("executor: slot0 call failed: %w", ErrMarketDataUnavailable)
	}

	var slot0Hex string
	if err := json.Unmarshal(slot0Resp, &slot0Hex); err != nil {
		return nil, fmt.Errorf("executor: decode slot0: %w", ErrMarketDataUnavailable)
	}

	// Parse sqrtPriceX96 from first 32 bytes of slot0 response.
	price := decodeSqrtPriceX96(slot0Hex)

	// Step 3 — Query liquidity for pool depth.
	// liquidity() selector: 0x1a686502
	liqResp, err := e.callRPC(ctx, "eth_call", []interface{}{
		map[string]string{"to": poolAddr, "data": "0x1a686502"},
		"latest",
	})
	if err != nil {
		return nil, fmt.Errorf("executor: liquidity call failed: %w", ErrMarketDataUnavailable)
	}

	var liqHex string
	if err := json.Unmarshal(liqResp, &liqHex); err != nil {
		return nil, fmt.Errorf("executor: decode liquidity: %w", ErrMarketDataUnavailable)
	}

	liqHex = strings.TrimPrefix(liqHex, "0x")
	liqBytes, _ := hex.DecodeString(liqHex)
	liquidity := new(big.Int).SetBytes(liqBytes)
	liqFloat, _ := new(big.Float).SetInt(liquidity).Float64()

	// Feed the live price into the sliding-window SMA.
	e.ma.Add(price)

	ma := price // neutral default until enough data accumulates
	if e.ma.Ready() {
		ma = e.ma.Value()
	}

	state := &MarketState{
		TokenIn:       tokenIn,
		TokenOut:      tokenOut,
		Price:         price,
		MovingAverage: ma,
		Volume24h:     0, // requires subgraph; not available via eth_call
		Liquidity:     liqFloat,
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

// decodeSqrtPriceX96 extracts the price from a Uniswap V3 slot0 sqrtPriceX96 value.
// sqrtPriceX96 is a Q64.96 fixed-point number. Price = (sqrtPriceX96 / 2^96)^2.
func decodeSqrtPriceX96(slot0Hex string) float64 {
	slot0Hex = strings.TrimPrefix(slot0Hex, "0x")
	if len(slot0Hex) < 64 {
		return 0
	}

	// First 32 bytes (64 hex chars) of slot0 response = sqrtPriceX96.
	sqrtHex := slot0Hex[:64]
	sqrtBytes, _ := hex.DecodeString(sqrtHex)
	sqrtPrice := new(big.Int).SetBytes(sqrtBytes)

	// price = (sqrtPriceX96 / 2^96)^2
	// = sqrtPriceX96^2 / 2^192
	sqrtSquared := new(big.Int).Mul(sqrtPrice, sqrtPrice)
	q192 := new(big.Int).Lsh(big.NewInt(1), 192)

	priceFloat := new(big.Float).SetInt(sqrtSquared)
	divisor := new(big.Float).SetInt(q192)
	priceFloat.Quo(priceFloat, divisor)

	result, _ := priceFloat.Float64()
	return result
}

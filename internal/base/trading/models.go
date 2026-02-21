// Package trading implements DeFi trading strategies, execution, and P&L tracking
// on Base Sepolia via DEX interaction.
package trading

import (
	"errors"
	"time"
)

// Sentinel errors for trading operations.
var (
	// ErrTradeFailed is returned when a trade transaction fails on-chain.
	ErrTradeFailed = errors.New("trading: trade transaction failed on Base chain")

	// ErrInsufficientLiquidity is returned when there is insufficient DEX liquidity
	// to fill a trade at the requested size.
	ErrInsufficientLiquidity = errors.New("trading: insufficient DEX liquidity for trade")

	// ErrInvalidSignal is returned when a strategy produces a signal with
	// invalid or missing required fields.
	ErrInvalidSignal = errors.New("trading: invalid or malformed trading signal")

	// ErrMarketDataUnavailable is returned when market data cannot be fetched
	// from the DEX or oracle.
	ErrMarketDataUnavailable = errors.New("trading: market data unavailable")

	// ErrPositionExceedsMax is returned when a trade would exceed the strategy's
	// configured maximum position size.
	ErrPositionExceedsMax = errors.New("trading: trade would exceed maximum position size")
)

// SignalType represents the direction of a trading signal.
type SignalType string

const (
	// SignalBuy indicates the strategy recommends buying the base asset.
	SignalBuy SignalType = "buy"

	// SignalSell indicates the strategy recommends selling the base asset.
	SignalSell SignalType = "sell"

	// SignalHold indicates the strategy recommends holding the current position.
	SignalHold SignalType = "hold"
)

// Signal is a trading recommendation produced by a strategy.
type Signal struct {
	// Type is the signal direction: buy, sell, or hold.
	Type SignalType

	// Confidence is a value between 0.0 and 1.0 indicating signal strength.
	Confidence float64

	// SuggestedSize is the recommended trade size in base asset units.
	SuggestedSize float64

	// Reason is a human-readable explanation of why the signal was generated.
	Reason string

	// GeneratedAt is when the signal was produced.
	GeneratedAt time.Time

	// TokenIn is the address of the token to sell.
	TokenIn string

	// TokenOut is the address of the token to buy.
	TokenOut string
}

// Trade represents a DEX trade to be executed.
type Trade struct {
	// TokenIn is the address of the input token.
	TokenIn string

	// TokenOut is the address of the output token.
	TokenOut string

	// AmountIn is the amount of TokenIn to swap.
	AmountIn string

	// MinAmountOut is the minimum acceptable TokenOut amount (slippage protection).
	MinAmountOut string

	// Signal is the strategy signal that triggered this trade.
	Signal Signal

	// Deadline is the transaction deadline (Unix timestamp).
	Deadline time.Time
}

// TradeResult holds the outcome of a completed trade.
type TradeResult struct {
	// Trade is the original trade request.
	Trade Trade

	// TxHash is the on-chain transaction hash.
	TxHash string

	// AmountIn is the actual amount of TokenIn consumed.
	AmountIn string

	// AmountOut is the actual amount of TokenOut received.
	AmountOut string

	// PriceImpact is the percentage price impact of the trade.
	PriceImpact float64

	// GasUsed is the actual gas consumed by the transaction.
	GasUsed uint64

	// GasCostWei is the total gas cost in wei.
	GasCostWei string

	// ExecutedAt is when the transaction was confirmed.
	ExecutedAt time.Time

	// BlockNumber is the block at which the trade was confirmed.
	BlockNumber uint64

	// Profitable indicates whether this trade generated positive P&L.
	Profitable bool
}

// PricePoint is a single price data point in the price history.
type PricePoint struct {
	Price     float64
	Timestamp time.Time
}

// MarketState holds current market data for a trading pair.
type MarketState struct {
	// Pair is the trading pair identifier (e.g., "WETH/USDC").
	Pair string

	// TokenIn is the address of the input token.
	TokenIn string

	// TokenOut is the address of the output token.
	TokenOut string

	// Price is the current price of TokenOut in units of TokenIn.
	Price float64

	// MovingAverage is the N-period simple moving average of Price.
	MovingAverage float64

	// PriceHistory is recent price data points for strategy evaluation.
	PriceHistory []PricePoint

	// Volume24h is the 24-hour trading volume in USD.
	Volume24h float64

	// Liquidity is the available DEX liquidity in USD.
	Liquidity float64

	// FetchedAt is when this market state was fetched.
	FetchedAt time.Time
}

// Balance holds the agent's token balances.
type Balance struct {
	// TokenAddress is the ERC-20 token contract address.
	// Empty string represents native ETH.
	TokenAddress string

	// AmountWei is the balance in wei (smallest unit).
	AmountWei string

	// Symbol is the human-readable token symbol.
	Symbol string

	// UpdatedAt is when this balance was last fetched.
	UpdatedAt time.Time
}

// Position represents a current open position in a trading pair.
type Position struct {
	// TokenIn is the address of the token we sold.
	TokenIn string

	// TokenOut is the address of the token we bought.
	TokenOut string

	// Size is the current position size in TokenOut units.
	Size float64

	// EntryPrice is the average price paid for this position.
	EntryPrice float64

	// OpenedAt is when the position was first opened.
	OpenedAt time.Time
}

// TradeRecord is a historical record of a completed trade for P&L accounting.
type TradeRecord struct {
	// TradeResult is the execution result.
	TradeResult TradeResult

	// Revenue is the revenue from this trade in USD.
	Revenue float64

	// Cost is the cost basis for this trade in USD.
	Cost float64

	// PnL is the profit or loss for this trade (Revenue - Cost).
	PnL float64

	// RecordedAt is when this record was created.
	RecordedAt time.Time
}

// GasCost records gas expenditure for a single transaction.
type GasCost struct {
	// TxHash is the transaction hash this gas cost is for.
	TxHash string

	// GasUsed is the amount of gas consumed.
	GasUsed uint64

	// GasPriceWei is the gas price at time of the transaction.
	GasPriceWei string

	// CostWei is the total cost in wei (GasUsed * GasPriceWei).
	CostWei string

	// CostUSD is the gas cost converted to USD.
	CostUSD float64

	// RecordedAt is when this gas cost was recorded.
	RecordedAt time.Time
}

// Fee records a protocol or DEX fee paid during trading.
type Fee struct {
	// TxHash is the transaction this fee applies to.
	TxHash string

	// Type is the fee type (e.g., "swap_fee", "protocol_fee").
	Type string

	// AmountUSD is the fee amount in USD.
	AmountUSD float64

	// RecordedAt is when this fee was recorded.
	RecordedAt time.Time
}

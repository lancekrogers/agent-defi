package trading

import (
	"context"
	"fmt"
	"time"
)

// mockExecutor implements TradeExecutor without touching the blockchain.
// Used when DEFI_MOCK_MODE=true to allow the agent to run without funded wallets.
type mockExecutor struct {
	ma *SMA
}

// NewMockExecutor creates an in-memory TradeExecutor for dry-run mode.
func NewMockExecutor() TradeExecutor {
	return &mockExecutor{
		ma: NewSMA(20),
	}
}

func (m *mockExecutor) Execute(_ context.Context, trade Trade) (*TradeResult, error) {
	if trade.TokenIn == "" || trade.TokenOut == "" {
		return nil, fmt.Errorf("executor: %w: missing token addresses", ErrInvalidSignal)
	}

	return &TradeResult{
		Trade:      trade,
		TxHash:     fmt.Sprintf("0xmock_trade_%d", time.Now().UnixNano()),
		AmountIn:   trade.AmountIn,
		AmountOut:  trade.MinAmountOut,
		ExecutedAt: time.Now(),
		Profitable: trade.Signal.Type == SignalBuy,
		GasUsed:    21000,
		GasCostWei: "0x0",
	}, nil
}

func (m *mockExecutor) GetBalance(_ context.Context, _ string) (*Balance, error) {
	return &Balance{
		AmountWei: "0x56bc75e2d63100000", // 100 ETH in wei
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockExecutor) GetMarketState(_ context.Context, tokenIn, tokenOut string) (*MarketState, error) {
	price := 2500.0
	m.ma.Add(price)

	ma := price
	if m.ma.Ready() {
		ma = m.ma.Value()
	}

	return &MarketState{
		TokenIn:       tokenIn,
		TokenOut:      tokenOut,
		Price:         price,
		MovingAverage: ma,
		Volume24h:     1_000_000,
		Liquidity:     5_000_000,
		FetchedAt:     time.Now(),
	}, nil
}

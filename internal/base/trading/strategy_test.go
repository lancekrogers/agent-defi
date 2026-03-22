package trading

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testMarket(price, ma float64) MarketState {
	return MarketState{
		TokenIn:       "0xusdc",
		TokenOut:      "0xweth",
		Price:         price,
		MovingAverage: ma,
		Volume24h:     1_000_000,
		Liquidity:     10_000_000,
		FetchedAt:     time.Now(),
	}
}

func testStrategy() *MeanReversionStrategy {
	return NewMeanReversionStrategy(MeanReversionConfig{
		TokenIn:            "0xusdc",
		TokenOut:           "0xweth",
		BuyThreshold:       0.02,
		SellThreshold:      0.02,
		MaxPositionSize:    1.0,
		MinLiquidity:       1000,
		DataStalenessLimit: 5 * time.Minute,
	})
}

func TestStrategy_Name(t *testing.T) {
	s := testStrategy()
	if s.Name() != "mean_reversion" {
		t.Errorf("expected mean_reversion, got %s", s.Name())
	}
}

func TestStrategy_MaxPosition(t *testing.T) {
	s := testStrategy()
	if s.MaxPosition() != 1.0 {
		t.Errorf("expected 1.0, got %f", s.MaxPosition())
	}
}

func TestEvaluate_BuySignal(t *testing.T) {
	s := testStrategy()

	// Price 3% below moving average = buy signal.
	market := testMarket(1700, 1750) // 1700/1750 = ~2.86% below

	signal, err := s.Evaluate(context.Background(), market)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Type != SignalBuy {
		t.Errorf("expected buy signal, got %s", signal.Type)
	}
	if signal.Confidence <= 0 {
		t.Error("expected positive confidence for buy signal")
	}
	if signal.SuggestedSize <= 0 {
		t.Error("expected positive suggested size for buy signal")
	}
	if signal.SuggestedSize > s.MaxPosition() {
		t.Errorf("suggested size %f exceeds max position %f", signal.SuggestedSize, s.MaxPosition())
	}
}

func TestEvaluate_SellSignal(t *testing.T) {
	s := testStrategy()

	// Price 3% above moving average = sell signal.
	market := testMarket(1803, 1750) // 1803/1750 = ~3% above

	signal, err := s.Evaluate(context.Background(), market)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Type != SignalSell {
		t.Errorf("expected sell signal, got %s", signal.Type)
	}
	if signal.Confidence <= 0 {
		t.Error("expected positive confidence for sell signal")
	}
}

func TestEvaluate_HoldSignal(t *testing.T) {
	s := testStrategy()

	// Price within 1% of moving average = hold signal.
	market := testMarket(1755, 1750) // 0.28% above - within 2% threshold

	signal, err := s.Evaluate(context.Background(), market)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Type != SignalHold {
		t.Errorf("expected hold signal, got %s", signal.Type)
	}
}

func TestEvaluate_StaleData(t *testing.T) {
	s := testStrategy()

	market := testMarket(1700, 1750)
	market.FetchedAt = time.Now().Add(-10 * time.Minute) // stale data

	_, err := s.Evaluate(context.Background(), market)
	if err == nil {
		t.Fatal("expected error for stale market data")
	}
	if !errors.Is(err, ErrMarketDataUnavailable) {
		t.Errorf("expected ErrMarketDataUnavailable, got %v", err)
	}
}

func TestEvaluate_ZeroPrice(t *testing.T) {
	s := testStrategy()

	market := testMarket(0, 1750)

	_, err := s.Evaluate(context.Background(), market)
	if err == nil {
		t.Fatal("expected error for zero price")
	}
	if !errors.Is(err, ErrMarketDataUnavailable) {
		t.Errorf("expected ErrMarketDataUnavailable, got %v", err)
	}
}

func TestEvaluate_InsufficientLiquidity(t *testing.T) {
	s := NewMeanReversionStrategy(MeanReversionConfig{
		TokenIn:            "0xusdc",
		TokenOut:           "0xweth",
		BuyThreshold:       0.02,
		SellThreshold:      0.02,
		MaxPositionSize:    1.0,
		MinLiquidity:       1_000_000,
		DataStalenessLimit: 5 * time.Minute,
	})

	market := testMarket(1700, 1750)
	market.Liquidity = 500 // well below MinLiquidity

	_, err := s.Evaluate(context.Background(), market)
	if err == nil {
		t.Fatal("expected error for insufficient liquidity")
	}
	if !errors.Is(err, ErrInsufficientLiquidity) {
		t.Errorf("expected ErrInsufficientLiquidity, got %v", err)
	}
}

func TestEvaluate_ContextCancelled(t *testing.T) {
	s := testStrategy()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Evaluate(ctx, testMarket(1700, 1750))
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEvaluate_SignalHasTimestamp(t *testing.T) {
	s := testStrategy()
	before := time.Now()

	signal, err := s.Evaluate(context.Background(), testMarket(1700, 1750))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signal.GeneratedAt.Before(before) {
		t.Error("signal timestamp should be after test start")
	}
}

func TestEvaluate_SignalHasTokens(t *testing.T) {
	s := testStrategy()

	signal, err := s.Evaluate(context.Background(), testMarket(1700, 1750))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signal.TokenIn != "0xusdc" {
		t.Errorf("expected 0xusdc, got %s", signal.TokenIn)
	}
	if signal.TokenOut != "0xweth" {
		t.Errorf("expected 0xweth, got %s", signal.TokenOut)
	}
}

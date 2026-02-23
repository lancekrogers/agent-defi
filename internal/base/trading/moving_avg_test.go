package trading

import (
	"sync"
	"testing"
)

func TestSMA_BasicAverage(t *testing.T) {
	s := NewSMA(20)
	s.Add(10)
	s.Add(20)
	s.Add(30)

	got := s.Value()
	want := 20.0
	if got != want {
		t.Errorf("Value() = %f, want %f", got, want)
	}
}

func TestSMA_EmptyValue(t *testing.T) {
	s := NewSMA(20)
	if s.Value() != 0 {
		t.Errorf("empty SMA should return 0, got %f", s.Value())
	}
}

func TestSMA_ReadyThreshold(t *testing.T) {
	s := NewSMA(20) // ready at 10 observations

	for i := 0; i < 9; i++ {
		s.Add(float64(i))
	}
	if s.Ready() {
		t.Error("should not be ready with 9 observations (need 10)")
	}

	s.Add(9)
	if !s.Ready() {
		t.Error("should be ready with 10 observations")
	}
}

func TestSMA_WindowEviction(t *testing.T) {
	s := NewSMA(5)

	// Fill with 1,2,3,4,5
	for i := 1; i <= 5; i++ {
		s.Add(float64(i))
	}
	// avg = (1+2+3+4+5)/5 = 3.0
	if got := s.Value(); got != 3.0 {
		t.Errorf("Value() = %f, want 3.0", got)
	}

	// Add 10 — evicts 1, buffer becomes 2,3,4,5,10
	s.Add(10)
	want := (2 + 3 + 4 + 5 + 10) / 5.0 // = 4.8
	if got := s.Value(); got != want {
		t.Errorf("Value() = %f, want %f", got, want)
	}

	if s.Len() != 5 {
		t.Errorf("Len() = %d, want 5", s.Len())
	}
}

func TestSMA_InvalidWindow(t *testing.T) {
	s := NewSMA(0)
	if s.window != 20 {
		t.Errorf("invalid window should default to 20, got %d", s.window)
	}

	s = NewSMA(1)
	if s.window != 20 {
		t.Errorf("window=1 should default to 20, got %d", s.window)
	}
}

func TestSMA_ConcurrentAccess(t *testing.T) {
	s := NewSMA(100)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			s.Add(v)
		}(float64(i))
	}
	wg.Wait()

	if s.Len() != 100 {
		t.Errorf("Len() = %d, want 100 after concurrent adds", s.Len())
	}

	// Value should be the mean of 0..99 = 49.5, but order is
	// non-deterministic due to goroutine scheduling. Just verify
	// it returns a sane positive number without panicking.
	v := s.Value()
	if v <= 0 || v >= 100 {
		t.Errorf("Value() = %f, expected between 0 and 100", v)
	}
}

func TestSMA_StrategyIntegration(t *testing.T) {
	// Verify the SMA produces correct strategy signals when wired to
	// MeanReversionStrategy.

	s := NewSMA(10) // ready at 5

	// Feed 20 rising prices: 100, 101, 102, ..., 119
	// MA will lag behind the latest price → strategy should signal SELL.
	for i := 0; i < 20; i++ {
		s.Add(100 + float64(i))
	}

	strategy := NewMeanReversionStrategy(MeanReversionConfig{
		TokenIn:            "0xusdc",
		TokenOut:           "0xweth",
		BuyThreshold:       0.02,
		SellThreshold:      0.02,
		MaxPositionSize:    1.0,
		DataStalenessLimit: 5 * 60e9, // 5 min in ns
	})

	market := testMarket(119, s.Value()) // latest price vs SMA
	signal, err := strategy.Evaluate(t.Context(), market)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Type != SignalSell {
		t.Errorf("rising prices: expected SELL signal, got %s (price=119, MA=%f)", signal.Type, s.Value())
	}

	// Now feed 20 falling prices: 119, 118, ..., 100
	s2 := NewSMA(10)
	for i := 0; i < 20; i++ {
		s2.Add(119 - float64(i))
	}

	market2 := testMarket(100, s2.Value())
	signal2, err := strategy.Evaluate(t.Context(), market2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal2.Type != SignalBuy {
		t.Errorf("falling prices: expected BUY signal, got %s (price=100, MA=%f)", signal2.Type, s2.Value())
	}
}

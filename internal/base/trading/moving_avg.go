package trading

import "sync"

// SMA computes a simple moving average over a sliding window of prices.
// It is safe for concurrent use.
type SMA struct {
	mu     sync.Mutex
	buf    []float64
	window int
}

// NewSMA creates an SMA with the given window size.
// Window must be at least 2; defaults to 20 if invalid.
func NewSMA(window int) *SMA {
	if window < 2 {
		window = 20
	}
	return &SMA{
		buf:    make([]float64, 0, window),
		window: window,
	}
}

// Add records a new price observation. If the buffer exceeds the window
// size, the oldest entry is evicted.
func (s *SMA) Add(price float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.buf) >= s.window {
		s.buf = s.buf[1:]
	}
	s.buf = append(s.buf, price)
}

// Value returns the arithmetic mean of all prices in the buffer.
// Returns 0 if the buffer is empty.
func (s *SMA) Value() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.buf)
	if n == 0 {
		return 0
	}

	var sum float64
	for _, p := range s.buf {
		sum += p
	}
	return sum / float64(n)
}

// Ready returns true when the buffer has at least half the window size
// worth of observations — enough data to produce a meaningful average.
func (s *SMA) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf) >= s.window/2
}

// Len returns the current number of observations in the buffer.
func (s *SMA) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

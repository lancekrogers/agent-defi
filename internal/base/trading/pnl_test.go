package trading

import (
	"sync"
	"testing"
	"time"
)

// reportAll generates a report from the beginning of time to far in the future.
func reportAll(tracker *PnLTracker) *PnLReport {
	return tracker.Report(time.Time{}, time.Now().Add(24*time.Hour))
}

func testTradeRecord(revenue, cost float64, profitable bool) TradeRecord {
	return TradeRecord{
		TradeResult: TradeResult{
			TxHash:     "0xabc",
			Profitable: profitable,
		},
		Revenue:    revenue,
		Cost:       cost,
		RecordedAt: time.Now(),
	}
}

func TestPnLTracker_RecordTrade(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.RecordTrade(testTradeRecord(100, 80, true))

	if tracker.TradeCount() != 1 {
		t.Errorf("expected 1 trade, got %d", tracker.TradeCount())
	}
}

func TestPnLTracker_Report_BasicMetrics(t *testing.T) {
	tracker := NewPnLTracker()

	// 3 trades: 2 profitable, 1 loss.
	tracker.RecordTrade(testTradeRecord(100, 80, true))
	tracker.RecordTrade(testTradeRecord(200, 150, true))
	tracker.RecordTrade(testTradeRecord(50, 90, false))

	tracker.RecordGasCost(GasCost{
		TxHash:  "0x1",
		CostUSD: 5.0,
	})
	tracker.RecordFee(Fee{
		TxHash:    "0x1",
		Type:      "swap_fee",
		AmountUSD: 2.0,
	})

	report := reportAll(tracker)

	if report.TradeCount != 3 {
		t.Errorf("expected 3 trades, got %d", report.TradeCount)
	}
	if report.WinCount != 2 {
		t.Errorf("expected 2 wins, got %d", report.WinCount)
	}
	if report.LossCount != 1 {
		t.Errorf("expected 1 loss, got %d", report.LossCount)
	}

	expectedRevenue := 100.0 + 200.0 + 50.0 // 350
	if report.TotalRevenue != expectedRevenue {
		t.Errorf("expected revenue %.2f, got %.2f", expectedRevenue, report.TotalRevenue)
	}

	if report.TotalGasCosts != 5.0 {
		t.Errorf("expected gas costs 5.0, got %.2f", report.TotalGasCosts)
	}

	if report.TotalFees != 2.0 {
		t.Errorf("expected fees 2.0, got %.2f", report.TotalFees)
	}
}

func TestPnLTracker_WinRate(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.RecordTrade(testTradeRecord(100, 80, true))
	tracker.RecordTrade(testTradeRecord(50, 90, false))
	tracker.RecordTrade(testTradeRecord(200, 150, true))
	tracker.RecordTrade(testTradeRecord(30, 60, false))

	report := reportAll(tracker)

	expectedWinRate := 0.5 // 2 wins out of 4 trades
	if report.WinRate != expectedWinRate {
		t.Errorf("expected win rate %.2f, got %.2f", expectedWinRate, report.WinRate)
	}
}

func TestPnLTracker_IsSelfSustaining_True(t *testing.T) {
	tracker := NewPnLTracker()

	// Revenue exceeds all costs.
	tracker.RecordTrade(testTradeRecord(1000, 800, true))
	tracker.RecordGasCost(GasCost{CostUSD: 10.0})
	tracker.RecordFee(Fee{AmountUSD: 5.0})

	if !tracker.IsSelfSustaining() {
		t.Error("expected self-sustaining to be true")
	}

	report := reportAll(tracker)
	if !report.IsSelfSustaining {
		t.Error("expected report IsSelfSustaining to be true")
	}
	if report.NetPnL <= 0 {
		t.Errorf("expected positive NetPnL, got %.2f", report.NetPnL)
	}
}

func TestPnLTracker_IsSelfSustaining_False(t *testing.T) {
	tracker := NewPnLTracker()

	// Gas costs exceed revenue.
	tracker.RecordTrade(testTradeRecord(10, 8, true))
	tracker.RecordGasCost(GasCost{CostUSD: 100.0})

	if tracker.IsSelfSustaining() {
		t.Error("expected self-sustaining to be false when costs exceed revenue")
	}
}

func TestPnLTracker_EmptyReport(t *testing.T) {
	tracker := NewPnLTracker()

	report := reportAll(tracker)

	if report.TradeCount != 0 {
		t.Errorf("expected 0 trades, got %d", report.TradeCount)
	}
	if report.WinRate != 0 {
		t.Errorf("expected 0 win rate, got %.2f", report.WinRate)
	}
	if report.NetPnL != 0 {
		t.Errorf("expected 0 net PnL, got %.2f", report.NetPnL)
	}
	if report.IsSelfSustaining {
		t.Error("empty tracker should not be self-sustaining")
	}
}

func TestPnLTracker_NetPnL_Calculation(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.RecordTrade(testTradeRecord(500, 400, true)) // revenue: 500
	tracker.RecordGasCost(GasCost{CostUSD: 25.0})        // gas: 25
	tracker.RecordFee(Fee{AmountUSD: 10.0})              // fees: 10
	// NetPnL = 500 - 25 - 10 = 465

	report := reportAll(tracker)

	expectedNet := 500.0 - 25.0 - 10.0
	if report.NetPnL != expectedNet {
		t.Errorf("expected net PnL %.2f, got %.2f", expectedNet, report.NetPnL)
	}
}

func TestPnLTracker_TimeFiltering(t *testing.T) {
	tracker := NewPnLTracker()

	// Record trades at different times.
	early := time.Now().Add(-2 * time.Hour)
	middle := time.Now().Add(-1 * time.Hour)
	late := time.Now()

	tracker.RecordTrade(TradeRecord{
		Revenue:    100,
		Cost:       80,
		PnL:        20,
		RecordedAt: early,
	})
	tracker.RecordTrade(TradeRecord{
		Revenue:    200,
		Cost:       150,
		PnL:        50,
		RecordedAt: middle,
	})
	tracker.RecordTrade(TradeRecord{
		Revenue:    300,
		Cost:       250,
		PnL:        50,
		RecordedAt: late,
	})

	tracker.RecordGasCost(GasCost{CostUSD: 10, RecordedAt: early})
	tracker.RecordGasCost(GasCost{CostUSD: 20, RecordedAt: middle})
	tracker.RecordGasCost(GasCost{CostUSD: 30, RecordedAt: late})

	// Report covering only the middle trade.
	report := tracker.Report(
		middle.Add(-time.Minute),
		middle.Add(time.Minute),
	)

	if report.TradeCount != 1 {
		t.Errorf("expected 1 trade in time window, got %d", report.TradeCount)
	}
	if report.TotalRevenue != 200 {
		t.Errorf("expected revenue 200, got %.2f", report.TotalRevenue)
	}
	if report.TotalGasCosts != 20 {
		t.Errorf("expected gas costs 20, got %.2f", report.TotalGasCosts)
	}
}

func TestPnLTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewPnLTracker()

	var wg sync.WaitGroup
	const goroutines = 10
	const tradesPerRoutine = 100

	// Concurrent trade recording.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tradesPerRoutine; j++ {
				tracker.RecordTrade(testTradeRecord(10, 8, true))
				tracker.RecordGasCost(GasCost{CostUSD: 0.1})
				tracker.RecordFee(Fee{AmountUSD: 0.05})
			}
		}()
	}

	// Concurrent reads.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tradesPerRoutine; j++ {
				_ = reportAll(tracker)
				_ = tracker.IsSelfSustaining()
			}
		}()
	}

	wg.Wait()

	expectedTrades := goroutines * tradesPerRoutine
	if tracker.TradeCount() != expectedTrades {
		t.Errorf("expected %d trades, got %d", expectedTrades, tracker.TradeCount())
	}
}

func TestPnLTracker_AutoTimestamp(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.RecordTrade(TradeRecord{Revenue: 100})
	tracker.RecordGasCost(GasCost{CostUSD: 5})
	tracker.RecordFee(Fee{AmountUSD: 2})

	report := reportAll(tracker)
	if report.TradeCount != 1 {
		t.Errorf("expected 1 trade, got %d", report.TradeCount)
	}
}

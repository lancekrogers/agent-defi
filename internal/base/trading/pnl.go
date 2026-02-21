package trading

import (
	"sync"
	"time"
)

// PnLReport summarizes the agent's trading performance over a period.
type PnLReport struct {
	// TotalRevenue is the sum of revenue from all profitable trades in USD.
	TotalRevenue float64

	// TotalGasCosts is the total gas expenditure across all transactions in USD.
	TotalGasCosts float64

	// TotalFees is the total protocol and DEX fees paid in USD.
	TotalFees float64

	// NetPnL is TotalRevenue minus TotalGasCosts minus TotalFees.
	NetPnL float64

	// TradeCount is the total number of trades executed.
	TradeCount int

	// WinCount is the number of profitable trades.
	WinCount int

	// LossCount is the number of unprofitable trades.
	LossCount int

	// WinRate is WinCount / TradeCount (0.0 to 1.0).
	WinRate float64

	// IsSelfSustaining is true when NetPnL > 0, meaning trading revenue covers
	// all gas and fee costs.
	IsSelfSustaining bool

	// PeriodStart is when tracking began.
	PeriodStart time.Time

	// PeriodEnd is the time of this report.
	PeriodEnd time.Time
}

// PnLTracker records trades, gas costs, and fees in a thread-safe manner.
// It provides P&L reporting and self-sustainability analysis for the trading agent.
type PnLTracker struct {
	mu      sync.Mutex
	trades  []TradeRecord
	gas     []GasCost
	fees    []Fee
	started time.Time
}

// NewPnLTracker creates a new P&L tracker initialized with the current time.
func NewPnLTracker() *PnLTracker {
	return &PnLTracker{
		trades:  make([]TradeRecord, 0),
		gas:     make([]GasCost, 0),
		fees:    make([]Fee, 0),
		started: time.Now(),
	}
}

// RecordTrade adds a completed trade to the P&L ledger.
// This method is safe for concurrent use.
func (t *PnLTracker) RecordTrade(record TradeRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if record.RecordedAt.IsZero() {
		record.RecordedAt = time.Now()
	}
	record.PnL = record.Revenue - record.Cost
	t.trades = append(t.trades, record)
}

// RecordGasCost records gas expenditure for a transaction.
// This method is safe for concurrent use.
func (t *PnLTracker) RecordGasCost(cost GasCost) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cost.RecordedAt.IsZero() {
		cost.RecordedAt = time.Now()
	}
	t.gas = append(t.gas, cost)
}

// RecordFee records a protocol or DEX fee payment.
// This method is safe for concurrent use.
func (t *PnLTracker) RecordFee(fee Fee) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if fee.RecordedAt.IsZero() {
		fee.RecordedAt = time.Now()
	}
	t.fees = append(t.fees, fee)
}

// Report generates a PnLReport summarizing recorded activity within the given
// time period. Trades, gas costs, and fees recorded outside the window are excluded.
// This method is safe for concurrent use.
func (t *PnLTracker) Report(from, to time.Time) *PnLReport {
	t.mu.Lock()
	defer t.mu.Unlock()

	report := &PnLReport{
		PeriodStart: from,
		PeriodEnd:   to,
	}

	// Aggregate trade data within the time window.
	for _, trade := range t.trades {
		if trade.RecordedAt.Before(from) || trade.RecordedAt.After(to) {
			continue
		}
		report.TradeCount++
		report.TotalRevenue += trade.Revenue
		if trade.PnL > 0 {
			report.WinCount++
		} else {
			report.LossCount++
		}
	}

	// Aggregate gas costs within the time window.
	for _, gc := range t.gas {
		if gc.RecordedAt.Before(from) || gc.RecordedAt.After(to) {
			continue
		}
		report.TotalGasCosts += gc.CostUSD
	}

	// Aggregate fees within the time window.
	for _, fee := range t.fees {
		if fee.RecordedAt.Before(from) || fee.RecordedAt.After(to) {
			continue
		}
		report.TotalFees += fee.AmountUSD
	}

	// Compute derived metrics.
	report.NetPnL = report.TotalRevenue - report.TotalGasCosts - report.TotalFees
	report.IsSelfSustaining = report.NetPnL > 0

	if report.TradeCount > 0 {
		report.WinRate = float64(report.WinCount) / float64(report.TradeCount)
	}

	return report
}

// IsSelfSustaining returns true when trading revenue exceeds all costs
// from the tracker's start time through now.
// This method is safe for concurrent use.
func (t *PnLTracker) IsSelfSustaining() bool {
	return t.Report(t.started, time.Now()).IsSelfSustaining
}

// TradeCount returns the total number of recorded trades.
// This method is safe for concurrent use.
func (t *PnLTracker) TradeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.trades)
}

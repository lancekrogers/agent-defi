package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lancekrogers/agent-coordinator/pkg/daemon"
	"github.com/lancekrogers/agent-defi/internal/base/identity"
	"github.com/lancekrogers/agent-defi/internal/base/payment"
	"github.com/lancekrogers/agent-defi/internal/base/trading"
	"github.com/lancekrogers/agent-defi/internal/hcs"
)

// Mock implementations for testing

type mockIdentity struct {
	registerResult *identity.Identity
	registerErr    error
	getResult      *identity.Identity
	getErr         error
	verifyCalled   bool
}

func (m *mockIdentity) Register(_ context.Context, _ identity.RegistrationRequest) (*identity.Identity, error) {
	return m.registerResult, m.registerErr
}

func (m *mockIdentity) Verify(_ context.Context, _ string) (bool, error) {
	m.verifyCalled = true
	return true, nil
}

func (m *mockIdentity) GetIdentity(_ context.Context, _ string) (*identity.Identity, error) {
	return m.getResult, m.getErr
}

type mockPayment struct {
	payCalls int
	lastReq  payment.PaymentRequest
}

func (m *mockPayment) Pay(_ context.Context, req payment.PaymentRequest) (*payment.Receipt, error) {
	m.payCalls++
	m.lastReq = req
	return &payment.Receipt{TxHash: "0xx402mock", GasCost: big.NewInt(21000)}, nil
}

func (m *mockPayment) RequestPayment(_ context.Context, _ *big.Int, _ string) (*payment.Invoice, error) {
	return &payment.Invoice{}, nil
}

func (m *mockPayment) VerifyPayment(_ context.Context, _, _ string) (*payment.Receipt, error) {
	return &payment.Receipt{}, nil
}

func (m *mockPayment) HandlePaymentRequired(_ context.Context, resp *http.Response) (*http.Response, error) {
	return resp, nil
}

func (m *mockPayment) CreatePaymentRequiredResponse(_ payment.Invoice) *http.Response {
	return &http.Response{StatusCode: 402}
}

type mockStrategy struct {
	name        string
	signal      *trading.Signal
	evaluateErr error
	maxPos      float64
}

func (m *mockStrategy) Name() string { return m.name }

func (m *mockStrategy) Evaluate(_ context.Context, _ trading.MarketState) (*trading.Signal, error) {
	return m.signal, m.evaluateErr
}

func (m *mockStrategy) MaxPosition() float64 { return m.maxPos }

type mockExecutor struct {
	executeErr    error
	balanceErr    error
	marketErr     error
	executeResult *trading.TradeResult
	balance       *trading.Balance
	market        *trading.MarketState
	lastTrade     trading.Trade
}

func (m *mockExecutor) Execute(_ context.Context, trade trading.Trade) (*trading.TradeResult, error) {
	m.lastTrade = trade
	if m.executeErr != nil {
		return nil, m.executeErr
	}
	if m.executeResult != nil {
		return m.executeResult, nil
	}
	return &trading.TradeResult{
		Trade:      trade,
		TxHash:     "0xmocktx",
		ExecutedAt: time.Now(),
		Profitable: true,
	}, nil
}

func (m *mockExecutor) GetBalance(_ context.Context, _ string) (*trading.Balance, error) {
	return m.balance, m.balanceErr
}

func (m *mockExecutor) GetMarketState(_ context.Context, _, _ string) (*trading.MarketState, error) {
	if m.marketErr != nil {
		return nil, m.marketErr
	}
	if m.market != nil {
		return m.market, nil
	}
	return &trading.MarketState{
		TokenIn:       "0xusdc",
		TokenOut:      "0xweth",
		Price:         1800.0,
		MovingAverage: 1750.0,
		Liquidity:     10_000_000,
		FetchedAt:     time.Now(),
	}, nil
}

type mockTransport struct {
	published [][]byte
	messages  chan []byte
	subErr    chan error
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		published: make([][]byte, 0),
		messages:  make(chan []byte, 16),
		subErr:    make(chan error, 1),
	}
}

func (m *mockTransport) Publish(_ context.Context, _ string, data []byte) error {
	m.published = append(m.published, data)
	return nil
}

func (m *mockTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return m.messages, m.subErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() Config {
	return Config{
		AgentID:           "test-defi-agent",
		HealthInterval:    time.Hour,
		TradingInterval:   time.Hour,
		PnLReportInterval: time.Hour,
		TokenIn:           "0xusdc",
		TokenOut:          "0xweth",
	}
}

func defaultMockIdentity() *mockIdentity {
	return &mockIdentity{
		registerResult: &identity.Identity{
			AgentID:   "test-defi-agent",
			AgentType: "defi",
			TxHash:    "0xmockregistration",
		},
	}
}

func testAgent(t *testing.T) (*Agent, *mockTransport) {
	t.Helper()
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		TaskTopicID:   "task-topic",
		ResultTopicID: "result-topic",
		AgentID:       "test-defi-agent",
	})

	strategy := &mockStrategy{
		name:   "mean_reversion",
		maxPos: 1.0,
		signal: &trading.Signal{
			Type:          trading.SignalBuy,
			Confidence:    0.8,
			SuggestedSize: 0.001,
			TokenIn:       "0xusdc",
			TokenOut:      "0xweth",
			GeneratedAt:   time.Now(),
		},
	}

	a := New(
		testConfig(),
		testLogger(),
		daemon.Noop(),
		defaultMockIdentity(),
		&mockPayment{},
		&mockExecutor{},
		strategy,
		trading.NewPnLTracker(),
		handler,
	)
	return a, mt
}

func TestAgent_Run_RegistersIdentity(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		TaskTopicID:   "task-topic",
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	mockID := defaultMockIdentity()
	a := New(
		testConfig(), testLogger(),
		daemon.Noop(),
		mockID, &mockPayment{},
		&mockExecutor{},
		&mockStrategy{name: "s", maxPos: 1.0, signal: &trading.Signal{Type: trading.SignalHold}},
		trading.NewPnLTracker(),
		handler,
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestAgent_Run_AlreadyRegistered(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		TaskTopicID:   "task-topic",
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	mockID := &mockIdentity{
		registerErr: identity.ErrAlreadyRegistered,
		getResult: &identity.Identity{
			AgentID:   "test-agent",
			AgentType: "defi",
			TxHash:    "0xexisting",
		},
	}

	a := New(
		testConfig(), testLogger(),
		daemon.Noop(),
		mockID, &mockPayment{},
		&mockExecutor{},
		&mockStrategy{name: "s", maxPos: 1.0, signal: &trading.Signal{Type: trading.SignalHold}},
		trading.NewPnLTracker(),
		handler,
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestProcessTrade_Success(t *testing.T) {
	a, _ := testAgent(t)

	signal := &trading.Signal{
		Type:          trading.SignalBuy,
		Confidence:    0.8,
		SuggestedSize: 0.001,
		TokenIn:       "0xusdc",
		TokenOut:      "0xweth",
		GeneratedAt:   time.Now(),
	}
	market := &trading.MarketState{
		Price:         1800.0,
		MovingAverage: 1750.0,
		FetchedAt:     time.Now(),
	}

	err := a.processTrade(context.Background(), signal, market, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.completedTrades.Load() != 1 {
		t.Errorf("expected 1 completed trade, got %d", a.completedTrades.Load())
	}
}

func TestProcessTrade_ExecuteFails(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	a := New(
		testConfig(), testLogger(),
		daemon.Noop(),
		defaultMockIdentity(), &mockPayment{},
		&mockExecutor{executeErr: errors.New("trade failed")},
		&mockStrategy{name: "s", maxPos: 1.0},
		trading.NewPnLTracker(),
		handler,
	)

	signal := &trading.Signal{
		Type:          trading.SignalBuy,
		SuggestedSize: 0.001,
		TokenIn:       "0xusdc",
		TokenOut:      "0xweth",
	}
	market := &trading.MarketState{Price: 1800.0, FetchedAt: time.Now()}

	err := a.processTrade(context.Background(), signal, market, nil)
	if err == nil {
		t.Fatal("expected error when execute fails")
	}
}

func TestProcessTrade_AppliesTaskCREConstraints(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	exec := &mockExecutor{}
	a := New(
		testConfig(), testLogger(),
		daemon.Noop(),
		defaultMockIdentity(), &mockPayment{},
		exec,
		&mockStrategy{name: "s", maxPos: 1.0},
		trading.NewPnLTracker(),
		handler,
	)

	signal := &trading.Signal{
		Type:          trading.SignalBuy,
		SuggestedSize: 0.2,
		TokenIn:       "0xusdc",
		TokenOut:      "0xweth",
	}
	market := &trading.MarketState{Price: 2000.0, FetchedAt: time.Now()}
	creDecision := &hcs.CREDecision{
		Approved:          true,
		MaxPositionUSD:    100_000000, // $100 at 6 decimals -> 0.05 units at $2000
		MaxSlippageBps:    100,        // 1%
		TTLSeconds:        300,
		DecisionTimestamp: time.Now().Unix(),
		Reason:            "approved",
	}

	if err := a.processTrade(context.Background(), signal, market, creDecision); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := exec.lastTrade.AmountIn, "0.050000"; got != want {
		t.Fatalf("AmountIn = %s, want %s", got, want)
	}
	if got, want := exec.lastTrade.MinAmountOut, "99.000000"; got != want {
		t.Fatalf("MinAmountOut = %s, want %s", got, want)
	}
}

func TestValidateCREDecision(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		task    hcs.TaskAssignment
		wantErr bool
	}{
		{
			name:    "missing decision",
			task:    hcs.TaskAssignment{TaskID: "t1"},
			wantErr: true,
		},
		{
			name: "expired decision",
			task: hcs.TaskAssignment{
				TaskID: "t2",
				CREDecision: &hcs.CREDecision{
					Approved:          true,
					MaxPositionUSD:    1,
					MaxSlippageBps:    50,
					TTLSeconds:        10,
					DecisionTimestamp: now - 100,
				},
			},
			wantErr: true,
		},
		{
			name: "valid decision",
			task: hcs.TaskAssignment{
				TaskID: "t3",
				CREDecision: &hcs.CREDecision{
					Approved:          true,
					MaxPositionUSD:    1,
					MaxSlippageBps:    50,
					TTLSeconds:        300,
					DecisionTimestamp: now,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateCREDecision(tt.task, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateCREDecision error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTradingLoop_ExecutesStrategy(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	strategy := &mockStrategy{
		name:   "mean_reversion",
		maxPos: 1.0,
		signal: &trading.Signal{
			Type:          trading.SignalBuy,
			Confidence:    0.8,
			SuggestedSize: 0.001,
			TokenIn:       "0xusdc",
			TokenOut:      "0xweth",
			GeneratedAt:   time.Now(),
		},
	}

	cfg := testConfig()
	cfg.TradingInterval = 50 * time.Millisecond

	a := New(
		cfg, testLogger(),
		daemon.Noop(),
		defaultMockIdentity(), &mockPayment{},
		&mockExecutor{},
		strategy,
		trading.NewPnLTracker(),
		handler,
	)

	ctx, cancel := context.WithCancel(context.Background())

	go a.tradingLoop(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()

	if a.completedTrades.Load() < 1 {
		t.Errorf("expected at least 1 completed trade, got %d", a.completedTrades.Load())
	}
}

func TestTradingLoop_HoldSignalSkipsTrade(t *testing.T) {
	a, _ := testAgent(t)

	a.strategy = &mockStrategy{
		name:   "mean_reversion",
		maxPos: 1.0,
		signal: &trading.Signal{
			Type:        trading.SignalHold,
			Confidence:  0.9,
			GeneratedAt: time.Now(),
		},
	}

	err := a.executeTradingCycle(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.completedTrades.Load() != 0 {
		t.Errorf("expected 0 trades for hold signal, got %d", a.completedTrades.Load())
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	a, _ := testAgent(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- a.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for graceful shutdown")
	}
}

func TestRun_ReceivesAndProcessesTask(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		TaskTopicID:   "task-topic",
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	a := New(
		testConfig(), testLogger(),
		daemon.Noop(),
		defaultMockIdentity(), &mockPayment{},
		&mockExecutor{},
		&mockStrategy{
			name:   "mean_reversion",
			maxPos: 1.0,
			signal: &trading.Signal{
				Type:        trading.SignalHold,
				Confidence:  0.9,
				GeneratedAt: time.Now(),
			},
		},
		trading.NewPnLTracker(),
		handler,
	)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		payload, _ := json.Marshal(hcs.TaskAssignment{
			TaskID:   "task-run-1",
			TaskType: "execute_trade",
		})
		env := hcs.Envelope{
			Type:    hcs.MessageTypeTaskAssignment,
			Sender:  "coordinator",
			Payload: payload,
		}
		data, _ := env.Marshal()
		mt.messages <- data
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := a.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfig_RequiredFields(t *testing.T) {
	t.Setenv("DEFI_AGENT_ID", "")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when DEFI_AGENT_ID is missing")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DEFI_AGENT_ID", "test-defi-123")
	// Clear env vars that .env may set so we actually test defaults.
	t.Setenv("DEFI_DAEMON_ADDR", "")
	t.Setenv("DEFI_BASE_RPC_URL", "")
	t.Setenv("DEFI_DEX_ROUTER", "")
	t.Setenv("DEFI_TOKEN_IN", "")
	t.Setenv("DEFI_TOKEN_OUT", "")
	t.Setenv("DEFI_MARKET_DATA_COST_WEI", "")
	t.Setenv("DEFI_MOCK_MODE", "")
	t.Setenv("DEFI_TRADING_INTERVAL", "")
	t.Setenv("DEFI_PNL_REPORT_INTERVAL", "")
	t.Setenv("DEFI_HEALTH_INTERVAL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AgentID != "test-defi-123" {
		t.Errorf("expected test-defi-123, got %s", cfg.AgentID)
	}
	if cfg.DaemonAddr != "localhost:50051" {
		t.Errorf("expected localhost:50051, got %s", cfg.DaemonAddr)
	}
	if cfg.HealthInterval != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.HealthInterval)
	}
	if cfg.TradingInterval != 60*time.Second {
		t.Errorf("expected 60s, got %v", cfg.TradingInterval)
	}
	if cfg.PnLReportInterval != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.PnLReportInterval)
	}
	if cfg.Identity.RPCURL != "https://sepolia.base.org" {
		t.Errorf("expected https://sepolia.base.org, got %s", cfg.Identity.RPCURL)
	}
	if cfg.Identity.ChainID != 84532 {
		t.Errorf("expected 84532, got %d", cfg.Identity.ChainID)
	}
}

func TestTradingCycle_CallsX402Payment(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	mockPay := &mockPayment{}
	cfg := testConfig()
	cfg.MarketDataRecipient = "0xdataProvider"
	cfg.MarketDataCostWei = "1000000000000000"

	a := New(
		cfg, testLogger(),
		daemon.Noop(),
		defaultMockIdentity(), mockPay,
		&mockExecutor{},
		&mockStrategy{
			name:   "mean_reversion",
			maxPos: 1.0,
			signal: &trading.Signal{Type: trading.SignalHold, GeneratedAt: time.Now()},
		},
		trading.NewPnLTracker(),
		handler,
	)

	err := a.executeTradingCycle(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockPay.payCalls != 1 {
		t.Errorf("expected 1 x402 Pay call, got %d", mockPay.payCalls)
	}
	if mockPay.lastReq.RecipientAddress != "0xdataProvider" {
		t.Errorf("expected recipient 0xdataProvider, got %s", mockPay.lastReq.RecipientAddress)
	}

	// Verify fee was recorded in P&L.
	report := a.pnl.Report(time.Now().Add(-time.Minute), time.Now())
	if report.TotalFees <= 0 {
		t.Error("expected x402 fee to appear in P&L report")
	}
}

func TestTradingCycle_SkipsX402WhenNotConfigured(t *testing.T) {
	a, _ := testAgent(t)

	// MarketDataRecipient is empty by default in testConfig.
	mockPay := a.payment.(*mockPayment)
	err := a.executeTradingCycle(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockPay.payCalls != 0 {
		t.Errorf("expected 0 x402 Pay calls when not configured, got %d", mockPay.payCalls)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	t.Setenv("DEFI_AGENT_ID", "custom-agent")
	t.Setenv("DEFI_DAEMON_ADDR", "custom:8080")
	t.Setenv("DEFI_HEALTH_INTERVAL", "1m")
	t.Setenv("DEFI_TRADING_INTERVAL", "30s")
	t.Setenv("DEFI_BASE_RPC_URL", "https://custom.rpc")
	t.Setenv("HCS_TASK_TOPIC", "task-topic-1")
	t.Setenv("HCS_RESULT_TOPIC", "result-topic-1")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DaemonAddr != "custom:8080" {
		t.Errorf("expected custom:8080, got %s", cfg.DaemonAddr)
	}
	if cfg.HealthInterval != time.Minute {
		t.Errorf("expected 1m, got %v", cfg.HealthInterval)
	}
	if cfg.TradingInterval != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.TradingInterval)
	}
	if cfg.Identity.RPCURL != "https://custom.rpc" {
		t.Errorf("expected https://custom.rpc, got %s", cfg.Identity.RPCURL)
	}
	if cfg.HCS.TaskTopicID != "task-topic-1" {
		t.Errorf("expected task-topic-1, got %s", cfg.HCS.TaskTopicID)
	}
}

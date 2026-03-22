package hcs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// mockTransport implements Transport for testing.
type mockTransport struct {
	publishErr error
	published  [][]byte
	messages   chan []byte
	subErr     chan error
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		published: make([][]byte, 0),
		messages:  make(chan []byte, 16),
		subErr:    make(chan error, 1),
	}
}

func (m *mockTransport) Publish(_ context.Context, _ string, data []byte) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.published = append(m.published, data)
	return nil
}

func (m *mockTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return m.messages, m.subErr
}

func TestEnvelope_RoundTrip(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"key": "value"})
	env := Envelope{
		Type:        MessageTypeTaskAssignment,
		Sender:      "coordinator",
		Recipient:   "defi-agent-1",
		TaskID:      "task-100",
		SequenceNum: 42,
		Timestamp:   time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
		Payload:     payload,
	}

	data, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Type != MessageTypeTaskAssignment {
		t.Errorf("expected task_assignment, got %s", parsed.Type)
	}
	if parsed.Sender != "coordinator" {
		t.Errorf("expected coordinator, got %s", parsed.Sender)
	}
	if parsed.SequenceNum != 42 {
		t.Errorf("expected 42, got %d", parsed.SequenceNum)
	}
	if parsed.Recipient != "defi-agent-1" {
		t.Errorf("expected defi-agent-1, got %s", parsed.Recipient)
	}
}

func TestStartSubscription_ReceivesTask(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "defi-agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = h.StartSubscription(ctx) }()

	// Send a task assignment message.
	payload, _ := json.Marshal(TaskAssignment{
		TaskID:   "task-100",
		TaskType: "execute_trade",
		Priority: 1,
	})
	env := Envelope{
		Type:    MessageTypeTaskAssignment,
		Sender:  "coordinator",
		Payload: payload,
	}
	data, _ := env.Marshal()
	mt.messages <- data

	select {
	case task := <-h.Tasks():
		if task.TaskID != "task-100" {
			t.Errorf("expected task-100, got %s", task.TaskID)
		}
		if task.TaskType != "execute_trade" {
			t.Errorf("expected execute_trade, got %s", task.TaskType)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for task")
	}
}

func TestStartSubscription_FiltersOtherRecipients(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "defi-agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = h.StartSubscription(ctx) }()

	// Message addressed to a different agent - should be skipped.
	payload, _ := json.Marshal(TaskAssignment{TaskID: "skip-me"})
	env := Envelope{
		Type:      MessageTypeTaskAssignment,
		Sender:    "coordinator",
		Recipient: "inference-agent-1",
		Payload:   payload,
	}
	data, _ := env.Marshal()
	mt.messages <- data

	// Message addressed to us - should be received.
	payload2, _ := json.Marshal(TaskAssignment{TaskID: "task-200"})
	env2 := Envelope{
		Type:      MessageTypeTaskAssignment,
		Sender:    "coordinator",
		Recipient: "defi-agent-1",
		Payload:   payload2,
	}
	data2, _ := env2.Marshal()
	mt.messages <- data2

	select {
	case task := <-h.Tasks():
		if task.TaskID != "task-200" {
			t.Errorf("expected task-200, got %s", task.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for task")
	}
}

func TestStartSubscription_ContextCancelled(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "defi-agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- h.StartSubscription(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscription to stop")
	}
}

func TestPublishPnL_Success(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	report := PnLReportMessage{
		AgentID:          "defi-agent-1",
		TotalRevenue:     1000.0,
		TotalGasCosts:    50.0,
		TotalFees:        20.0,
		NetPnL:           930.0,
		TradeCount:       10,
		WinRate:          0.7,
		IsSelfSustaining: true,
	}

	err := h.PublishPnL(context.Background(), report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mt.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mt.published))
	}

	var env Envelope
	_ = json.Unmarshal(mt.published[0], &env)
	if env.Type != MessageTypePnLReport {
		t.Errorf("expected pnl_report, got %s", env.Type)
	}
	if env.Sender != "defi-agent-1" {
		t.Errorf("expected defi-agent-1, got %s", env.Sender)
	}
}

func TestPublishResult_Success(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	result := TaskResult{
		TaskID: "task-1",
		Status: "completed",
		TxHash: "0xabc",
	}

	err := h.PublishResult(context.Background(), result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mt.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mt.published))
	}

	var env Envelope
	_ = json.Unmarshal(mt.published[0], &env)
	if env.Type != MessageTypeTaskResult {
		t.Errorf("expected task_result, got %s", env.Type)
	}
}

func TestPublishHealth_Success(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	status := HealthStatus{
		AgentID:        "defi-agent-1",
		Status:         "trading",
		ActiveStrategy: "mean_reversion",
		UptimeSeconds:  3600,
		TradeCount:     5,
		CurrentPnL:     150.0,
	}

	err := h.PublishHealth(context.Background(), status)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env Envelope
	_ = json.Unmarshal(mt.published[0], &env)
	if env.Type != MessageTypeHeartbeat {
		t.Errorf("expected heartbeat, got %s", env.Type)
	}
}

func TestPublishResult_Failed(t *testing.T) {
	mt := newMockTransport()
	mt.publishErr = errors.New("network error")

	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	err := h.PublishResult(context.Background(), TaskResult{TaskID: "t1"})
	if err == nil {
		t.Fatal("expected error for failed publish")
	}
	if !errors.Is(err, ErrPublishFailed) {
		t.Errorf("expected ErrPublishFailed, got %v", err)
	}
}

func TestPublish_SequenceIncrement(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	_ = h.PublishResult(context.Background(), TaskResult{TaskID: "t1"})
	_ = h.PublishPnL(context.Background(), PnLReportMessage{AgentID: "a"})
	_ = h.PublishHealth(context.Background(), HealthStatus{AgentID: "a"})

	if len(mt.published) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(mt.published))
	}

	seqs := make([]uint64, 3)
	for i, data := range mt.published {
		var env Envelope
		_ = json.Unmarshal(data, &env)
		seqs[i] = env.SequenceNum
	}

	if seqs[0] >= seqs[1] || seqs[1] >= seqs[2] {
		t.Errorf("sequence numbers should be monotonically increasing: %v", seqs)
	}
}

func TestPublish_ContextCancelled(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "defi-agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"PublishPnL", func() error {
			return h.PublishPnL(ctx, PnLReportMessage{})
		}},
		{"PublishResult", func() error {
			return h.PublishResult(ctx, TaskResult{})
		}},
		{"PublishHealth", func() error {
			return h.PublishHealth(ctx, HealthStatus{})
		}},
		{"PublishStrategyUpdate", func() error {
			return h.PublishStrategyUpdate(ctx, StrategyUpdate{})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error for cancelled context")
			}
		})
	}
}

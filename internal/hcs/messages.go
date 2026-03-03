// Package hcs handles Hedera Consensus Service integration for the DeFi agent.
//
// The DeFi agent uses HCS to receive task assignments from the coordinator,
// publish P&L reports, broadcast strategy updates, and send heartbeat messages.
// This package uses the same Envelope format as the inference agent and coordinator
// to ensure protocol interoperability.
package hcs

import (
	"encoding/json"
	"errors"
	"time"
)

// Sentinel errors for HCS operations.
var (
	// ErrSubscriptionFailed is returned when HCS topic subscription fails.
	ErrSubscriptionFailed = errors.New("hcs: topic subscription failed")

	// ErrPublishFailed is returned when an HCS message publish fails.
	ErrPublishFailed = errors.New("hcs: message publish failed")

	// ErrInvalidMessage is returned when a received HCS message has invalid format.
	ErrInvalidMessage = errors.New("hcs: received invalid message format")

	// ErrTopicNotFound is returned when the specified HCS topic does not exist.
	ErrTopicNotFound = errors.New("hcs: topic not found")
)

// MessageType identifies the kind of protocol message in an envelope.
// These types match the coordinator's message protocol for interoperability.
type MessageType string

const (
	MessageTypeTaskAssignment MessageType = "task_assignment"
	MessageTypeTaskResult     MessageType = "task_result"
	MessageTypeHeartbeat      MessageType = "heartbeat"
	MessageTypePnLReport      MessageType = "pnl_report"
	MessageTypeStrategyUpdate MessageType = "strategy_update"
)

// Envelope is the standard message format for all protocol messages sent
// through HCS topics. This format MUST match the coordinator's envelope
// format exactly for interoperability across all agents.
type Envelope struct {
	Type        MessageType     `json:"type"`
	Sender      string          `json:"sender"`
	Recipient   string          `json:"recipient,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	SequenceNum uint64          `json:"sequence_num"`
	Timestamp   time.Time       `json:"timestamp"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Marshal serializes the envelope to JSON bytes for publishing to HCS.
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes JSON bytes from HCS into an Envelope.
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// TaskAssignment is received from the coordinator when a new task is assigned.
type TaskAssignment struct {
	TaskID      string       `json:"task_id"`
	TaskType    string       `json:"task_type"` // e.g., "execute_trade", "update_strategy"
	Priority    int          `json:"priority"`
	CallbackURL string       `json:"callback_url,omitempty"`
	Deadline    time.Time    `json:"deadline,omitempty"`
	CREDecision *CREDecision `json:"cre_decision,omitempty"`

	// Strategy holds optional strategy configuration for strategy_update tasks.
	Strategy map[string]interface{} `json:"strategy,omitempty"`
}

// CREDecision is an approved risk decision attached to DeFi tasks.
type CREDecision struct {
	Approved          bool   `json:"approved"`
	MaxPositionUSD    uint64 `json:"max_position_usd"`
	MaxSlippageBps    uint64 `json:"max_slippage_bps"`
	TTLSeconds        uint64 `json:"ttl_seconds"`
	DecisionTimestamp int64  `json:"decision_timestamp"`
	Reason            string `json:"reason"`
}

// TaskResult is published back to the coordinator when a task completes.
type TaskResult struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"` // "completed" or "failed"
	TxHash     string `json:"tx_hash,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// PnLReportMessage is published periodically to broadcast the agent's P&L status.
type PnLReportMessage struct {
	AgentID          string    `json:"agent_id"`
	TotalRevenue     float64   `json:"total_revenue"`
	TotalGasCosts    float64   `json:"total_gas_costs"`
	TotalFees        float64   `json:"total_fees"`
	NetPnL           float64   `json:"net_pnl"`
	TradeCount       int       `json:"trade_count"`
	WinRate          float64   `json:"win_rate"`
	IsSelfSustaining bool      `json:"is_self_sustaining"`
	PeriodStart      time.Time `json:"period_start"`
	PeriodEnd        time.Time `json:"period_end"`
	ActiveStrategy   string    `json:"active_strategy"`
}

// StrategyUpdate is published when the agent changes its active trading strategy.
type StrategyUpdate struct {
	AgentID      string                 `json:"agent_id"`
	StrategyName string                 `json:"strategy_name"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
	ChangedAt    time.Time              `json:"changed_at"`
}

// HealthStatus is published periodically to signal agent liveness and state.
type HealthStatus struct {
	AgentID        string  `json:"agent_id"`
	Status         string  `json:"status"` // "idle", "trading", "error"
	ActiveStrategy string  `json:"active_strategy,omitempty"`
	CurrentPnL     float64 `json:"current_pnl"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
	TradeCount     int     `json:"trade_count"`
}

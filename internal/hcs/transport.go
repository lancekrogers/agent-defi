package hcs

import (
	"context"
	"fmt"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"
)

const (
	defaultMessageBuffer  = 100
	defaultReconnectDelay = 2 * time.Second
	defaultMaxReconnects  = 10
)

// HCSTransportConfig holds configuration for the live Hedera transport.
type HCSTransportConfig struct {
	Client         *hiero.Client
	MessageBuffer  int
	ReconnectDelay time.Duration
	MaxReconnects  int
}

// HCSTransport implements Transport using the Hiero (Hedera) SDK.
type HCSTransport struct {
	client         *hiero.Client
	messageBuffer  int
	reconnectDelay time.Duration
	maxReconnects  int
}

// NewHCSTransport creates a new HCS transport backed by a live Hedera client.
func NewHCSTransport(cfg HCSTransportConfig) *HCSTransport {
	buf := cfg.MessageBuffer
	if buf <= 0 {
		buf = defaultMessageBuffer
	}
	delay := cfg.ReconnectDelay
	if delay <= 0 {
		delay = defaultReconnectDelay
	}
	maxR := cfg.MaxReconnects
	if maxR <= 0 {
		maxR = defaultMaxReconnects
	}

	return &HCSTransport{
		client:         cfg.Client,
		messageBuffer:  buf,
		reconnectDelay: delay,
		maxReconnects:  maxR,
	}
}

// Publish sends raw bytes to an HCS topic.
func (t *HCSTransport) Publish(ctx context.Context, topicID string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hcs transport: publish to %s: %w", topicID, err)
	}

	tid, err := hiero.TopicIDFromString(topicID)
	if err != nil {
		return fmt.Errorf("hcs transport: parse topic %s: %w", topicID, err)
	}

	tx, err := hiero.NewTopicMessageSubmitTransaction().
		SetTopicID(tid).
		SetMessage(data).
		FreezeWith(t.client)
	if err != nil {
		return fmt.Errorf("hcs transport: publish to %s: freeze: %w", topicID, err)
	}

	resp, err := tx.Execute(t.client)
	if err != nil {
		return fmt.Errorf("hcs transport: publish to %s: execute: %w", topicID, err)
	}

	_, err = resp.GetReceipt(t.client)
	if err != nil {
		return fmt.Errorf("hcs transport: publish to %s: receipt: %w", topicID, err)
	}

	return nil
}

// Subscribe starts receiving messages from an HCS topic.
// Messages are delivered as raw bytes to the returned channel until ctx is cancelled.
func (t *HCSTransport) Subscribe(ctx context.Context, topicID string) (<-chan []byte, <-chan error) {
	msgCh := make(chan []byte, t.messageBuffer)
	errCh := make(chan error, t.messageBuffer)

	tid, err := hiero.TopicIDFromString(topicID)
	if err != nil {
		errCh <- fmt.Errorf("hcs transport: parse topic %s: %w", topicID, err)
		close(msgCh)
		close(errCh)
		return msgCh, errCh
	}

	go t.runSubscription(ctx, tid, topicID, msgCh, errCh)

	return msgCh, errCh
}

func (t *HCSTransport) runSubscription(
	ctx context.Context,
	tid hiero.TopicID,
	topicStr string,
	msgCh chan<- []byte,
	errCh chan<- error,
) {
	defer close(msgCh)
	defer close(errCh)

	for reconnects := 0; reconnects <= t.maxReconnects; reconnects++ {
		if ctx.Err() != nil {
			return
		}

		err := t.subscribeOnce(ctx, tid, msgCh)
		if err == nil || ctx.Err() != nil {
			return
		}

		select {
		case errCh <- fmt.Errorf("hcs transport: subscribe to %s attempt %d: %w", topicStr, reconnects+1, err):
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(t.reconnectDelay):
		}
	}

	select {
	case errCh <- fmt.Errorf("hcs transport: subscribe to %s: exhausted %d reconnect attempts", topicStr, t.maxReconnects+1):
	default:
	}
}

func (t *HCSTransport) subscribeOnce(
	ctx context.Context,
	tid hiero.TopicID,
	msgCh chan<- []byte,
) error {
	handle, err := hiero.NewTopicMessageQuery().
		SetTopicID(tid).
		SetStartTime(time.Unix(0, 0)).
		Subscribe(t.client, func(message hiero.TopicMessage) {
			data := append([]byte(nil), message.Contents...)
			select {
			case msgCh <- data:
			case <-ctx.Done():
			}
		})
	if err != nil {
		return fmt.Errorf("start subscription: %w", err)
	}

	<-ctx.Done()
	handle.Unsubscribe()
	return nil
}

// Compile-time interface compliance check.
var _ Transport = (*HCSTransport)(nil)

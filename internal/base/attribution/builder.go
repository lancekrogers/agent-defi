package attribution

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// AttributionEncoder defines operations for embedding and extracting ERC-8021
// builder attribution codes in transaction calldata.
type AttributionEncoder interface {
	// Encode appends the configured builder code to the provided calldata.
	// The builder code occupies the last 20 bytes of the resulting calldata,
	// preceded by the 4-byte AttributionMagic marker.
	// If attribution is disabled, returns the original calldata unchanged.
	Encode(ctx context.Context, txData []byte) ([]byte, error)

	// Decode extracts the ERC-8021 builder code from the last 24 bytes of
	// calldata (4 magic bytes + 20 builder code bytes).
	// Returns ErrNoAttribution if the magic marker is not present.
	// Returns ErrInvalidCalldata if calldata is too short.
	Decode(ctx context.Context, txData []byte) (*Attribution, error)
}

// encoder implements AttributionEncoder for ERC-8021 calldata manipulation.
type encoder struct {
	cfg Config
}

// NewEncoder creates an AttributionEncoder with the given builder configuration.
// Returns an error if the builder code is empty and attribution is enabled.
func NewEncoder(cfg Config) (AttributionEncoder, error) {
	if cfg.Enabled {
		var empty [20]byte
		if cfg.BuilderCode == empty {
			return nil, fmt.Errorf("attribution: builder code is required when enabled")
		}
	}
	return &encoder{cfg: cfg}, nil
}

// Encode appends the ERC-8021 builder attribution to the provided calldata.
// If attribution is disabled, returns the original calldata unchanged.
// The output format is: [original calldata] [4-byte magic] [20-byte builder code]
// This adds 24 bytes to the calldata length.
func (e *encoder) Encode(ctx context.Context, txData []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("attribution: context cancelled: %w", err)
	}

	if !e.cfg.Enabled {
		return txData, nil
	}

	if len(e.cfg.BuilderCode) != BuilderCodeLength {
		return nil, fmt.Errorf("attribution: %w", ErrInvalidBuilderCode)
	}

	// Create a new byte slice to avoid mutating the input.
	result := make([]byte, 0, len(txData)+len(AttributionMagic)+BuilderCodeLength)
	result = append(result, txData...)
	result = append(result, []byte(AttributionMagic)...)
	result = append(result, e.cfg.BuilderCode[:]...)

	return result, nil
}

// Decode extracts the ERC-8021 builder code from transaction calldata.
// It checks the last 24 bytes for the magic marker followed by the builder code.
// Returns ErrInvalidCalldata if calldata is too short.
// Returns ErrNoAttribution if the magic marker is absent.
func (e *encoder) Decode(ctx context.Context, txData []byte) (*Attribution, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("attribution: context cancelled: %w", err)
	}

	const attributionSuffixLen = len(AttributionMagic) + BuilderCodeLength // 24 bytes

	if len(txData) < attributionSuffixLen {
		return nil, fmt.Errorf("attribution: calldata length %d: %w", len(txData), ErrInvalidCalldata)
	}

	// Extract the last 24 bytes.
	suffix := txData[len(txData)-attributionSuffixLen:]
	magic := suffix[:len(AttributionMagic)]
	code := suffix[len(AttributionMagic):]

	if !bytes.Equal(magic, []byte(AttributionMagic)) {
		return nil, fmt.Errorf("attribution: magic not found: %w", ErrNoAttribution)
	}

	var builderCode [20]byte
	copy(builderCode[:], code)

	return &Attribution{
		BuilderCode: builderCode,
		Version:     1,
		Timestamp:   time.Now(),
	}, nil
}

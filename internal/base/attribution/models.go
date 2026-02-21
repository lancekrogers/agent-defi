// Package attribution implements ERC-8021 builder attribution encoding for Base transactions.
//
// ERC-8021 defines a standard for embedding builder attribution codes in EVM transaction
// calldata. The attribution code occupies the last 20 bytes of the calldata, enabling
// tooling and analytics to attribute on-chain activity to specific builders or agents.
package attribution

import (
	"errors"
	"time"
)

// Sentinel errors for attribution operations.
var (
	// ErrNoAttribution is returned when attempting to decode attribution
	// from calldata that does not contain an ERC-8021 builder code.
	ErrNoAttribution = errors.New("attribution: no ERC-8021 builder code found in calldata")

	// ErrInvalidCalldata is returned when calldata is too short to contain
	// an ERC-8021 attribution code (minimum 20 bytes required).
	ErrInvalidCalldata = errors.New("attribution: calldata too short to contain builder code")

	// ErrInvalidBuilderCode is returned when a builder code is not exactly
	// 20 bytes as required by ERC-8021.
	ErrInvalidBuilderCode = errors.New("attribution: builder code must be exactly 20 bytes")

	// ErrEncodingFailed is returned when attribution encoding fails.
	ErrEncodingFailed = errors.New("attribution: failed to encode attribution")
)

const (
	// BuilderCodeLength is the byte length of an ERC-8021 builder code.
	// Builder codes are Ethereum addresses (20 bytes).
	BuilderCodeLength = 20

	// AttributionMagic is a 4-byte prefix prepended before the builder code
	// to distinguish ERC-8021 attribution from regular calldata.
	// Value: 0x45524338 ("ERC8" in ASCII).
	AttributionMagic = "\x45\x52\x43\x38"
)

// Attribution represents a decoded builder attribution from transaction calldata.
type Attribution struct {
	// BuilderCode is the 20-byte Ethereum address of the builder/agent.
	BuilderCode [20]byte

	// BuilderName is the human-readable name of the builder (if registered).
	BuilderName string

	// Version is the attribution protocol version.
	Version uint8

	// Timestamp records when the attribution was embedded.
	Timestamp time.Time

	// TxHash is the transaction hash this attribution was embedded in.
	TxHash string
}

// Config holds configuration for the attribution encoder.
type Config struct {
	// BuilderCode is this agent's registered builder attribution code.
	// This is a fixed-length byte sequence assigned by the Base builder registry.
	BuilderCode [20]byte

	// Enabled controls whether attribution is appended to calldata.
	// Set to false to disable during testing or development.
	Enabled bool
}

// BuilderCodeHex returns the builder code as a hex string with 0x prefix.
func (a *Attribution) BuilderCodeHex() string {
	return "0x" + hexEncode(a.BuilderCode[:])
}

// hexEncode encodes bytes to lowercase hex string without 0x prefix.
func hexEncode(b []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, v := range b {
		result[i*2] = hexChars[v>>4]
		result[i*2+1] = hexChars[v&0x0f]
	}
	return string(result)
}

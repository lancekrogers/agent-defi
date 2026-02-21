// Package identity handles ERC-8004 agent identity registration on Base Sepolia.
//
// ERC-8004 defines a standard for registering AI agent identities on-chain,
// enabling other agents and contracts to verify agent provenance.
package identity

import (
	"errors"
	"time"
)

// Sentinel errors for identity operations.
var (
	// ErrAlreadyRegistered is returned when an agent attempts to register
	// an identity that already exists on-chain.
	ErrAlreadyRegistered = errors.New("identity: agent already registered on-chain")

	// ErrIdentityNotFound is returned when a requested identity does not
	// exist in the ERC-8004 registry.
	ErrIdentityNotFound = errors.New("identity: agent identity not found")

	// ErrRegistrationFailed is returned when the registration transaction
	// fails on-chain (e.g., reverts, out of gas).
	ErrRegistrationFailed = errors.New("identity: registration transaction failed")

	// ErrChainUnreachable is returned when the Base Sepolia RPC endpoint
	// cannot be reached or returns an error.
	ErrChainUnreachable = errors.New("identity: Base chain RPC unreachable")

	// ErrInsufficientFunds is returned when the agent's wallet has
	// insufficient funds for the registration transaction gas.
	ErrInsufficientFunds = errors.New("identity: insufficient funds for gas")
)

// IdentityStatus represents the on-chain registration state of an agent identity.
type IdentityStatus string

const (
	// StatusActive means the identity is registered and active.
	StatusActive IdentityStatus = "active"

	// StatusRevoked means the identity has been revoked on-chain.
	StatusRevoked IdentityStatus = "revoked"

	// StatusPending means the registration transaction is pending confirmation.
	StatusPending IdentityStatus = "pending"
)

// RegistrationRequest holds the data required to register an agent identity
// via the ERC-8004 contract on Base Sepolia (chain ID 84532).
type RegistrationRequest struct {
	// AgentID is the unique identifier for this agent instance.
	AgentID string

	// AgentType classifies the agent (e.g., "defi", "inference", "coordinator").
	AgentType string

	// PublicKey is the agent's public key for on-chain identity binding.
	PublicKey []byte

	// Metadata holds arbitrary key-value pairs stored with the identity.
	Metadata map[string]string

	// OwnerAddress is the Ethereum address that owns this identity.
	OwnerAddress string

	// AttributionCode is the ERC-8021 builder attribution code to embed.
	AttributionCode string
}

// Identity represents an on-chain registered agent identity per ERC-8004.
type Identity struct {
	// AgentID is the unique identifier for the registered agent.
	AgentID string

	// AgentType classifies the agent (e.g., "defi", "inference", "coordinator").
	AgentType string

	// ContractAddress is the ERC-8004 contract where this identity is registered.
	ContractAddress string

	// OwnerAddress is the Ethereum address that owns this identity.
	OwnerAddress string

	// PublicKey is the agent's registered public key.
	PublicKey []byte

	// Status is the current on-chain status of this identity.
	Status IdentityStatus

	// IsVerified indicates whether the identity has been verified on-chain.
	IsVerified bool

	// Metadata holds the on-chain metadata key-value pairs.
	Metadata map[string]string

	// TxHash is the transaction hash of the registration transaction.
	TxHash string

	// ChainID is the chain where the identity is registered (84532 for Base Sepolia).
	ChainID int64

	// RegisteredAt is when the identity was registered on-chain.
	RegisteredAt time.Time

	// BlockNumber is the block at which the identity was registered.
	BlockNumber uint64
}

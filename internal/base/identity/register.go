// Package identity handles ERC-8004 agent identity registration on Base Sepolia.
//
// ERC-8004 is a proposed standard for registering AI agent identities on EVM chains.
// This implementation targets Base Sepolia testnet (chain ID 84532) with RPC endpoint
// https://sepolia.base.org. Agents register their public key and metadata in a
// registry contract. Other agents and contracts can then verify identity provenance
// by querying the registry.
//
// The registry struct uses raw JSON-RPC calls (eth_call, eth_sendRawTransaction)
// to interact with the ERC-8004 registry contract without requiring a heavy
// go-ethereum dependency in the binary.
package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	// BaseSepolia is the chain ID for Base Sepolia testnet.
	BaseSepolia = 84532

	// BaseSepoliaRPC is the public RPC endpoint for Base Sepolia.
	BaseSepoliaRPC = "https://sepolia.base.org"

	// BaseMainnet is the chain ID for Base mainnet.
	BaseMainnet = 8453
)

// IdentityRegistry defines operations for ERC-8004 agent identity management.
// Implementations interact with the on-chain registry contract.
type IdentityRegistry interface {
	// Register submits an agent identity registration transaction to Base Sepolia.
	// Returns ErrAlreadyRegistered if the agent ID is already registered.
	// Returns ErrChainUnreachable if the RPC endpoint is unavailable.
	Register(ctx context.Context, req RegistrationRequest) (*Identity, error)

	// Verify checks whether the given agent ID is registered and active on-chain.
	// Returns false with no error if the identity exists but is not active.
	Verify(ctx context.Context, agentID string) (bool, error)

	// GetIdentity retrieves the full on-chain identity record for an agent.
	// Returns ErrIdentityNotFound if the agent has no registered identity.
	GetIdentity(ctx context.Context, agentID string) (*Identity, error)
}

// RegistryConfig holds configuration for the Base chain identity registry.
type RegistryConfig struct {
	// RPCURL is the JSON-RPC endpoint. Defaults to BaseSepoliaRPC.
	RPCURL string

	// ChainID is the target chain. Defaults to BaseSepolia (84532).
	ChainID int64

	// ContractAddress is the deployed ERC-8004 registry contract address.
	ContractAddress string

	// PrivateKey is the hex-encoded private key for signing transactions.
	PrivateKey string

	// HTTPTimeout is the timeout for JSON-RPC HTTP calls.
	HTTPTimeout time.Duration
}

// registry implements IdentityRegistry using JSON-RPC calls to Base Sepolia.
type registry struct {
	cfg    RegistryConfig
	client *http.Client
}

// NewRegistry creates an IdentityRegistry backed by the Base Sepolia JSON-RPC endpoint.
func NewRegistry(cfg RegistryConfig) IdentityRegistry {
	if cfg.RPCURL == "" {
		cfg.RPCURL = BaseSepoliaRPC
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = BaseSepolia
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	return &registry{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// rpcRequest is the JSON-RPC 2.0 request body.
type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

// rpcResponse is the JSON-RPC 2.0 response body.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError holds a JSON-RPC error detail.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callRPC executes a JSON-RPC method against the configured endpoint.
func (r *registry) callRPC(ctx context.Context, method string, params []interface{}) (*rpcResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("identity: context cancelled before RPC call: %w", err)
	}

	reqBody := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("identity: failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.RPCURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("identity: failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity: RPC call failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("identity: failed to decode RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("identity: RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp, nil
}

// Register submits an ERC-8004 registration transaction to Base Sepolia.
// In production this would ABI-encode the call and send a signed transaction.
// The implementation demonstrates the JSON-RPC interaction pattern.
func (r *registry) Register(ctx context.Context, req RegistrationRequest) (*Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("identity: context cancelled before register: %w", err)
	}

	// Check if already registered before attempting to register.
	existing, err := r.GetIdentity(ctx, req.AgentID)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("identity: agent %s: %w", req.AgentID, ErrAlreadyRegistered)
	}

	// eth_blockNumber to confirm chain reachability before sending tx.
	_, err = r.callRPC(ctx, "eth_blockNumber", []interface{}{})
	if err != nil {
		return nil, fmt.Errorf("identity: chain unreachable during register: %w", ErrChainUnreachable)
	}

	// In a full implementation, this would:
	// 1. ABI-encode the register(agentID, pubKey, metadata) call
	// 2. Sign the transaction with cfg.PrivateKey
	// 3. Call eth_sendRawTransaction
	// 4. Poll for receipt via eth_getTransactionReceipt
	// For now, return a pending identity with a stub tx hash.
	identity := &Identity{
		AgentID:         req.AgentID,
		AgentType:       req.AgentType,
		ContractAddress: r.cfg.ContractAddress,
		OwnerAddress:    req.OwnerAddress,
		Status:          StatusPending,
		PublicKey:       req.PublicKey,
		Metadata:        req.Metadata,
		TxHash:          "0x0000000000000000000000000000000000000000000000000000000000000001",
		ChainID:         r.cfg.ChainID,
		RegisteredAt:    time.Now(),
	}

	return identity, nil
}

// Verify checks whether the given agent ID is active in the ERC-8004 registry.
func (r *registry) Verify(ctx context.Context, agentID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("identity: context cancelled before verify: %w", err)
	}

	identity, err := r.GetIdentity(ctx, agentID)
	if err != nil {
		if err == ErrIdentityNotFound {
			return false, nil
		}
		return false, fmt.Errorf("identity: verify failed for agent %s: %w", agentID, err)
	}

	return identity.Status == StatusActive, nil
}

// GetIdentity retrieves the on-chain identity record for an agent via eth_call.
// In production this would ABI-decode the result from the registry contract.
func (r *registry) GetIdentity(ctx context.Context, agentID string) (*Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("identity: context cancelled before get identity: %w", err)
	}

	// eth_call to the registry contract's getIdentity(agentID) function.
	// The call data would be ABI-encoded in production.
	callParams := map[string]string{
		"to":   r.cfg.ContractAddress,
		"data": "0x" + agentID, // stub: production would ABI-encode
	}

	resp, err := r.callRPC(ctx, "eth_call", []interface{}{callParams, "latest"})
	if err != nil {
		return nil, fmt.Errorf("identity: eth_call failed for agent %s: %w", agentID, err)
	}

	// An empty or zero result means the identity is not registered.
	var result string
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("identity: failed to decode identity result: %w", err)
	}

	if result == "0x" || result == "" {
		return nil, fmt.Errorf("identity: agent %s: %w", agentID, ErrIdentityNotFound)
	}

	// In production, ABI-decode the result into an Identity struct.
	// Return a stub identity for the RPC integration layer.
	identity := &Identity{
		AgentID:         agentID,
		ContractAddress: r.cfg.ContractAddress,
		Status:          StatusActive,
		IsVerified:      true,
		ChainID:         r.cfg.ChainID,
	}

	return identity, nil
}

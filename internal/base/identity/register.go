// Package identity handles ERC-8004 agent identity registration on Base Sepolia.
//
// ERC-8004 is a proposed standard for registering AI agent identities on EVM chains.
// This implementation targets Base Sepolia testnet (chain ID 84532) with RPC endpoint
// https://sepolia.base.org. Agents register their public key and metadata in a
// registry contract. Other agents and contracts can then verify identity provenance
// by querying the registry.
//
// The registry struct uses go-ethereum's ABI encoder for calldata construction
// and ethutil.SignAndSend for transaction signing and submission.
package identity

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/ethutil"
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

	if r.cfg.PrivateKey == "" {
		return nil, fmt.Errorf("identity: %w: private key not configured", ErrRegistrationFailed)
	}

	// ABI-encode the register(bytes32,bytes,bytes) call using go-ethereum's ABI encoder.
	registerABI, err := abi.JSON(bytes.NewReader([]byte(`[{"name":"register","type":"function","inputs":[{"name":"agentId","type":"bytes32"},{"name":"pubKey","type":"bytes"},{"name":"metadata","type":"bytes"}]}]`)))
	if err != nil {
		return nil, fmt.Errorf("identity: parse register ABI: %w", err)
	}

	// Encode agentID as bytes32 (right-padded UTF-8).
	var agentIDPadded [32]byte
	copy(agentIDPadded[:], []byte(req.AgentID))

	// Serialize metadata to JSON bytes for the dynamic bytes param.
	metadataBytes, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal metadata: %w", err)
	}

	calldata, err := registerABI.Pack("register", agentIDPadded, req.PublicKey, metadataBytes)
	if err != nil {
		return nil, fmt.Errorf("identity: ABI-encode register call: %w", err)
	}

	// Sign and submit via go-ethereum.
	key, err := ethutil.LoadKey(r.cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("identity: load signing key: %w", err)
	}

	client, err := ethutil.DialClient(ctx, r.cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("identity: dial rpc: %w", err)
	}
	defer client.Close()

	contract := common.HexToAddress(r.cfg.ContractAddress)
	txHash, _, err := ethutil.SignAndSend(ctx, client, key, r.cfg.ChainID, contract, calldata, nil)
	if err != nil {
		return nil, fmt.Errorf("identity: register tx failed: %w", err)
	}

	identity := &Identity{
		AgentID:         req.AgentID,
		AgentType:       req.AgentType,
		ContractAddress: r.cfg.ContractAddress,
		OwnerAddress:    req.OwnerAddress,
		Status:          StatusPending,
		PublicKey:       req.PublicKey,
		Metadata:        req.Metadata,
		TxHash:          txHash.Hex(),
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
// The result bytes are ABI-decoded as (uint8 status, bytes metadata, bytes signature).
func (r *registry) GetIdentity(ctx context.Context, agentID string) (*Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("identity: context cancelled before get identity: %w", err)
	}

	// Build ABI-encoded calldata for getIdentity(bytes32).
	//
	// Function selector: keccak256("getIdentity(bytes32)")[:4] = 0xf4c714b4
	//
	// The agentID string is encoded as bytes32: the UTF-8 bytes are right-padded
	// with zero bytes to fill 32 bytes. This matches the Solidity convention for
	// string-to-bytes32 conversion (bytes32(bytes(agentID))).
	//
	// In production the selector should be verified against the deployed contract ABI.
	const getIdentitySelector = "f4c714b4"

	agentIDBytes := []byte(agentID)
	var agentIDPadded [32]byte
	copy(agentIDPadded[:], agentIDBytes) // right-pad with zero bytes

	calldata := "0x" + getIdentitySelector + hex.EncodeToString(agentIDPadded[:])

	callParams := map[string]string{
		"to":   r.cfg.ContractAddress,
		"data": calldata,
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

	return r.decodeIdentity(agentID, result)
}

// decodeIdentity ABI-decodes a hex-encoded eth_call result into an Identity.
// Expected ABI output: (uint8 status, bytes metadata, bytes signature).
// Status 0 is treated as not-registered; 1 = active; 2 = revoked.
func (r *registry) decodeIdentity(agentID, hexResult string) (*Identity, error) {
	if len(hexResult) < 4 || hexResult[:2] != "0x" {
		return nil, fmt.Errorf("identity: invalid hex result for agent %s", agentID)
	}

	data, err := hex.DecodeString(hexResult[2:])
	if err != nil {
		return nil, fmt.Errorf("identity: hex decode failed for agent %s: %w", agentID, err)
	}

	parsed, err := abi.JSON(bytes.NewReader([]byte(`[{
		"name": "getIdentity",
		"type": "function",
		"inputs": [{"name": "agentId", "type": "bytes32"}],
		"outputs": [
			{"name": "status", "type": "uint8"},
			{"name": "metadata", "type": "bytes"},
			{"name": "signature", "type": "bytes"}
		]
	}]`)))
	if err != nil {
		return nil, fmt.Errorf("identity: parse getIdentity ABI: %w", err)
	}

	outputs, err := parsed.Methods["getIdentity"].Outputs.Unpack(data)
	if err != nil {
		return nil, fmt.Errorf("identity: ABI decode failed for agent %s: %w", agentID, err)
	}

	if len(outputs) < 3 {
		return nil, fmt.Errorf("identity: unexpected output count %d for agent %s", len(outputs), agentID)
	}

	status, _ := outputs[0].(uint8)
	metadataBytes, _ := outputs[1].([]byte)
	signatureBytes, _ := outputs[2].([]byte)

	// Status 0 means the agent is not registered on-chain.
	if status == 0 {
		return nil, fmt.Errorf("identity: agent %s: %w", agentID, ErrIdentityNotFound)
	}

	identity := &Identity{
		AgentID:         agentID,
		ContractAddress: r.cfg.ContractAddress,
		Status:          statusFromUint8(status),
		IsVerified:      status == 1,
		PublicKey:        signatureBytes,
		ChainID:         r.cfg.ChainID,
	}

	// Metadata is stored as JSON-encoded bytes on-chain.
	if len(metadataBytes) > 0 {
		var meta map[string]string
		if jsonErr := json.Unmarshal(metadataBytes, &meta); jsonErr == nil {
			identity.Metadata = meta
		}
	}

	return identity, nil
}

// statusFromUint8 maps an on-chain uint8 status to IdentityStatus.
func statusFromUint8(s uint8) IdentityStatus {
	switch s {
	case 1:
		return StatusActive
	case 2:
		return StatusRevoked
	default:
		return StatusPending
	}
}


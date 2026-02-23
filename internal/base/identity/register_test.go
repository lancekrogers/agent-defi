package identity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// testRegistry creates a registry pointing to a mock HTTP server.
func testRegistry(t *testing.T, handler http.HandlerFunc) (IdentityRegistry, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	reg := NewRegistry(RegistryConfig{
		RPCURL:          srv.URL,
		ChainID:         BaseSepolia,
		ContractAddress: "0xdeadbeef",
		HTTPTimeout:     5 * time.Second,
	})
	return reg, srv
}

// rpcHandlerFunc returns an HTTP handler that serves a fixed JSON-RPC result.
func rpcHandlerFunc(result interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resultData, _ := json.Marshal(result)
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  json.RawMessage(resultData),
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// rpcSequenceHandler serves different responses for sequential calls.
type rpcSequenceHandler struct {
	responses []interface{}
	index     int
}

func (h *rpcSequenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var result interface{}
	if h.index < len(h.responses) {
		result = h.responses[h.index]
		h.index++
	}
	resultData, _ := json.Marshal(result)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  json.RawMessage(resultData),
	}
	json.NewEncoder(w).Encode(resp)
}

// abiEncodeIdentity builds a hex-encoded ABI response for getIdentity(bytes32)
// with outputs (uint8 status, bytes metadata, bytes signature).
func abiEncodeIdentity(t *testing.T, status uint8, metadata, signature []byte) string {
	t.Helper()

	uint8Ty, _ := abi.NewType("uint8", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)

	args := abi.Arguments{
		{Type: uint8Ty},
		{Type: bytesTy},
		{Type: bytesTy},
	}

	packed, err := args.Pack(status, metadata, signature)
	if err != nil {
		t.Fatalf("abiEncodeIdentity: %v", err)
	}
	return "0x" + hex.EncodeToString(packed)
}

func TestRegister_NoPrivateKey(t *testing.T) {
	// Without a private key, Register returns an error.
	seq := &rpcSequenceHandler{
		responses: []interface{}{
			"0x",        // GetIdentity eth_call: not found
			"0x1234567", // eth_blockNumber: success
		},
	}
	srv := httptest.NewServer(seq)
	defer srv.Close()

	reg := NewRegistry(RegistryConfig{
		RPCURL:          srv.URL,
		ChainID:         BaseSepolia,
		ContractAddress: "0xdeadbeef",
		HTTPTimeout:     5 * time.Second,
	})

	_, err := reg.Register(context.Background(), RegistrationRequest{
		AgentID:   "agent-defi-001",
		PublicKey: []byte("pubkey"),
		Metadata:  map[string]string{"type": "defi"},
	})

	if err == nil {
		t.Fatal("expected error when private key not configured")
	}
	if !errors.Is(err, ErrRegistrationFailed) {
		t.Errorf("expected ErrRegistrationFailed, got %v", err)
	}
}

func TestRegister_CalldataBuilt(t *testing.T) {
	// With a private key, the register flow builds calldata and attempts
	// to connect via ethclient (which will fail against the mock server).
	seq := &rpcSequenceHandler{
		responses: []interface{}{
			"0x",        // GetIdentity eth_call: not found
			"0x1234567", // eth_blockNumber: success
		},
	}
	srv := httptest.NewServer(seq)
	defer srv.Close()

	reg := NewRegistry(RegistryConfig{
		RPCURL:          srv.URL,
		ChainID:         BaseSepolia,
		ContractAddress: "0xdeadbeef",
		PrivateKey:      "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		HTTPTimeout:     5 * time.Second,
	})

	// Will fail at ethclient.DialContext, confirming calldata path runs.
	_, err := reg.Register(context.Background(), RegistrationRequest{
		AgentID:   "agent-defi-001",
		PublicKey: []byte("pubkey"),
		Metadata:  map[string]string{"type": "defi"},
	})

	// Error expected (mock doesn't support ethclient), but no panic.
	if err == nil {
		t.Fatal("expected error (mock doesn't support ethclient)")
	}
}

func TestRegister_AlreadyRegistered(t *testing.T) {
	// GetIdentity returns an ABI-encoded active identity (agent already exists).
	encoded := abiEncodeIdentity(t, 1, []byte(`{"type":"defi"}`), []byte("sig"))
	reg, _ := testRegistry(t, rpcHandlerFunc(encoded))

	_, err := reg.Register(context.Background(), RegistrationRequest{
		AgentID: "agent-already-registered",
	})

	if err == nil {
		t.Fatal("expected error for already registered agent")
	}
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("expected ErrAlreadyRegistered, got %v", err)
	}
}

func TestVerify_Success(t *testing.T) {
	// GetIdentity returns ABI-encoded active identity.
	encoded := abiEncodeIdentity(t, 1, []byte(`{}`), []byte("sig"))
	reg, _ := testRegistry(t, rpcHandlerFunc(encoded))

	active, err := reg.Verify(context.Background(), "agent-verified")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active {
		t.Error("expected identity to be active")
	}
}

func TestGetIdentity_Success(t *testing.T) {
	meta := []byte(`{"type":"defi","version":"1"}`)
	sig := []byte("agent-pubkey-data")
	encoded := abiEncodeIdentity(t, 1, meta, sig)
	reg, _ := testRegistry(t, rpcHandlerFunc(encoded))

	identity, err := reg.GetIdentity(context.Background(), "agent-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity == nil {
		t.Fatal("expected identity, got nil")
	}
	if identity.AgentID != "agent-001" {
		t.Errorf("AgentID = %q, want %q", identity.AgentID, "agent-001")
	}
	if identity.Status != StatusActive {
		t.Errorf("Status = %q, want %q", identity.Status, StatusActive)
	}
	if !identity.IsVerified {
		t.Error("expected IsVerified=true for active identity")
	}
	if identity.Metadata["type"] != "defi" {
		t.Errorf("Metadata[type] = %q, want %q", identity.Metadata["type"], "defi")
	}
	if identity.Metadata["version"] != "1" {
		t.Errorf("Metadata[version] = %q, want %q", identity.Metadata["version"], "1")
	}
	if string(identity.PublicKey) != "agent-pubkey-data" {
		t.Errorf("PublicKey = %q, want %q", identity.PublicKey, "agent-pubkey-data")
	}
}

func TestGetIdentity_NotFound(t *testing.T) {
	// eth_call returns 0x meaning no identity registered.
	reg, _ := testRegistry(t, rpcHandlerFunc("0x"))

	_, err := reg.GetIdentity(context.Background(), "agent-missing")
	if err == nil {
		t.Fatal("expected error for missing identity")
	}
	if !errors.Is(err, ErrIdentityNotFound) {
		t.Errorf("expected ErrIdentityNotFound, got %v", err)
	}
}

func TestGetIdentity_StatusZero_NotFound(t *testing.T) {
	// Status 0 means not registered, even when the contract returns data.
	encoded := abiEncodeIdentity(t, 0, []byte{}, []byte{})
	reg, _ := testRegistry(t, rpcHandlerFunc(encoded))

	_, err := reg.GetIdentity(context.Background(), "agent-unregistered")
	if err == nil {
		t.Fatal("expected error for status=0")
	}
	if !errors.Is(err, ErrIdentityNotFound) {
		t.Errorf("expected ErrIdentityNotFound, got %v", err)
	}
}

func TestGetIdentity_RevokedStatus(t *testing.T) {
	encoded := abiEncodeIdentity(t, 2, []byte(`{}`), []byte{})
	reg, _ := testRegistry(t, rpcHandlerFunc(encoded))

	identity, err := reg.GetIdentity(context.Background(), "agent-revoked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Status != StatusRevoked {
		t.Errorf("Status = %q, want %q", identity.Status, StatusRevoked)
	}
	if identity.IsVerified {
		t.Error("expected IsVerified=false for revoked identity")
	}
}

func TestContextCancelled(t *testing.T) {
	tests := []struct {
		name string
		fn   func(ctx context.Context, reg IdentityRegistry) error
	}{
		{
			name: "Register",
			fn: func(ctx context.Context, reg IdentityRegistry) error {
				_, err := reg.Register(ctx, RegistrationRequest{AgentID: "a"})
				return err
			},
		},
		{
			name: "Verify",
			fn: func(ctx context.Context, reg IdentityRegistry) error {
				_, err := reg.Verify(ctx, "a")
				return err
			},
		},
		{
			name: "GetIdentity",
			fn: func(ctx context.Context, reg IdentityRegistry) error {
				_, err := reg.GetIdentity(ctx, "a")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a non-routable address to force timeout.
			reg := NewRegistry(RegistryConfig{
				RPCURL:      "http://203.0.113.0:9999",
				HTTPTimeout: 5 * time.Second,
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			err := tt.fn(ctx, reg)
			if err == nil {
				t.Fatal("expected error for cancelled context")
			}
		})
	}
}

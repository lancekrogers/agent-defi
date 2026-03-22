package identity

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// mockRegistry implements IdentityRegistry without touching the blockchain.
// Used when DEFI_MOCK_MODE=true to allow the agent to start without an funded wallet.
type mockRegistry struct {
	mu         sync.Mutex
	identities map[string]*Identity
}

// NewMockRegistry creates an in-memory IdentityRegistry for dry-run mode.
func NewMockRegistry() IdentityRegistry {
	return &mockRegistry{
		identities: make(map[string]*Identity),
	}
}

func (m *mockRegistry) Register(_ context.Context, req RegistrationRequest) (*Identity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.identities[req.AgentID]; exists {
		return nil, fmt.Errorf("identity: agent %s: %w", req.AgentID, ErrAlreadyRegistered)
	}

	id := &Identity{
		AgentID:         req.AgentID,
		AgentType:       req.AgentType,
		ContractAddress: "0x0000000000000000000000000000000000000000",
		OwnerAddress:    req.OwnerAddress,
		Status:          StatusActive,
		IsVerified:      true,
		PublicKey:       req.PublicKey,
		Metadata:        req.Metadata,
		TxHash:          fmt.Sprintf("0xmock_%s_%d", req.AgentID, time.Now().UnixNano()),
		ChainID:         BaseSepolia,
		RegisteredAt:    time.Now(),
	}

	m.identities[req.AgentID] = id
	return id, nil
}

func (m *mockRegistry) Verify(_ context.Context, agentID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, exists := m.identities[agentID]
	if !exists {
		return false, nil
	}
	return id.Status == StatusActive, nil
}

func (m *mockRegistry) GetIdentity(_ context.Context, agentID string) (*Identity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, exists := m.identities[agentID]
	if !exists {
		return nil, fmt.Errorf("identity: agent %s: %w", agentID, ErrIdentityNotFound)
	}
	return id, nil
}

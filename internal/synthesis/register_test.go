package synthesis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegister_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/register" {
			t.Fatalf("expected /register, got %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegisterResponse{
			ParticipantID:   "test-id",
			TeamID:          "test-team",
			Name:            "OBEY Vault Agent",
			APIKey:          "sk-synth-test123",
			RegistrationTxn: "https://basescan.org/tx/0xtest",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.Register(context.Background(), RegisterRequest{
		Name:         "OBEY Vault Agent",
		Description:  "DeFi trading agent with on-chain vault custody",
		AgentHarness: "claude-code",
		Model:        "claude-sonnet-4-6",
		HumanInfo: HumanInfo{
			FullName:         "Lance Rogers",
			Email:            "lance@example.com",
			Background:       "Builder",
			CryptoExperience: "yes",
			AIExperience:     "yes",
			CodingComfort:    9,
			ProblemStatement: "Building autonomous DeFi agents with transparent on-chain vault management",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.APIKey == "" {
		t.Fatal("expected API key in response")
	}
}

func TestRegister_ContextCancellation(t *testing.T) {
	client := NewClient("http://localhost:0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Register(ctx, RegisterRequest{})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

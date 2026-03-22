package synthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type HumanInfo struct {
	FullName         string `json:"fullName"`
	Email            string `json:"email"`
	SocialHandle     string `json:"socialHandle,omitempty"`
	Background       string `json:"background"`
	CryptoExperience string `json:"cryptoExperience"`
	AIExperience     string `json:"aiAgentExperience"`
	CodingComfort    int    `json:"codingComfort"`
	ProblemStatement string `json:"problemStatement"`
}

type RegisterRequest struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	AgentHarness string    `json:"agentHarness"`
	Model        string    `json:"model"`
	Image        string    `json:"image,omitempty"`
	HumanInfo    HumanInfo `json:"humanInfo"`
}

type RegisterResponse struct {
	ParticipantID   string `json:"participantId"`
	TeamID          string `json:"teamId"`
	Name            string `json:"name"`
	APIKey          string `json:"apiKey"`
	RegistrationTxn string `json:"registrationTxn"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("synthesis: context cancelled: %w", err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("synthesis: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/register", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("synthesis: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("synthesis: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("synthesis: unexpected status %d", resp.StatusCode)
	}

	var result RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("synthesis: decode response: %w", err)
	}

	return &result, nil
}

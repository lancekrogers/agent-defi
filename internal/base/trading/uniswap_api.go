package trading

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UniswapAPIClient wraps the Uniswap Developer Platform Trading API.
type UniswapAPIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewUniswapAPIClient creates a client for the Uniswap Trading API.
func NewUniswapAPIClient(baseURL, apiKey string) *UniswapAPIClient {
	if baseURL == "" {
		baseURL = "https://trade-api.gateway.uniswap.org/v1"
	}
	return &UniswapAPIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// QuoteParams holds parameters for a quote request.
type QuoteParams struct {
	Type            string  `json:"type"`            // "EXACT_INPUT" or "EXACT_OUTPUT"
	TokenIn         string  `json:"tokenIn"`         // token address
	TokenOut        string  `json:"tokenOut"`        // token address
	TokenInChainID  string  `json:"tokenInChainId"`  // must be string per API spec
	TokenOutChainID string  `json:"tokenOutChainId"` // must be string per API spec
	Amount          string  `json:"amount"`          // amount in smallest unit
	Swapper         string  `json:"swapper"`         // wallet address
	Slippage        float64 `json:"slippageTolerance,omitempty"`
}

// QuoteResponse holds the API quote response.
type QuoteResponse struct {
	RequestID  string          `json:"requestId"`
	Routing    string          `json:"routing"` // "CLASSIC", "DUTCH_V2", etc.
	Quote      json.RawMessage `json:"quote"`
	PermitData json.RawMessage `json:"permitData"`

	// Parsed from quote for CLASSIC routing.
	ClassicQuote *ClassicQuote `json:"-"`
}

// ClassicQuote holds parsed fields from a CLASSIC route quote.
type ClassicQuote struct {
	Input       TokenAmount `json:"input"`
	Output      TokenAmount `json:"output"`
	Slippage    float64     `json:"slippage"`
	GasFee      string      `json:"gasFee"`
	GasFeeUSD   string      `json:"gasFeeUSD"`
	GasEstimate string      `json:"gasUseEstimate"`
}

// TokenAmount holds a token address and amount from the API.
type TokenAmount struct {
	Token  string `json:"token"`
	Amount string `json:"amount"`
}

// ApprovalRequest holds parameters for a check_approval request.
type ApprovalRequest struct {
	WalletAddress string `json:"walletAddress"`
	Token         string `json:"token"`
	Amount        string `json:"amount"`
	ChainID       int    `json:"chainId"`
}

// ApprovalResponse holds the API approval check response.
type ApprovalResponse struct {
	Approval *ApprovalTx `json:"approval"`
}

// ApprovalTx holds the approval transaction data. Nil if already approved.
type ApprovalTx struct {
	To      string `json:"to"`
	From    string `json:"from"`
	Data    string `json:"data"`
	Value   string `json:"value"`
	ChainID int    `json:"chainId"`
}

// SwapResponse holds the API swap response with a ready-to-sign transaction.
type SwapResponse struct {
	Swap SwapTx `json:"swap"`
}

// SwapTx holds the unsigned swap transaction.
type SwapTx struct {
	To       string `json:"to"`
	From     string `json:"from"`
	Data     string `json:"data"`
	Value    string `json:"value"`
	ChainID  int    `json:"chainId"`
	GasLimit string `json:"gasLimit"`
}

// CheckApproval checks whether a token is approved for Permit2.
func (c *UniswapAPIClient) CheckApproval(ctx context.Context, req ApprovalRequest) (*ApprovalResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("uniswap api: context cancelled: %w", err)
	}

	body, err := c.post(ctx, "/check_approval", req)
	if err != nil {
		return nil, fmt.Errorf("uniswap api: check approval: %w", err)
	}

	var resp ApprovalResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("uniswap api: decode approval response: %w", err)
	}
	return &resp, nil
}

// GetQuote gets an optimized routing quote from the Trading API.
func (c *UniswapAPIClient) GetQuote(ctx context.Context, params QuoteParams) (*QuoteResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("uniswap api: context cancelled: %w", err)
	}

	body, err := c.post(ctx, "/quote", params)
	if err != nil {
		return nil, fmt.Errorf("uniswap api: get quote: %w", err)
	}

	var resp QuoteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("uniswap api: decode quote response: %w", err)
	}

	// Parse classic quote details if routing is CLASSIC.
	if resp.Routing == "CLASSIC" && resp.Quote != nil {
		var cq ClassicQuote
		if err := json.Unmarshal(resp.Quote, &cq); err == nil {
			resp.ClassicQuote = &cq
		}
	}

	return &resp, nil
}

// GetSwap converts a quote into an unsigned transaction ready for signing.
func (c *UniswapAPIClient) GetSwap(ctx context.Context, quoteResp *QuoteResponse) (*SwapResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("uniswap api: context cancelled: %w", err)
	}

	// Build swap request by spreading the quote response fields.
	// Strip null permitData per API requirements.
	swapReq := map[string]interface{}{
		"requestId": quoteResp.RequestID,
		"routing":   quoteResp.Routing,
		"quote":     quoteResp.Quote,
	}

	// Only include permitData if it's non-null.
	if quoteResp.PermitData != nil && string(quoteResp.PermitData) != "null" {
		swapReq["permitData"] = quoteResp.PermitData
	}

	body, err := c.post(ctx, "/swap", swapReq)
	if err != nil {
		return nil, fmt.Errorf("uniswap api: get swap: %w", err)
	}

	var resp SwapResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("uniswap api: decode swap response: %w", err)
	}
	return &resp, nil
}

// post sends a POST request to the Trading API.
func (c *UniswapAPIClient) post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("x-universal-router-version", "2.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("authentication failed (HTTP %d): check UNISWAP_API_KEY", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

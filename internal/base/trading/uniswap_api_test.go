package trading

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckApproval_AlreadyApproved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/check_approval" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") == "" {
			t.Error("missing x-api-key header")
		}
		if r.Header.Get("x-universal-router-version") != "2.0" {
			t.Error("missing x-universal-router-version header")
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"approval": nil})
	}))
	defer srv.Close()

	client := NewUniswapAPIClient(srv.URL, "test-key")
	resp, err := client.CheckApproval(context.Background(), ApprovalRequest{
		WalletAddress: "0x0000000000000000000000000000000000000001",
		Token:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Amount:        "10000000",
		ChainID:       8453,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Approval != nil {
		t.Error("expected nil approval (already approved)")
	}
}

func TestCheckApproval_NeedsApproval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"approval": map[string]interface{}{
				"to":      "0xpermit2",
				"from":    "0xwallet",
				"data":    "0xapprovedata",
				"value":   "0",
				"chainId": 8453,
			},
		})
	}))
	defer srv.Close()

	client := NewUniswapAPIClient(srv.URL, "test-key")
	resp, err := client.CheckApproval(context.Background(), ApprovalRequest{
		WalletAddress: "0x0000000000000000000000000000000000000001",
		Token:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Amount:        "10000000",
		ChainID:       8453,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Approval == nil {
		t.Error("expected approval transaction")
	}
	if resp.Approval.To != "0xpermit2" {
		t.Errorf("unexpected approval.to: %s", resp.Approval.To)
	}
}

func TestGetQuote_Classic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/quote" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tokenInChainId"] != "8453" {
			t.Errorf("tokenInChainId should be string, got: %v", body["tokenInChainId"])
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"requestId":  "test-req-123",
			"routing":    "CLASSIC",
			"quote":      map[string]interface{}{"input": map[string]string{"token": "0xUSDC", "amount": "10000000"}, "output": map[string]string{"token": "0xWETH", "amount": "3100000000000000"}, "gasFeeUSD": "0.01"},
			"permitData": nil,
		})
	}))
	defer srv.Close()

	client := NewUniswapAPIClient(srv.URL, "test-key")
	resp, err := client.GetQuote(context.Background(), QuoteParams{
		Type:            "EXACT_INPUT",
		TokenIn:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		TokenOut:        "0x4200000000000000000000000000000000000006",
		TokenInChainID:  "8453",
		TokenOutChainID: "8453",
		Amount:          "10000000",
		Swapper:         "0x0000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Routing != "CLASSIC" {
		t.Errorf("expected CLASSIC routing, got: %s", resp.Routing)
	}
	if resp.RequestID != "test-req-123" {
		t.Errorf("unexpected requestId: %s", resp.RequestID)
	}
	if resp.ClassicQuote == nil {
		t.Fatal("expected parsed classic quote")
	}
	if resp.ClassicQuote.Output.Token != "0xWETH" {
		t.Errorf("unexpected output token: %s", resp.ClassicQuote.Output.Token)
	}
}

func TestGetSwap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/swap" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["permitData"]; ok {
			t.Error("null permitData should be stripped from swap request")
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"swap": map[string]interface{}{
				"to":       "0xrouter",
				"from":     "0xwallet",
				"data":     "0xswapdata",
				"value":    "0",
				"chainId":  8453,
				"gasLimit": "250000",
			},
		})
	}))
	defer srv.Close()

	client := NewUniswapAPIClient(srv.URL, "test-key")
	quoteResp := &QuoteResponse{
		RequestID:  "test-req-123",
		Routing:    "CLASSIC",
		Quote:      json.RawMessage(`{"input":{"token":"0xUSDC","amount":"10000000"}}`),
		PermitData: json.RawMessage("null"),
	}
	resp, err := client.GetSwap(context.Background(), quoteResp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Swap.To != "0xrouter" {
		t.Errorf("unexpected swap.to: %s", resp.Swap.To)
	}
	if resp.Swap.Data != "0xswapdata" {
		t.Errorf("unexpected swap.data: %s", resp.Swap.Data)
	}
}

func TestAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	client := NewUniswapAPIClient(srv.URL, "bad-key")
	_, err := client.GetQuote(context.Background(), QuoteParams{
		Type:            "EXACT_INPUT",
		TokenIn:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		TokenOut:        "0x4200000000000000000000000000000000000006",
		TokenInChainID:  "8453",
		TokenOutChainID: "8453",
		Amount:          "10000000",
		Swapper:         "0x0000000000000000000000000000000000000001",
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestContextCancellation(t *testing.T) {
	client := NewUniswapAPIClient("http://localhost:1", "test-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetQuote(ctx, QuoteParams{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

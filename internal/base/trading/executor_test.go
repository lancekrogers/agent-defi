package trading

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testExecutor creates an executor pointing to a mock HTTP server.
func testExecutor(t *testing.T, handler http.HandlerFunc) (TradeExecutor, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	exec := NewExecutor(ExecutorConfig{
		RPCURL:           srv.URL,
		ChainID:          84532,
		WalletAddress:    "0xagentaddress",
		DEXRouterAddress: "0xrouteraddress",
		HTTPTimeout:      5 * time.Second,
	})
	return exec, srv
}

// rpcResultHandler returns an HTTP handler serving a fixed JSON-RPC result.
func rpcResultHandler(result interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resultData, _ := json.Marshal(result)
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  json.RawMessage(resultData),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestExecute_NoPrivateKey(t *testing.T) {
	exec, _ := testExecutor(t, rpcResultHandler("0x1234567"))

	trade := Trade{
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		AmountIn:     "0x1000",
		MinAmountOut: "0x100",
		Deadline:     time.Now().Add(5 * time.Minute),
		Signal: Signal{
			Type:       SignalBuy,
			Confidence: 0.8,
		},
	}

	// Without a private key configured, Execute returns an error.
	_, err := exec.Execute(context.Background(), trade)
	if err == nil {
		t.Fatal("expected error when private key not configured")
	}
}

func TestExecute_CalldataBuilt(t *testing.T) {
	// Verify the executor reaches the chain reachability check and calldata building.
	// The test mock returns a valid block number, confirming the ABI encoding path runs.
	callCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": "0x1234567",
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	exec := NewExecutor(ExecutorConfig{
		RPCURL:           srv.URL,
		ChainID:          84532,
		WalletAddress:    "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD1e",
		PrivateKey:       "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		DEXRouterAddress: "0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD",
		HTTPTimeout:      5 * time.Second,
	})

	trade := Trade{
		TokenIn:      "0x4200000000000000000000000000000000000006",
		TokenOut:     "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		AmountIn:     "0x1000",
		MinAmountOut: "0x100",
		Deadline:     time.Now().Add(5 * time.Minute),
		Signal:       Signal{Type: SignalBuy, Confidence: 0.8},
	}

	// Execute will fail at the ethclient.DialContext stage (mock server doesn't
	// speak go-ethereum RPC), but it should get past calldata building and the
	// chain reachability check (callCount > 0).
	_, err := exec.Execute(context.Background(), trade)
	if err == nil {
		t.Fatal("expected error (mock doesn't support ethclient)")
	}
	if callCount == 0 {
		t.Error("expected at least one RPC call for chain reachability check")
	}
}

func TestExecute_MissingTokens(t *testing.T) {
	exec, _ := testExecutor(t, rpcResultHandler("0x1"))

	trade := Trade{
		AmountIn: "0x1000",
	}

	_, err := exec.Execute(context.Background(), trade)
	if err == nil {
		t.Fatal("expected error for missing token addresses")
	}
}

func TestExecute_ContextCancelled(t *testing.T) {
	exec := NewExecutor(ExecutorConfig{
		RPCURL:        "http://203.0.113.0:9999",
		ChainID:       84532,
		WalletAddress: "0xagent",
		HTTPTimeout:   5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	trade := Trade{
		TokenIn:  "0xusdc",
		TokenOut: "0xweth",
	}

	_, err := exec.Execute(ctx, trade)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetBalance_ETH(t *testing.T) {
	// Return a hex ETH balance.
	exec, _ := testExecutor(t, rpcResultHandler("0xde0b6b3a7640000"))

	balance, err := exec.GetBalance(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance == nil {
		t.Fatal("expected balance, got nil")
	}
	if balance.AmountWei != "0xde0b6b3a7640000" {
		t.Errorf("expected 0xde0b6b3a7640000, got %s", balance.AmountWei)
	}
	if balance.TokenAddress != "" {
		t.Errorf("expected empty token address for ETH, got %s", balance.TokenAddress)
	}
	if balance.UpdatedAt.IsZero() {
		t.Error("expected non-zero updated at")
	}
}

func TestGetBalance_ERC20(t *testing.T) {
	exec, _ := testExecutor(t, rpcResultHandler("0x1000"))

	balance, err := exec.GetBalance(context.Background(), "0xtokenaddress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance.TokenAddress != "0xtokenaddress" {
		t.Errorf("expected 0xtokenaddress, got %s", balance.TokenAddress)
	}
}

func TestGetBalance_ContextCancelled(t *testing.T) {
	exec := NewExecutor(ExecutorConfig{
		RPCURL:        "http://203.0.113.0:9999",
		WalletAddress: "0xagent",
		HTTPTimeout:   5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := exec.GetBalance(ctx, "")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetMarketState_PoolQuery(t *testing.T) {
	callNum := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callNum++
		var result string
		switch callNum {
		case 1:
			// eth_blockNumber
			result = "0x1234567"
		case 2:
			// getPool — return a 32-byte ABI-encoded address
			result = "0x000000000000000000000000" + "aabbccddee11223344556677889900aabbccddee"
		case 3:
			// slot0 — sqrtPriceX96 (fake but valid 32 bytes + extra)
			result = "0x" + "0000000000000000000000000000000000000000000000000de0b6b3a7640000" +
				"0000000000000000000000000000000000000000000000000000000000000001"
		case 4:
			// liquidity
			result = "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000"
		default:
			result = "0x0"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": result,
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	exec := NewExecutor(ExecutorConfig{
		RPCURL:        srv.URL,
		ChainID:       84532,
		WalletAddress: "0xagentaddress",
		OracleAddress: "0xfactoryaddress",
		HTTPTimeout:   5 * time.Second,
	})

	state, err := exec.GetMarketState(context.Background(), "0xusdc", "0xweth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("expected market state, got nil")
	}
	if state.Price < 0 {
		t.Error("expected non-negative price")
	}
	if state.FetchedAt.IsZero() {
		t.Error("expected non-zero fetched at")
	}
	if state.TokenIn != "0xusdc" {
		t.Errorf("expected 0xusdc, got %s", state.TokenIn)
	}
	if state.TokenOut != "0xweth" {
		t.Errorf("expected 0xweth, got %s", state.TokenOut)
	}
}

func TestGetMarketState_NoPool(t *testing.T) {
	callNum := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callNum++
		var result string
		switch callNum {
		case 1:
			result = "0x1234567" // eth_blockNumber
		case 2:
			// getPool returns short/zero — no pool
			result = "0x1234567"
		default:
			result = "0x0"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": result,
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	exec := NewExecutor(ExecutorConfig{
		RPCURL:        srv.URL,
		OracleAddress: "0xfactory",
		HTTPTimeout:   5 * time.Second,
	})

	_, err := exec.GetMarketState(context.Background(), "0xusdc", "0xweth")
	if err == nil {
		t.Fatal("expected error for missing pool")
	}
}

func TestGetMarketState_ContextCancelled(t *testing.T) {
	exec := NewExecutor(ExecutorConfig{
		RPCURL:      "http://203.0.113.0:9999",
		HTTPTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := exec.GetMarketState(ctx, "0xusdc", "0xweth")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testProtocol creates a PaymentProtocol pointing to a mock HTTP server.
func testProtocol(t *testing.T, rpcHandler http.HandlerFunc) (PaymentProtocol, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(rpcHandler)
	t.Cleanup(srv.Close)

	p := NewProtocol(ProtocolConfig{
		RPCURL:        srv.URL,
		ChainID:       84532,
		WalletAddress: "0xabcdef1234567890",
		HTTPTimeout:   5 * time.Second,
	})
	return p, srv
}

// balanceHandler returns an eth_getBalance RPC response.
func balanceHandler(balanceHex string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  balanceHex,
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestPay_NoPrivateKey(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0xde0b6b3a7640000", // 1 ETH in wei (hex)
		}
		json.NewEncoder(w).Encode(resp)
	}

	p, _ := testProtocol(t, handler)

	req := PaymentRequest{
		InvoiceID:        "inv-001",
		RecipientAddress: "0xrecipient",
		Amount:           big.NewInt(1000000000000000), // 0.001 ETH
	}

	// Without a private key configured, Pay returns an error after balance check.
	_, err := p.Pay(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when private key not configured")
	}
	if !errors.Is(err, ErrPaymentFailed) {
		t.Errorf("expected ErrPaymentFailed, got %v", err)
	}
}

func TestPay_InsufficientFunds(t *testing.T) {
	p, _ := testProtocol(t, balanceHandler("0x1")) // 1 wei

	req := PaymentRequest{
		InvoiceID:        "inv-002",
		RecipientAddress: "0xrecipient",
		Amount:           big.NewInt(1e18), // 1 ETH
	}

	_, err := p.Pay(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for insufficient funds")
	}
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("expected ErrInsufficientFunds, got %v", err)
	}
}

func TestPay_GasTooHigh(t *testing.T) {
	// Return a high gas price.
	handler := func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		method := reqBody["method"].(string)

		var result string
		switch method {
		case "eth_gasPrice":
			result = "0x174876e800" // 100 gwei
		default:
			result = "0xde0b6b3a7640000" // 1 ETH balance
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  result,
		}
		json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	p := NewProtocol(ProtocolConfig{
		RPCURL:        srv.URL,
		ChainID:       84532,
		WalletAddress: "0xabcdef1234567890",
		MaxGasPrice:   big.NewInt(1e9), // 1 gwei max
		HTTPTimeout:   5 * time.Second,
	})

	req := PaymentRequest{
		InvoiceID:        "inv-003",
		RecipientAddress: "0xrecipient",
		Amount:           big.NewInt(1000),
	}

	_, err := p.Pay(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for gas too high")
	}
	if !errors.Is(err, ErrGasTooHigh) {
		t.Errorf("expected ErrGasTooHigh, got %v", err)
	}
}

func TestPay_InvalidInvoice(t *testing.T) {
	tests := []struct {
		name string
		req  PaymentRequest
	}{
		{
			name: "missing InvoiceID",
			req:  PaymentRequest{RecipientAddress: "0xr", Amount: big.NewInt(1)},
		},
		{
			name: "missing RecipientAddress",
			req:  PaymentRequest{InvoiceID: "inv-1", Amount: big.NewInt(1)},
		},
		{
			name: "missing Amount",
			req:  PaymentRequest{InvoiceID: "inv-1", RecipientAddress: "0xr"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := testProtocol(t, balanceHandler("0xde0b6b3a7640000"))

			_, err := p.Pay(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error for invalid invoice")
			}
			if !errors.Is(err, ErrInvalidInvoice) {
				t.Errorf("expected ErrInvalidInvoice, got %v", err)
			}
		})
	}
}

func TestVerify_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"status":      "0x1",
				"blockNumber": "0x12345",
				"from":        "0xsender",
				"to":          "0xrecipient",
				"gasUsed":     "0x5208",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}

	p, _ := testProtocol(t, handler)

	receipt, err := p.VerifyPayment(context.Background(), "inv-001", "0xtxhash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected receipt, got nil")
	}
	if receipt.TxHash != "0xtxhash" {
		t.Errorf("expected 0xtxhash, got %s", receipt.TxHash)
	}
	if receipt.InvoiceID != "inv-001" {
		t.Errorf("expected inv-001, got %s", receipt.InvoiceID)
	}
}

func TestVerify_InvalidProof(t *testing.T) {
	tests := []struct {
		name      string
		invoiceID string
		txHash    string
	}{
		{name: "empty invoiceID", invoiceID: "", txHash: "0x1"},
		{name: "empty txHash", invoiceID: "inv-1", txHash: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := testProtocol(t, balanceHandler("0x1"))

			_, err := p.VerifyPayment(context.Background(), tt.invoiceID, tt.txHash)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrInvalidProof) {
				t.Errorf("expected ErrInvalidProof, got %v", err)
			}
		})
	}
}

func TestContextCancelled(t *testing.T) {
	tests := []struct {
		name string
		fn   func(ctx context.Context, p PaymentProtocol) error
	}{
		{
			name: "Pay",
			fn: func(ctx context.Context, p PaymentProtocol) error {
				_, err := p.Pay(ctx, PaymentRequest{InvoiceID: "i", RecipientAddress: "r", Amount: big.NewInt(1)})
				return err
			},
		},
		{
			name: "RequestPayment",
			fn: func(ctx context.Context, p PaymentProtocol) error {
				_, err := p.RequestPayment(ctx, big.NewInt(1), "test")
				return err
			},
		},
		{
			name: "VerifyPayment",
			fn: func(ctx context.Context, p PaymentProtocol) error {
				_, err := p.VerifyPayment(ctx, "inv-1", "0xtx")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewProtocol(ProtocolConfig{
				RPCURL:        "http://203.0.113.0:9999",
				ChainID:       84532,
				WalletAddress: "0xaddr",
				HTTPTimeout:   5 * time.Second,
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := tt.fn(ctx, p)
			if err == nil {
				t.Fatal("expected error for cancelled context")
			}
		})
	}
}

func TestRequestPayment_Success(t *testing.T) {
	p := NewProtocol(ProtocolConfig{
		RPCURL:        "http://localhost:9999",
		ChainID:       84532,
		WalletAddress: "0xmyaddress",
	})

	amount := big.NewInt(1000000000000000) // 0.001 ETH
	invoice, err := p.RequestPayment(context.Background(), amount, "test compute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoice == nil {
		t.Fatal("expected invoice, got nil")
	}
	if invoice.RecipientAddress != "0xmyaddress" {
		t.Errorf("expected 0xmyaddress, got %s", invoice.RecipientAddress)
	}
	if invoice.Amount.Cmp(amount) != 0 {
		t.Errorf("expected %s, got %s", amount.String(), invoice.Amount.String())
	}
	if invoice.Network != 84532 {
		t.Errorf("expected 84532, got %d", invoice.Network)
	}
	if invoice.ExpiresAt.Before(time.Now()) {
		t.Error("invoice should not be expired immediately")
	}
}

func TestHandlePaymentRequired_NoKey(t *testing.T) {
	// Mock RPC that returns high balance.
	rpcHandler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0xde0b6b3a7640000", // 1 ETH
		}
		json.NewEncoder(w).Encode(resp)
	}

	p, _ := testProtocol(t, rpcHandler)

	// Create a 402 response with a payment envelope.
	envelope := PaymentEnvelope{
		Version:          "1",
		Network:          "base-sepolia",
		RecipientAddress: "0xrecipient",
		Amount:           "1000000000000000",
		Token:            "ETH",
		Expiry:           time.Now().Add(5 * time.Minute).Unix(),
	}
	envData, _ := json.Marshal(envelope)

	resp := &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Body:       newReadCloser(envData),
	}

	// Without a private key, the payment fails after parsing the envelope.
	_, err := p.HandlePaymentRequired(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error when private key not configured")
	}
}

func TestCreatePaymentRequiredResponse(t *testing.T) {
	p := NewProtocol(ProtocolConfig{
		RPCURL:        "http://localhost:9999",
		ChainID:       84532,
		WalletAddress: "0xmyaddress",
	})

	invoice := Invoice{
		InvoiceID:        "inv-test",
		RecipientAddress: "0xrecipient",
		Amount:           big.NewInt(1e15),
		Token:            "ETH",
		ExpiresAt:        time.Now().Add(5 * time.Minute),
	}

	resp := p.CreatePaymentRequiredResponse(invoice)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", resp.StatusCode)
	}

	var envelope PaymentEnvelope
	json.NewDecoder(resp.Body).Decode(&envelope)
	if envelope.RecipientAddress != "0xrecipient" {
		t.Errorf("expected 0xrecipient, got %s", envelope.RecipientAddress)
	}
	if envelope.Version != "1" {
		t.Errorf("expected version 1, got %s", envelope.Version)
	}
}

// newReadCloser creates an io.ReadCloser from bytes.
func newReadCloser(data []byte) *readCloser {
	return &readCloser{data: data, pos: 0}
}

type readCloser struct {
	data []byte
	pos  int
}

func (rc *readCloser) Read(p []byte) (int, error) {
	if rc.pos >= len(rc.data) {
		return 0, io.EOF
	}
	n := copy(p, rc.data[rc.pos:])
	rc.pos += n
	return n, nil
}

func (rc *readCloser) Close() error { return nil }

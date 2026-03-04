package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"
)

// mockProtocol implements PaymentProtocol without touching the blockchain.
// Used when DEFI_MOCK_MODE=true to allow the agent to run without funded wallets.
type mockProtocol struct{}

// NewMockProtocol creates an in-memory PaymentProtocol for dry-run mode.
func NewMockProtocol() PaymentProtocol {
	return &mockProtocol{}
}

func (m *mockProtocol) Pay(_ context.Context, req PaymentRequest) (*Receipt, error) {
	if req.InvoiceID == "" || req.RecipientAddress == "" || req.Amount == nil {
		return nil, fmt.Errorf("payment: %w: missing required fields", ErrInvalidInvoice)
	}

	return &Receipt{
		ReceiptID:   fmt.Sprintf("mock-rcpt-%d", time.Now().UnixNano()),
		InvoiceID:   req.InvoiceID,
		TxHash:      fmt.Sprintf("0xmock_pay_%d", time.Now().UnixNano()),
		Amount:      req.Amount,
		Token:       req.Token,
		PaidAt:      time.Now(),
		GasCost:     big.NewInt(21000),
		ProofHeader: fmt.Sprintf("mock-base:%s:0xmock", req.InvoiceID),
	}, nil
}

func (m *mockProtocol) RequestPayment(_ context.Context, amount *big.Int, description string) (*Invoice, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("payment: %w: amount must be positive", ErrInvalidInvoice)
	}

	return &Invoice{
		InvoiceID:          fmt.Sprintf("mock-inv-%d", time.Now().UnixNano()),
		RecipientAddress:   "0x0000000000000000000000000000000000000000",
		Amount:             amount,
		Token:              "ETH",
		Network:            84532,
		ServiceDescription: description,
		ExpiresAt:          time.Now().Add(5 * time.Minute),
	}, nil
}

func (m *mockProtocol) VerifyPayment(_ context.Context, invoiceID string, txHash string) (*Receipt, error) {
	if invoiceID == "" || txHash == "" {
		return nil, fmt.Errorf("payment: %w: invoiceID and txHash are required", ErrInvalidProof)
	}

	return &Receipt{
		InvoiceID: invoiceID,
		TxHash:    txHash,
		PaidAt:    time.Now(),
		GasCost:   big.NewInt(21000),
	}, nil
}

func (m *mockProtocol) HandlePaymentRequired(_ context.Context, resp *http.Response) (*http.Response, error) {
	if resp.StatusCode != http.StatusPaymentRequired {
		return resp, nil
	}

	proofResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	proofResp.Header.Set("X-Payment-Proof", "mock-proof")
	proofResp.Header.Set("X-Payment-TxHash", "0xmock")

	return proofResp, nil
}

func (m *mockProtocol) CreatePaymentRequiredResponse(invoice Invoice) *http.Response {
	envelope := PaymentEnvelope{
		Version:          "1",
		Network:          "base-sepolia",
		RecipientAddress: invoice.RecipientAddress,
		Amount:           invoice.Amount.String(),
		Token:            invoice.Token,
		Expiry:           invoice.ExpiresAt.Unix(),
	}

	data, _ := json.Marshal(envelope)

	return &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
	}
}

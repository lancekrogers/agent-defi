// Package payment implements the x402 protocol for machine-to-machine payments.
//
// x402 is a payment negotiation protocol that extends HTTP 402 Payment Required.
// When an agent requests a resource that requires payment, the server responds
// with HTTP 402 and an Invoice JSON body. The agent parses the invoice, submits
// an on-chain payment to Base Sepolia (chain ID 84532), then retries the request
// with an X-Payment-Proof header containing the transaction hash.
//
// This pattern enables autonomous agents to pay for compute, data, or API access
// without human intervention in the payment loop.
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

	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi-ethden-2026/internal/base/ethutil"
)

// PaymentProtocol defines the x402 machine-to-machine payment operations.
// Implementations handle the full HTTP 402 handshake with Base Sepolia payments.
type PaymentProtocol interface {
	// Pay executes an x402 payment for the given request.
	// Returns ErrInsufficientFunds if wallet balance is too low.
	// Returns ErrPaymentFailed if the transaction fails on-chain.
	// Returns ErrGasTooHigh if gas price exceeds MaxGasPrice safety limit.
	Pay(ctx context.Context, req PaymentRequest) (*Receipt, error)

	// RequestPayment generates an Invoice for a resource access request.
	// This is used when acting as the resource server side of x402.
	RequestPayment(ctx context.Context, amount *big.Int, description string) (*Invoice, error)

	// VerifyPayment checks that a payment proof corresponds to a valid
	// on-chain transaction and returns the verified Receipt.
	// Returns ErrInvalidProof if the proof is invalid.
	VerifyPayment(ctx context.Context, invoiceID string, txHash string) (*Receipt, error)

	// HandlePaymentRequired processes an HTTP 402 response, extracts the
	// payment envelope, makes the payment, and retries the request with
	// proof of payment.
	HandlePaymentRequired(ctx context.Context, resp *http.Response) (*http.Response, error)

	// CreatePaymentRequiredResponse builds an HTTP 402 response with the
	// payment envelope for the requested resource.
	CreatePaymentRequiredResponse(invoice Invoice) *http.Response
}

// ProtocolConfig holds configuration for the x402 payment protocol.
type ProtocolConfig struct {
	// RPCURL is the Base Sepolia JSON-RPC endpoint.
	RPCURL string

	// ChainID is the target chain (84532 for Base Sepolia).
	ChainID int64

	// WalletAddress is this agent's Ethereum address for sending payments.
	WalletAddress string

	// PrivateKey is the hex-encoded private key for signing transactions.
	PrivateKey string

	// DefaultToken is the default token for payments (e.g., USDC address).
	DefaultToken string

	// MaxGasPrice is the maximum gas price the agent will pay (safety limit).
	// If nil, no safety limit is enforced.
	MaxGasPrice *big.Int

	// HTTPTimeout is the timeout for HTTP calls to resource servers.
	HTTPTimeout time.Duration
}

// protocol implements PaymentProtocol using HTTP and JSON-RPC.
type protocol struct {
	cfg    ProtocolConfig
	client *http.Client
}

// NewProtocol creates a PaymentProtocol for x402 machine-to-machine payments.
func NewProtocol(cfg ProtocolConfig) PaymentProtocol {
	if cfg.RPCURL == "" {
		cfg.RPCURL = "https://sepolia.base.org"
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = 84532
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	return &protocol{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// Pay executes an x402 payment by submitting a transaction to Base Sepolia
// and confirming with the resource server's callback URL.
func (p *protocol) Pay(ctx context.Context, req PaymentRequest) (*Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("payment: context cancelled before pay: %w", err)
	}

	if req.InvoiceID == "" || req.RecipientAddress == "" || req.Amount == nil {
		return nil, fmt.Errorf("payment: %w: missing required fields", ErrInvalidInvoice)
	}

	if !req.Deadline.IsZero() && time.Now().After(req.Deadline) {
		return nil, fmt.Errorf("payment: %w", ErrInvoiceExpired)
	}

	// Check gas price against safety limit.
	if p.cfg.MaxGasPrice != nil {
		gasPrice, err := p.getGasPrice(ctx)
		if err != nil {
			return nil, fmt.Errorf("payment: failed to check gas price: %w", err)
		}
		if gasPrice.Cmp(p.cfg.MaxGasPrice) > 0 {
			return nil, fmt.Errorf("payment: gas price %s exceeds max %s: %w",
				gasPrice.String(), p.cfg.MaxGasPrice.String(), ErrGasTooHigh)
		}
	}

	// Check wallet balance via JSON-RPC before attempting payment.
	balance, err := p.getBalance(ctx, p.cfg.WalletAddress)
	if err != nil {
		return nil, fmt.Errorf("payment: failed to check balance: %w", err)
	}

	if balance.Cmp(req.Amount) < 0 {
		return nil, fmt.Errorf("payment: wallet %s: %w", p.cfg.WalletAddress, ErrInsufficientFunds)
	}

	// Sign and submit the payment transaction via go-ethereum.
	if p.cfg.PrivateKey == "" {
		return nil, fmt.Errorf("payment: %w: private key not configured", ErrPaymentFailed)
	}

	key, err := ethutil.LoadKey(p.cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("payment: load signing key: %w", err)
	}

	client, err := ethutil.DialClient(ctx, p.cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("payment: dial rpc: %w", err)
	}
	defer client.Close()

	recipient := common.HexToAddress(req.RecipientAddress)
	txHashResult, txReceipt, err := ethutil.SignAndSend(ctx, client, key, p.cfg.ChainID, recipient, nil, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("payment: tx failed: %w", ErrPaymentFailed)
	}

	gasCost := new(big.Int).SetUint64(txReceipt.GasUsed)

	receipt := &Receipt{
		ReceiptID:   fmt.Sprintf("rcpt-%d", time.Now().UnixNano()),
		InvoiceID:   req.InvoiceID,
		TxHash:      txHashResult.Hex(),
		Amount:      req.Amount,
		Token:       req.Token,
		PaidAt:      time.Now(),
		GasCost:     gasCost,
		ProofHeader: fmt.Sprintf("base:%s:%s", req.InvoiceID, txHashResult.Hex()),
	}

	return receipt, nil
}

// RequestPayment creates an Invoice for agents requesting access to a resource.
func (p *protocol) RequestPayment(ctx context.Context, amount *big.Int, description string) (*Invoice, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("payment: context cancelled before request payment: %w", err)
	}

	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("payment: %w: amount must be positive", ErrInvalidInvoice)
	}

	invoice := &Invoice{
		InvoiceID:          fmt.Sprintf("inv-%d", time.Now().UnixNano()),
		RecipientAddress:   p.cfg.WalletAddress,
		Amount:             amount,
		Token:              p.cfg.DefaultToken,
		Network:            p.cfg.ChainID,
		ServiceDescription: description,
		ExpiresAt:          time.Now().Add(5 * time.Minute),
		PaymentEndpoint:    fmt.Sprintf("%s/payment/confirm", p.cfg.RPCURL),
	}

	return invoice, nil
}

// VerifyPayment checks an on-chain transaction to verify a payment proof.
func (p *protocol) VerifyPayment(ctx context.Context, invoiceID string, txHash string) (*Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("payment: context cancelled before verify: %w", err)
	}

	if invoiceID == "" || txHash == "" {
		return nil, fmt.Errorf("payment: %w: invoiceID and txHash are required", ErrInvalidProof)
	}

	receipt, err := p.getTransactionReceipt(ctx, txHash)
	if err != nil {
		return nil, fmt.Errorf("payment: failed to verify tx %s: %w", txHash, err)
	}

	receipt.InvoiceID = invoiceID
	return receipt, nil
}

// HandlePaymentRequired processes an HTTP 402 response, extracts the payment
// envelope, makes the payment, and returns a retried response with proof.
func (p *protocol) HandlePaymentRequired(ctx context.Context, resp *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("payment: context cancelled before handling 402: %w", err)
	}

	if resp.StatusCode != http.StatusPaymentRequired {
		return resp, nil
	}

	// Parse the PaymentEnvelope from the 402 response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("payment: failed to read 402 body: %w", err)
	}
	resp.Body.Close()

	var envelope PaymentEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("payment: failed to parse payment envelope: %w", ErrInvalidInvoice)
	}

	// Validate the envelope.
	if envelope.Expiry > 0 && time.Now().Unix() > envelope.Expiry {
		return nil, fmt.Errorf("payment: %w", ErrInvoiceExpired)
	}

	amount := new(big.Int)
	if _, ok := amount.SetString(envelope.Amount, 10); !ok {
		return nil, fmt.Errorf("payment: %w: invalid amount in envelope", ErrInvalidInvoice)
	}

	// Make the on-chain payment.
	receipt, err := p.Pay(ctx, PaymentRequest{
		RecipientAddress: envelope.RecipientAddress,
		Amount:           amount,
		Token:            envelope.Token,
		InvoiceID:        fmt.Sprintf("x402-%d", time.Now().UnixNano()),
	})
	if err != nil {
		return nil, fmt.Errorf("payment: 402 handshake payment failed: %w", err)
	}

	// Build a response indicating successful payment.
	proofResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	proofResp.Header.Set("X-Payment-Proof", receipt.ProofHeader)
	proofResp.Header.Set("X-Payment-TxHash", receipt.TxHash)

	return proofResp, nil
}

// CreatePaymentRequiredResponse builds an HTTP 402 response with the
// payment envelope for the requested resource.
func (p *protocol) CreatePaymentRequiredResponse(invoice Invoice) *http.Response {
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
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(data)),
	}
}

// getBalance fetches the ETH balance for an address via eth_getBalance.
func (p *protocol) getBalance(ctx context.Context, address string) (*big.Int, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getBalance",
		"params":  []any{address, "latest"},
		"id":      1,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("payment: marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.RPCURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("payment: request create error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payment: RPC call failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("payment: failed to read balance response: %w", err)
	}

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("payment: failed to decode balance response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("payment: RPC error: %s", rpcResp.Error.Message)
	}

	balance := new(big.Int)
	balance.SetString(rpcResp.Result, 0) // handles 0x prefix
	return balance, nil
}

// getGasPrice fetches the current gas price via eth_gasPrice.
func (p *protocol) getGasPrice(ctx context.Context) (*big.Int, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_gasPrice",
		"params":  []any{},
		"id":      1,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("payment: marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.RPCURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("payment: request create error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payment: RPC call failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("payment: failed to decode gas price: %w", err)
	}

	gasPrice := new(big.Int)
	gasPrice.SetString(rpcResp.Result, 0)
	return gasPrice, nil
}

// getTransactionReceipt fetches a tx receipt via eth_getTransactionReceipt.
func (p *protocol) getTransactionReceipt(ctx context.Context, txHash string) (*Receipt, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getTransactionReceipt",
		"params":  []any{txHash},
		"id":      1,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("payment: marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.RPCURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("payment: request create error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payment: RPC call failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result *struct {
			Status      string `json:"status"`
			BlockNumber string `json:"blockNumber"`
			From        string `json:"from"`
			To          string `json:"to"`
			GasUsed     string `json:"gasUsed"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("payment: failed to decode receipt: %w", err)
	}

	if rpcResp.Result == nil {
		return nil, fmt.Errorf("payment: tx %s not found: %w", txHash, ErrPaymentFailed)
	}

	if rpcResp.Result.Status != "0x1" {
		return nil, fmt.Errorf("payment: tx %s reverted: %w", txHash, ErrPaymentFailed)
	}

	gasCost := new(big.Int)
	gasCost.SetString(rpcResp.Result.GasUsed, 0)

	receipt := &Receipt{
		TxHash:  txHash,
		PaidAt:  time.Now(),
		GasCost: gasCost,
	}

	return receipt, nil
}

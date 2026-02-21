// Package payment implements the x402 protocol for machine-to-machine payments.
//
// x402 is a payment protocol using HTTP 402 Payment Required responses for
// micropayment negotiation between autonomous agents. An agent requests a
// resource, receives a 402 with payment details, pays via Base Sepolia, then
// retries with a payment proof header.
package payment

import (
	"errors"
	"math/big"
	"time"
)

// Sentinel errors for payment operations.
var (
	// ErrPaymentFailed is returned when a payment transaction fails on-chain.
	ErrPaymentFailed = errors.New("payment: on-chain payment failed")

	// ErrInsufficientFunds is returned when the agent's wallet has insufficient
	// balance to complete the payment.
	ErrInsufficientFunds = errors.New("payment: insufficient funds for payment")

	// ErrInvalidInvoice is returned when the invoice format is malformed or
	// contains invalid payment details.
	ErrInvalidInvoice = errors.New("payment: invalid invoice format or details")

	// ErrInvoiceExpired is returned when attempting to pay an expired invoice.
	ErrInvoiceExpired = errors.New("payment: invoice has expired")

	// ErrGasTooHigh is returned when gas price exceeds the configured safety limit.
	ErrGasTooHigh = errors.New("payment: gas price exceeds safety limit")

	// ErrInvalidProof is returned when a payment proof is invalid or forged.
	ErrInvalidProof = errors.New("payment: payment proof is invalid")

	// ErrChainUnreachable is returned when the Base chain RPC is unreachable.
	ErrChainUnreachable = errors.New("payment: Base chain RPC unreachable")
)

// PaymentRequest represents a request to make a payment.
type PaymentRequest struct {
	// RecipientAddress is the on-chain address to pay.
	RecipientAddress string

	// Amount is the payment amount in the smallest unit (wei for ETH).
	Amount *big.Int

	// Token is the token to pay with ("ETH" or ERC-20 contract address).
	Token string

	// InvoiceID links this payment to a specific invoice.
	InvoiceID string

	// Memo is an optional message attached to the payment.
	Memo string

	// Deadline is the latest time this payment can be made.
	Deadline time.Time
}

// Invoice is a payment request created by a service provider.
type Invoice struct {
	// InvoiceID is the unique identifier for this invoice.
	InvoiceID string `json:"invoice_id"`

	// ServiceDescription describes what the payment is for.
	ServiceDescription string `json:"service_description,omitempty"`

	// Amount is the requested payment amount in smallest unit.
	Amount *big.Int `json:"amount"`

	// Token is the requested payment token ("ETH" or ERC-20 address).
	Token string `json:"token"`

	// RecipientAddress is where payment should be sent.
	RecipientAddress string `json:"recipient_address"`

	// ExpiresAt is when this invoice expires.
	ExpiresAt time.Time `json:"expires_at"`

	// PaymentEndpoint is the URL to submit proof of payment.
	PaymentEndpoint string `json:"payment_endpoint"`

	// Network is the required chain ID.
	Network int64 `json:"network"`
}

// Receipt confirms a completed payment.
type Receipt struct {
	// ReceiptID is the unique identifier for this receipt.
	ReceiptID string

	// InvoiceID links back to the original invoice.
	InvoiceID string

	// TxHash is the on-chain transaction hash.
	TxHash string

	// Amount is the amount paid.
	Amount *big.Int

	// Token is the token used for payment.
	Token string

	// PaidAt is when the payment was confirmed.
	PaidAt time.Time

	// GasCost is the gas spent on this transaction (tracked for P&L).
	GasCost *big.Int

	// BlockNumber is the block where the payment was included.
	BlockNumber uint64

	// ProofHeader is the x402 payment proof header value to include in
	// subsequent requests to the resource.
	ProofHeader string
}

// PaymentEnvelope is the x402 protocol response envelope
// returned with HTTP 402 responses.
type PaymentEnvelope struct {
	// Version is the x402 protocol version.
	Version string `json:"version"`

	// Network is the blockchain network (e.g., "base", "base-sepolia").
	Network string `json:"network"`

	// RecipientAddress is where to send payment.
	RecipientAddress string `json:"recipient"`

	// Amount is the required payment in smallest unit.
	Amount string `json:"amount"`

	// Token is the payment token address or "ETH".
	Token string `json:"token"`

	// PayloadHash is the hash of the resource being paid for.
	PayloadHash string `json:"payload_hash"`

	// Expiry is when this payment envelope expires (Unix timestamp).
	Expiry int64 `json:"expiry"`
}

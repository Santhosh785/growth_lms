// Package payments wraps the Razorpay payment provider that Task 6's
// commerce handlers, webhook handler, and worker jobs consume. It is
// exposed as a small interface, mirroring internal/media/bunny.go's
// pattern, so handlers can be unit-tested against a fake instead of
// requiring live credentials. The real implementation is best-effort
// against Razorpay's documented REST API shapes and has not been
// exercised against a live account in this session — treat it as a
// starting point to validate against a real sandbox before relying on it
// in production.
package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"growth-lms/internal/config"
)

// Provider is everything the commerce handlers, webhook handler, and
// worker jobs need from Razorpay.
type Provider interface {
	// CreateOrder creates a Razorpay order for amountMinorUnits (already
	// computed server-side, in the smallest currency unit — paise for
	// INR, cents for USD; Razorpay's Orders API always takes an integer
	// minor-unit amount, never a decimal major-unit amount) in the given
	// currency ("INR" or "USD"), tagged with receipt as the caller's own
	// order/reference ID. Returns Razorpay's order ID (e.g. "order_xxx").
	CreateOrder(ctx context.Context, amountMinorUnits int64, currency string, receipt string) (orderID string, err error)

	// VerifyWebhookSignature HMAC-SHA256-verifies payload (the raw,
	// unparsed webhook request body) against signatureHeader (the raw
	// value of the X-Razorpay-Signature header) using the configured
	// webhook secret, constant-time. Only a call site that has already
	// gotten true from this may treat the webhook event as authentic.
	VerifyWebhookSignature(payload []byte, signatureHeader string) bool

	// VerifyPaymentSignature HMAC-SHA256-verifies Razorpay checkout.js's
	// client-side success-callback signature: HMAC-SHA256 of
	// "razorpayOrderID|razorpayPaymentID" using the key secret, hex
	// digest, constant-time compared against razorpaySignature.
	//
	// SECURITY CONSTRAINT — READ BEFORE CALLING THIS FROM A HANDLER:
	// a true result here proves the browser's checkout.js callback data
	// is authentic, but it must NEVER be used to mark an order/payment
	// succeeded or to grant a learner entitlement. Per this repo's
	// CLAUDE.md, entitlements/access are granted only from a verified
	// asynchronous webhook event (processed via VerifyWebhookSignature
	// above, on the server-to-server webhook POST), never from a
	// browser-driven return/callback — even one whose signature checks
	// out, since a browser round-trip can be interrupted, replayed, or
	// simply never happen (tab closed mid-checkout) while the webhook
	// still fires independently. This method exists solely so a
	// checkout-callback HTTP handler (Task 6's commerce-handlers, not
	// this task) can optimistically render a "verifying your payment..."
	// UI state while it polls/waits for the real webhook-driven order
	// status to flip. Do not wire this method's return value into any
	// code path that updates order status, entitlement status, or
	// access_status.
	VerifyPaymentSignature(razorpayOrderID, razorpayPaymentID, razorpaySignature string) bool

	// CreateRefund calls Razorpay's Refunds API for razorpayPaymentID,
	// refunding amountMinorUnits (same minor-unit convention as
	// CreateOrder; pass the full original amount for a full refund).
	// Returns Razorpay's refund ID (e.g. "rfnd_xxx").
	CreateRefund(ctx context.Context, razorpayPaymentID string, amountMinorUnits int64) (refundID string, err error)
}

// RazorpayProvider talks to Razorpay's REST API directly over HTTP. The
// key secret and webhook secret never leave the server.
type RazorpayProvider struct {
	keyID         string
	keySecret     string
	webhookSecret string
	http          *http.Client
}

func NewRazorpayProvider(cfg config.RazorpayConfig) *RazorpayProvider {
	return &RazorpayProvider{
		keyID:         cfg.KeyID,
		keySecret:     cfg.KeySecret,
		webhookSecret: cfg.WebhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

var _ Provider = (*RazorpayProvider)(nil)

const razorpayAPIBase = "https://api.razorpay.com/v1"

// CreateOrder calls Razorpay's Orders API to create an order for
// amountMinorUnits in currency, tagged with receipt as the caller's own
// order/reference ID.
func (p *RazorpayProvider) CreateOrder(ctx context.Context, amountMinorUnits int64, currency string, receipt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"amount":   amountMinorUnits,
		"currency": currency,
		"receipt":  receipt,
	})
	if err != nil {
		return "", fmt.Errorf("payments: marshal create-order request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, razorpayAPIBase+"/orders", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("payments: build create-order request: %w", err)
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("payments: create razorpay order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("payments: razorpay create-order returned status %d", resp.StatusCode)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("payments: decode create-order response: %w", err)
	}
	return out.ID, nil
}

// VerifyWebhookSignature HMAC-SHA256-verifies payload against
// signatureHeader using the configured webhook secret, constant-time.
func (p *RazorpayProvider) VerifyWebhookSignature(payload []byte, signatureHeader string) bool {
	if p.webhookSecret == "" || signatureHeader == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signatureHeader)) == 1
}

// VerifyPaymentSignature HMAC-SHA256-verifies Razorpay checkout.js's
// client-side success-callback signature.
//
// SECURITY CONSTRAINT — READ BEFORE CALLING THIS FROM A HANDLER: a true
// result here proves the browser's checkout.js callback data is
// authentic, but it must NEVER be used to mark an order/payment
// succeeded or to grant a learner entitlement. Per this repo's
// CLAUDE.md, entitlements/access are granted only from a verified
// asynchronous webhook event (processed via VerifyWebhookSignature
// above, on the server-to-server webhook POST), never from a
// browser-driven return/callback — even one whose signature checks out,
// since a browser round-trip can be interrupted, replayed, or simply
// never happen (tab closed mid-checkout) while the webhook still fires
// independently. This method exists solely so a checkout-callback HTTP
// handler (Task 6's commerce-handlers, not this task) can optimistically
// render a "verifying your payment..." UI state while it polls/waits for
// the real webhook-driven order status to flip. Do not wire this
// method's return value into any code path that updates order status,
// entitlement status, or access_status.
func (p *RazorpayProvider) VerifyPaymentSignature(razorpayOrderID, razorpayPaymentID, razorpaySignature string) bool {
	if p.keySecret == "" || razorpayOrderID == "" || razorpayPaymentID == "" || razorpaySignature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(p.keySecret))
	mac.Write([]byte(razorpayOrderID + "|" + razorpayPaymentID))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(razorpaySignature)) == 1
}

// CreateRefund calls Razorpay's Refunds API for razorpayPaymentID,
// refunding amountMinorUnits.
func (p *RazorpayProvider) CreateRefund(ctx context.Context, razorpayPaymentID string, amountMinorUnits int64) (string, error) {
	body, err := json.Marshal(map[string]any{
		"amount": amountMinorUnits,
	})
	if err != nil {
		return "", fmt.Errorf("payments: marshal create-refund request: %w", err)
	}

	url := fmt.Sprintf("%s/payments/%s/refund", razorpayAPIBase, razorpayPaymentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("payments: build create-refund request: %w", err)
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("payments: create razorpay refund: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("payments: razorpay create-refund returned status %d", resp.StatusCode)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("payments: decode create-refund response: %w", err)
	}
	return out.ID, nil
}

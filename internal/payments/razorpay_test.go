package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"growth-lms/internal/config"
)

func TestVerifyWebhookSignature(t *testing.T) {
	provider := NewRazorpayProvider(config.RazorpayConfig{
		KeyID:         "key_id",
		KeySecret:     "key_secret",
		WebhookSecret: "webhook_secret",
	})

	payload := []byte(`{"event":"payment.captured"}`)
	validSig := computeHMACHex(t, "webhook_secret", payload)

	tests := []struct {
		name    string
		payload []byte
		sig     string
		want    bool
	}{
		{"valid signature", payload, validSig, true},
		{"tampered payload", []byte(`{"event":"payment.failed"}`), validSig, false},
		{"wrong secret", payload, computeHMACHex(t, "other_secret", payload), false},
		{"empty header", payload, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.VerifyWebhookSignature(tt.payload, tt.sig)
			if got != tt.want {
				t.Errorf("VerifyWebhookSignature() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("empty webhook secret", func(t *testing.T) {
		p := NewRazorpayProvider(config.RazorpayConfig{KeyID: "key_id", KeySecret: "key_secret"})
		if p.VerifyWebhookSignature(payload, validSig) {
			t.Error("expected false when webhook secret is empty")
		}
	})
}

func TestVerifyPaymentSignature(t *testing.T) {
	provider := NewRazorpayProvider(config.RazorpayConfig{
		KeyID:         "key_id",
		KeySecret:     "key_secret",
		WebhookSecret: "webhook_secret",
	})

	orderID := "order_abc123"
	paymentID := "pay_xyz789"
	validSig := computeHMACHex(t, "key_secret", []byte(orderID+"|"+paymentID))

	tests := []struct {
		name      string
		orderID   string
		paymentID string
		sig       string
		want      bool
	}{
		{"valid signature", orderID, paymentID, validSig, true},
		{"tampered payment id", orderID, "pay_other", validSig, false},
		{"wrong secret", orderID, paymentID, computeHMACHex(t, "other_secret", []byte(orderID+"|"+paymentID)), false},
		{"empty signature", orderID, paymentID, "", false},
		{"empty order id", "", paymentID, validSig, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.VerifyPaymentSignature(tt.orderID, tt.paymentID, tt.sig)
			if got != tt.want {
				t.Errorf("VerifyPaymentSignature() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("empty key secret", func(t *testing.T) {
		p := NewRazorpayProvider(config.RazorpayConfig{KeyID: "key_id", WebhookSecret: "webhook_secret"})
		if p.VerifyPaymentSignature(orderID, paymentID, validSig) {
			t.Error("expected false when key secret is empty")
		}
	})
}

func computeHMACHex(t *testing.T, secret string, payload []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

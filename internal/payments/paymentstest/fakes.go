// Package paymentstest provides an in-memory fake of payments.Provider
// for handler/worker tests that need predictable order/refund/signature
// behavior without live Razorpay credentials.
package paymentstest

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"growth-lms/internal/payments"
)

var _ payments.Provider = (*FakeProvider)(nil)

// FakeProvider is a deterministic, in-memory payments.Provider.
type FakeProvider struct {
	OrderIDToReturn  string
	RefundIDToReturn string
	CreateOrderErr   error
	CreateRefundErr  error

	// WebhookSecret and KeySecret gate VerifyWebhookSignature and
	// VerifyPaymentSignature respectively: a non-empty secret plus the
	// "valid-signature" convention (mirroring
	// mediatest.FakeBunnyClient.VerifyWebhookSignature) simulates a
	// correctly-signed payload/callback.
	WebhookSecret string
	KeySecret     string
}

func (f *FakeProvider) CreateOrder(ctx context.Context, amountMinorUnits int64, currency string, receipt string) (string, error) {
	if f.CreateOrderErr != nil {
		return "", f.CreateOrderErr
	}
	if f.OrderIDToReturn != "" {
		return f.OrderIDToReturn, nil
	}
	return "order-" + uuid.NewString(), nil
}

func (f *FakeProvider) VerifyWebhookSignature(payload []byte, signatureHeader string) bool {
	return f.WebhookSecret != "" && signatureHeader == "valid-signature"
}

func (f *FakeProvider) VerifyPaymentSignature(razorpayOrderID, razorpayPaymentID, razorpaySignature string) bool {
	return f.KeySecret != "" && razorpayOrderID != "" && razorpayPaymentID != "" && razorpaySignature == "valid-signature"
}

func (f *FakeProvider) CreateRefund(ctx context.Context, razorpayPaymentID string, amountMinorUnits int64) (string, error) {
	if f.CreateRefundErr != nil {
		return "", f.CreateRefundErr
	}
	if f.RefundIDToReturn != "" {
		return f.RefundIDToReturn, nil
	}
	return "rfnd-" + uuid.NewString(), nil
}

// String is a small debug helper, unused by tests but handy when
// inspecting a fake in a failure message.
func (f *FakeProvider) String() string {
	return fmt.Sprintf("FakeProvider{OrderID:%q, RefundID:%q}", f.OrderIDToReturn, f.RefundIDToReturn)
}

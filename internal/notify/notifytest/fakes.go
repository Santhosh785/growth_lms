// Package notifytest provides an in-memory fake of notify.EmailClient for
// worker/handler tests that need to assert on send/no-send behavior (e.g.
// notification_opt_out) without live Resend credentials.
package notifytest

import (
	"context"
	"sync"

	"growth-lms/internal/notify"
)

var _ notify.EmailClient = (*FakeEmailClient)(nil)

// SentEmail records one call to SendEmail.
type SentEmail struct {
	To      string
	Subject string
	Body    string
}

// FakeEmailClient is a deterministic, in-memory notify.EmailClient. Safe
// for concurrent use since asynq handlers may run on multiple worker
// goroutines.
type FakeEmailClient struct {
	mu   sync.Mutex
	Sent []SentEmail
}

func (f *FakeEmailClient) SendEmail(ctx context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, SentEmail{To: to, Subject: subject, Body: body})
	return nil
}

// Count returns how many emails have been sent so far.
func (f *FakeEmailClient) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Sent)
}

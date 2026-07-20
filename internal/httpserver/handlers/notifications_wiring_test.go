package handlers

import (
	"os"
	"strings"
	"testing"
)

// TestRequestPathHandlers_NeverCallSendEmailSynchronously is the
// code-level assertion the Stage 7 spec calls for in place of a
// timing-based test (which would be flaky): a request completing grading
// or certificate issuance must never itself call the Resend client — only
// worker.EnqueueX functions, which hand the actual send off to the async
// worker process. A runtime assertion of "this HTTP handler never
// transitively calls notify.EmailClient.SendEmail" isn't practical to
// express as a unit test without a live Redis/asynq server standing
// between the handler and the worker, so this test instead greps the two
// hook-point source files for the two signals that matter: the presence
// of an Enqueue call, and the absence of any direct SendEmail/Resend
// call.
func TestRequestPathHandlers_NeverCallSendEmailSynchronously(t *testing.T) {
	files := []struct {
		path          string
		wantEnqueue   string
		wantNoNetwork []string
	}{
		{
			path:          "assignment.go",
			wantEnqueue:   "worker.EnqueueAssignmentGradedNotification(",
			wantNoNetwork: []string{"SendEmail(", "notify.NewResendClient(", "resend.com"},
		},
		{
			path:          "completion.go",
			wantEnqueue:   "worker.EnqueueCertificateIssuedNotification(",
			wantNoNetwork: []string{"SendEmail(", "notify.NewResendClient(", "resend.com"},
		},
		{
			path:          "course_announcement.go",
			wantEnqueue:   "worker.EnqueueCourseAnnouncementNotification(",
			wantNoNetwork: []string{"SendEmail(", "notify.NewResendClient(", "resend.com"},
		},
	}

	for _, f := range files {
		t.Run(f.path, func(t *testing.T) {
			data, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("read %s: %v", f.path, err)
			}
			src := string(data)

			if !strings.Contains(src, f.wantEnqueue) {
				t.Errorf("%s: expected to find enqueue-only call %q — the notification hook must be wired", f.path, f.wantEnqueue)
			}
			for _, forbidden := range f.wantNoNetwork {
				if strings.Contains(src, forbidden) {
					t.Errorf("%s: found forbidden synchronous-network signal %q — notifications must only ever be enqueued from the request path, never sent directly", f.path, forbidden)
				}
			}
		})
	}
}

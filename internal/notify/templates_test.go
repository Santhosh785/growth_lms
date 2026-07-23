package notify

import (
	"strings"
	"testing"
)

func TestRenderMentionEmail(t *testing.T) {
	subject, body := RenderMentionEmail("Alice", "Welcome thread", "hi there", "http://x/thread", "http://x/unsub/tok")
	if !strings.Contains(subject, "Alice") || !strings.Contains(subject, "Welcome thread") {
		t.Fatalf("subject missing actor/thread: %q", subject)
	}
	for _, want := range []string{"hi there", "http://x/thread", "http://x/unsub/tok", "Unsubscribe"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestRenderBroadcastEmail_EscapesAndOmitsEmptyUnsub(t *testing.T) {
	_, body := RenderBroadcastEmail("Title <b>", "Body & more", "", "")
	if strings.Contains(body, "<b>") {
		t.Fatalf("title HTML not escaped: %s", body)
	}
	if strings.Contains(body, "Unsubscribe") {
		t.Fatalf("empty unsubscribe URL should omit the footer link: %s", body)
	}
}

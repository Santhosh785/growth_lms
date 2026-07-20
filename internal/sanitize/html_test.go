package sanitize_test

import (
	"strings"
	"testing"

	"growth-lms/internal/sanitize"
)

func TestTextBlockHTML_StripsScript(t *testing.T) {
	out := sanitize.TextBlockHTML(`<p>hi</p><script>alert(1)</script>`)
	if strings.Contains(out, "script") || strings.Contains(out, "alert") {
		t.Errorf("expected script tag stripped, got %q", out)
	}
	if !strings.Contains(out, "<p>hi</p>") {
		t.Errorf("expected allowed <p> tag preserved, got %q", out)
	}
}

func TestTextBlockHTML_StripsEventHandlers(t *testing.T) {
	out := sanitize.TextBlockHTML(`<p onclick="evil()">hi</p>`)
	if strings.Contains(out, "onclick") {
		t.Errorf("expected onclick attribute stripped, got %q", out)
	}
}

func TestTextBlockHTML_AllowsAnchorHrefOnly(t *testing.T) {
	out := sanitize.TextBlockHTML(`<a href="https://example.com" target="_blank" onclick="evil()">link</a>`)
	if strings.Contains(out, "target") || strings.Contains(out, "onclick") {
		t.Errorf("expected target/onclick stripped from anchor, got %q", out)
	}
	if !strings.Contains(out, `href="https://example.com"`) {
		t.Errorf("expected href preserved, got %q", out)
	}
}

func TestTextBlockHTML_StripsDisallowedElements(t *testing.T) {
	out := sanitize.TextBlockHTML(`<div><table><tr><td>x</td></tr></table></div>`)
	if strings.Contains(out, "<div") || strings.Contains(out, "<table") {
		t.Errorf("expected div/table stripped, got %q", out)
	}
}

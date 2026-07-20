// Package sanitize provides the single HTML-sanitization policy text
// blocks are allowed to use, per Task 4's spec: an explicit allowlist, not
// a regex-based blocklist, applied on every create/update of a text
// block's content.
package sanitize

import "github.com/microcosm-cc/bluemonday"

var textBlockPolicy = buildTextBlockPolicy()

func buildTextBlockPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "strong", "em", "u", "ul", "ol", "li", "br", "h1", "h2", "h3", "a")
	p.AllowAttrs("href").OnElements("a")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("mailto", "http", "https")
	// Deliberately not RequireNoFollowOnLinks: the spec's allowlist is
	// exactly {p, strong, em, u, ul, ol, li, br, a[href], h1-h3} — adding
	// rel="nofollow" would inject an attribute the spec never asked for.
	return p
}

// TextBlockHTML sanitizes raw HTML for a "text" block using the exact
// allowlist the spec requires: p, strong, em, u, ul, ol, li, br,
// a (href only, no target/event attributes), h1-h3. Everything else —
// including all script/style/event-handler content — is stripped.
func TextBlockHTML(raw string) string {
	return textBlockPolicy.Sanitize(raw)
}

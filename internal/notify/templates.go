// Email body templates for Task 7 community notifications. These are the
// typed render helpers the plan calls for: each returns (subject, htmlBody)
// with a standard footer carrying the one-click unsubscribe link. The
// EmailClient interface (SendEmail) is unchanged — this is the provider-
// independent presentation layer on top of it.
package notify

import (
	"fmt"
	"html"
)

// emailLayout wraps a body fragment in a minimal HTML shell and appends the
// unsubscribe footer. unsubscribeURL is per-recipient and may be empty (e.g.
// in tests), in which case the footer link is omitted.
func emailLayout(bodyHTML, unsubscribeURL string) string {
	footer := ""
	if unsubscribeURL != "" {
		footer = fmt.Sprintf(
			`<hr style="margin-top:24px;border:none;border-top:1px solid #eee">`+
				`<p style="color:#888;font-size:12px">`+
				`You are receiving this because you are a member of this organization. `+
				`<a href="%s">Unsubscribe from these emails</a>.</p>`,
			html.EscapeString(unsubscribeURL))
	}
	return `<div style="font-family:system-ui,sans-serif;max-width:560px;margin:0 auto">` + bodyHTML + footer + `</div>`
}

// RenderMentionEmail is sent when someone @-mentions the recipient in a post.
func RenderMentionEmail(actorName, threadTitle, preview, link, unsubscribeURL string) (subject, body string) {
	subject = fmt.Sprintf("%s mentioned you in \"%s\"", actorName, threadTitle)
	inner := fmt.Sprintf(
		`<p><strong>%s</strong> mentioned you in <strong>%s</strong>:</p>`+
			`<blockquote style="color:#444;border-left:3px solid #ddd;padding-left:12px">%s</blockquote>`+
			`<p><a href="%s">View the discussion</a></p>`,
		html.EscapeString(actorName), html.EscapeString(threadTitle),
		html.EscapeString(preview), html.EscapeString(link))
	return subject, emailLayout(inner, unsubscribeURL)
}

// RenderReplyEmail is sent to a post's author when someone replies to it.
func RenderReplyEmail(actorName, threadTitle, preview, link, unsubscribeURL string) (subject, body string) {
	subject = fmt.Sprintf("%s replied in \"%s\"", actorName, threadTitle)
	inner := fmt.Sprintf(
		`<p><strong>%s</strong> replied in <strong>%s</strong>:</p>`+
			`<blockquote style="color:#444;border-left:3px solid #ddd;padding-left:12px">%s</blockquote>`+
			`<p><a href="%s">View the discussion</a></p>`,
		html.EscapeString(actorName), html.EscapeString(threadTitle),
		html.EscapeString(preview), html.EscapeString(link))
	return subject, emailLayout(inner, unsubscribeURL)
}

// RenderBroadcastEmail is sent for an owner/teacher announcement to the org.
func RenderBroadcastEmail(title, msgBody, link, unsubscribeURL string) (subject, body string) {
	subject = title
	inner := fmt.Sprintf(
		`<h2 style="font-size:18px">%s</h2><p style="color:#444">%s</p>`,
		html.EscapeString(title), html.EscapeString(msgBody))
	if link != "" {
		inner += fmt.Sprintf(`<p><a href="%s">Open in Growth LMS</a></p>`, html.EscapeString(link))
	}
	return subject, emailLayout(inner, unsubscribeURL)
}

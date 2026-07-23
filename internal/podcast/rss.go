// Package podcast holds the database-free RSS 2.0 (+ iTunes namespace) feed
// renderer for Task 9's Podcasts module. Like internal/ai's service layer,
// it never touches Postgres: the handler layer resolves the published show
// and episodes (through SECURITY DEFINER functions) and hands plain structs
// here, so the feed shape is unit-testable without a database and the
// XML-escaping/date-format rules live in exactly one place.
package podcast

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// Feed is the fully-resolved input to Render: channel-level show metadata
// plus every published episode, newest first.
type Feed struct {
	Title       string
	Link        string // the show's public HTML page
	FeedURL     string // this RSS document's own URL (atom:link rel=self)
	Description string
	Author      string
	Language    string
	Category    string
	ImageURL    string
	Episodes    []FeedItem
}

// FeedItem is one <item>: an episode's enclosure + metadata.
type FeedItem struct {
	GUID          string
	Title         string
	Description   string
	AudioURL      string
	AudioBytes    int64
	AudioMimeType string
	Duration      int // seconds
	EpisodeNumber *int
	SeasonNumber  *int
	PublishedAt   time.Time
}

// rssDateFormat is RFC 1123Z, the pubDate format the RSS 2.0 spec (RFC 822)
// requires and every podcast client expects.
const rssDateFormat = "Mon, 02 Jan 2006 15:04:05 -0700"

// Render produces the RSS 2.0 XML document (with the iTunes podcast
// namespace) for a feed. Output is a complete document including the XML
// declaration, safe to write straight to an application/rss+xml response.
func Render(f Feed) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:atom="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("  <channel>\n")

	lang := f.Language
	if lang == "" {
		lang = "en"
	}
	writeTag(&b, "    ", "title", f.Title)
	if f.Link != "" {
		writeTag(&b, "    ", "link", f.Link)
	}
	if f.FeedURL != "" {
		b.WriteString(fmt.Sprintf(`    <atom:link href="%s" rel="self" type="application/rss+xml"/>`+"\n", escapeAttr(f.FeedURL)))
	}
	writeTag(&b, "    ", "description", f.Description)
	writeTag(&b, "    ", "language", lang)
	if f.Author != "" {
		writeTag(&b, "    ", "itunes:author", f.Author)
	}
	if f.Category != "" {
		b.WriteString(fmt.Sprintf(`    <itunes:category text="%s"/>`+"\n", escapeAttr(f.Category)))
	}
	if f.ImageURL != "" {
		b.WriteString(fmt.Sprintf(`    <itunes:image href="%s"/>`+"\n", escapeAttr(f.ImageURL)))
	}

	for _, e := range f.Episodes {
		b.WriteString("    <item>\n")
		writeTag(&b, "      ", "title", e.Title)
		writeTag(&b, "      ", "description", e.Description)
		guid := e.GUID
		if guid == "" {
			guid = e.AudioURL
		}
		b.WriteString(fmt.Sprintf(`      <guid isPermaLink="false">%s</guid>`+"\n", escapeText(guid)))
		if !e.PublishedAt.IsZero() {
			writeTag(&b, "      ", "pubDate", e.PublishedAt.UTC().Format(rssDateFormat))
		}
		mime := e.AudioMimeType
		if mime == "" {
			mime = "audio/mpeg"
		}
		b.WriteString(fmt.Sprintf(`      <enclosure url="%s" length="%d" type="%s"/>`+"\n",
			escapeAttr(e.AudioURL), e.AudioBytes, escapeAttr(mime)))
		if e.Duration > 0 {
			writeTag(&b, "      ", "itunes:duration", formatDuration(e.Duration))
		}
		if e.EpisodeNumber != nil {
			writeTag(&b, "      ", "itunes:episode", fmt.Sprintf("%d", *e.EpisodeNumber))
		}
		if e.SeasonNumber != nil {
			writeTag(&b, "      ", "itunes:season", fmt.Sprintf("%d", *e.SeasonNumber))
		}
		b.WriteString("    </item>\n")
	}

	b.WriteString("  </channel>\n")
	b.WriteString("</rss>\n")
	return b.String()
}

// formatDuration renders seconds as H:MM:SS (or M:SS when under an hour),
// the <itunes:duration> form clients display.
func formatDuration(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func writeTag(b *strings.Builder, indent, tag, value string) {
	b.WriteString(fmt.Sprintf("%s<%s>%s</%s>\n", indent, tag, escapeText(value), tag))
}

// escapeText XML-escapes element text via encoding/xml, so any characters
// in author-supplied titles/descriptions can never break the document.
func escapeText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// escapeAttr escapes a value destined for a double-quoted attribute. It
// reuses escapeText (which already escapes <, >, &, and quotes) so URLs
// with query strings embed safely.
func escapeAttr(s string) string {
	return escapeText(s)
}

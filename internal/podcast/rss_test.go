package podcast

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func ptrInt(n int) *int { return &n }

// TestRenderWellFormedAndComplete proves the rendered feed is valid XML and
// carries the channel + item fields a podcast client needs.
func TestRenderWellFormedAndComplete(t *testing.T) {
	pub := time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC)
	feed := Feed{
		Title:       "Growth Weekly",
		Link:        "https://lms.example/o/acme/podcasts/growth-weekly",
		FeedURL:     "https://lms.example/o/acme/podcasts/growth-weekly/rss.xml",
		Description: "Lessons on growth",
		Author:      "Acme Team",
		Language:    "en",
		Category:    "Education",
		ImageURL:    "https://cdn.example/cover.jpg",
		Episodes: []FeedItem{{
			GUID:          "ep-1",
			Title:         "Episode 1",
			Description:   "Intro",
			AudioURL:      "https://cdn.example/ep1.mp3",
			AudioBytes:    123456,
			AudioMimeType: "audio/mpeg",
			Duration:      3725, // 1:02:05
			EpisodeNumber: ptrInt(1),
			SeasonNumber:  ptrInt(1),
			PublishedAt:   pub,
		}},
	}

	out := Render(feed)

	// Must be parseable XML.
	if err := xml.Unmarshal([]byte(out), new(struct {
		XMLName xml.Name `xml:"rss"`
	})); err != nil {
		t.Fatalf("rendered feed is not well-formed XML: %v\n%s", err, out)
	}

	for _, want := range []string{
		`<rss version="2.0"`,
		`xmlns:itunes=`,
		`<title>Growth Weekly</title>`,
		`<atom:link href="https://lms.example/o/acme/podcasts/growth-weekly/rss.xml" rel="self"`,
		`<itunes:author>Acme Team</itunes:author>`,
		`<itunes:category text="Education"/>`,
		`<itunes:image href="https://cdn.example/cover.jpg"/>`,
		`<guid isPermaLink="false">ep-1</guid>`,
		`<enclosure url="https://cdn.example/ep1.mp3" length="123456" type="audio/mpeg"/>`,
		`<itunes:duration>1:02:05</itunes:duration>`,
		`<itunes:episode>1</itunes:episode>`,
		`<pubDate>Mon, 20 Jul 2026 09:30:00 +0000</pubDate>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered feed missing %q\n---\n%s", want, out)
		}
	}
}

// TestRenderEscapesAuthorContent proves author-supplied text can never break
// the document or inject markup — the whole reason rendering lives in one
// escaped place.
func TestRenderEscapesAuthorContent(t *testing.T) {
	feed := Feed{
		Title:       `Tom & Jerry <hax>`,
		Description: `A "quoted" & <bold> tale`,
		Language:    "en",
		Episodes: []FeedItem{{
			Title:       `Ep <script>alert(1)</script>`,
			AudioURL:    "https://cdn.example/x.mp3?a=1&b=2",
			PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
	out := Render(feed)

	if strings.Contains(out, "<script>") || strings.Contains(out, "<hax>") || strings.Contains(out, "<bold>") {
		t.Fatalf("author content not escaped:\n%s", out)
	}
	if err := xml.Unmarshal([]byte(out), new(struct {
		XMLName xml.Name `xml:"rss"`
	})); err != nil {
		t.Fatalf("feed with hostile content is not well-formed XML: %v", err)
	}
	// The ampersand in the enclosure URL must be entity-encoded.
	if !strings.Contains(out, "a=1&amp;b=2") {
		t.Errorf("enclosure URL ampersand not escaped:\n%s", out)
	}
}

// TestFormatDuration covers the H:MM:SS vs M:SS boundary.
func TestFormatDuration(t *testing.T) {
	cases := map[int]string{
		0:    "0:00",
		5:    "0:05",
		65:   "1:05",
		3600: "1:00:00",
		3725: "1:02:05",
	}
	for secs, want := range cases {
		if got := formatDuration(secs); got != want {
			t.Errorf("formatDuration(%d) = %q, want %q", secs, got, want)
		}
	}
}

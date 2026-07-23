package handlers

import "testing"

func TestHTMLEscape(t *testing.T) {
	cases := map[string]string{
		`hello`:             `hello`,
		`<script>`:          `&lt;script&gt;`,
		`Tom & Jerry`:       `Tom &amp; Jerry`,
		`"quoted"`:          `&quot;quoted&quot;`,
		`<a href="x">y</a>`: `&lt;a href=&quot;x&quot;&gt;y&lt;/a&gt;`,
	}
	for in, want := range cases {
		if got := htmlEscape(in); got != want {
			t.Errorf("htmlEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

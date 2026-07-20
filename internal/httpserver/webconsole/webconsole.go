// Package webconsole embeds a single self-contained HTML/JS page that
// exercises the JSON API from a browser for manual testing. It is not
// part of the product UI (the plan calls for a server-rendered
// HTML+HTMX+Tailwind frontend once course/lesson work starts) — this
// exists only so Task 1-3 auth/org/tenancy endpoints can be driven
// interactively instead of via curl.
package webconsole

import _ "embed"

//go:embed index.html
var IndexHTML []byte

package metrics

import (
	"strings"
	"testing"
)

func TestWriteProm(t *testing.T) {
	r := NewRegistry()
	r.ObserveRequest("GET", "2xx", 0.003)
	r.ObserveRequest("GET", "2xx", 0.4)
	r.ObserveRequest("POST", "5xx", 1.2)
	r.IncPanic()
	r.IncPanic()
	r.SetGauge("worker_queue_depth", 7)

	var sb strings.Builder
	r.WriteProm(&sb)
	out := sb.String()

	wants := []string{
		`lms_http_requests_total{method="GET",status="2xx"} 2`,
		`lms_http_requests_total{method="POST",status="5xx"} 1`,
		`lms_http_request_duration_seconds_count{method="GET"} 2`,
		// 0.003 and 0.4 both fall at/below the +Inf bound; only 0.003 is <= 0.005.
		`lms_http_request_duration_seconds_bucket{method="GET",le="0.005"} 1`,
		`lms_http_request_duration_seconds_bucket{method="GET",le="+Inf"} 2`,
		`lms_panics_total 2`,
		`lms_worker_queue_depth 7`,
		"# TYPE lms_http_requests_total counter",
		"# TYPE lms_http_request_duration_seconds histogram",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("metrics output missing %q\n---\n%s", w, out)
		}
	}
}

func TestObserveRequestBucketMonotonic(t *testing.T) {
	r := NewRegistry()
	r.ObserveRequest("GET", "2xx", 0.02) // <= 0.025, 0.05, ... but not <= 0.01
	var sb strings.Builder
	r.WriteProm(&sb)
	out := sb.String()

	if strings.Contains(out, `le="0.01"} 1`) {
		t.Error("0.02 should not be counted in the 0.01 bucket")
	}
	if !strings.Contains(out, `le="0.025"} 1`) {
		t.Error("0.02 should be counted in the 0.025 bucket")
	}
}

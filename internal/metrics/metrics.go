// Package metrics is a tiny, dependency-free Prometheus exposition layer for
// Task 10's observability requirement (metrics/traces/request-IDs). It
// deliberately avoids pulling in prometheus/client_golang: the platform's
// metric surface is small and well-known (HTTP request counts/latency, panic
// count, a handful of operational gauges), so a self-contained implementation
// keeps the dependency tree and attack surface minimal. The text output
// conforms to the Prometheus exposition format so any standard scraper,
// Grafana Agent, or `promtool` can consume /metrics unchanged.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// durationBuckets are the cumulative "le" upper bounds (seconds) for the HTTP
// request-duration histogram. Chosen to straddle typical web latencies from a
// fast cache hit (5ms) to a slow checkout/webhook path (5s).
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry holds all process-wide metrics. A single package-level Default is
// used by the HTTP middleware; tests may construct their own.
type Registry struct {
	mu sync.Mutex

	// requestCounts is keyed by "method\x00status" so cardinality stays
	// bounded (path is deliberately NOT a label — an unbounded path label is
	// the classic Prometheus cardinality blowup).
	requestCounts map[string]uint64
	// durSum/durCount/durBucket accumulate the histogram, keyed by method.
	durSum    map[string]float64
	durCount  map[string]uint64
	durBucket map[string][]uint64 // len == len(durationBuckets)

	panics uint64

	// gauges is a small set of named operational gauges (e.g. queue depth)
	// pushed by background collectors.
	gauges map[string]int64
}

// NewRegistry builds an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		requestCounts: map[string]uint64{},
		durSum:        map[string]float64{},
		durCount:      map[string]uint64{},
		durBucket:     map[string][]uint64{},
		gauges:        map[string]int64{},
	}
}

// Default is the process-wide registry the HTTP middleware writes to.
var Default = NewRegistry()

// ObserveRequest records one completed HTTP request.
func (r *Registry) ObserveRequest(method, status string, durationSeconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.requestCounts[method+"\x00"+status]++

	r.durSum[method] += durationSeconds
	r.durCount[method]++
	b := r.durBucket[method]
	if b == nil {
		b = make([]uint64, len(durationBuckets))
	}
	for i, le := range durationBuckets {
		if durationSeconds <= le {
			b[i]++
		}
	}
	r.durBucket[method] = b
}

// IncPanic records a recovered panic.
func (r *Registry) IncPanic() { atomic.AddUint64(&r.panics, 1) }

// SetGauge sets a named operational gauge (last-write-wins).
func (r *Registry) SetGauge(name string, value int64) {
	r.mu.Lock()
	r.gauges[name] = value
	r.mu.Unlock()
}

// WriteProm emits the full registry in Prometheus text exposition format.
func (r *Registry) WriteProm(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(w, "# HELP lms_http_requests_total Total HTTP requests by method and status.")
	fmt.Fprintln(w, "# TYPE lms_http_requests_total counter")
	keys := make([]string, 0, len(r.requestCounts))
	for k := range r.requestCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		method, status := splitKey(k)
		fmt.Fprintf(w, "lms_http_requests_total{method=%q,status=%q} %d\n", method, status, r.requestCounts[k])
	}

	fmt.Fprintln(w, "# HELP lms_http_request_duration_seconds HTTP request latency by method.")
	fmt.Fprintln(w, "# TYPE lms_http_request_duration_seconds histogram")
	methods := make([]string, 0, len(r.durCount))
	for m := range r.durCount {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	for _, m := range methods {
		buckets := r.durBucket[m]
		for i, le := range durationBuckets {
			fmt.Fprintf(w, "lms_http_request_duration_seconds_bucket{method=%q,le=%q} %d\n", m, formatFloat(le), buckets[i])
		}
		fmt.Fprintf(w, "lms_http_request_duration_seconds_bucket{method=%q,le=\"+Inf\"} %d\n", m, r.durCount[m])
		fmt.Fprintf(w, "lms_http_request_duration_seconds_sum{method=%q} %g\n", m, r.durSum[m])
		fmt.Fprintf(w, "lms_http_request_duration_seconds_count{method=%q} %d\n", m, r.durCount[m])
	}

	fmt.Fprintln(w, "# HELP lms_panics_total Total recovered panics.")
	fmt.Fprintln(w, "# TYPE lms_panics_total counter")
	fmt.Fprintf(w, "lms_panics_total %d\n", atomic.LoadUint64(&r.panics))

	gnames := make([]string, 0, len(r.gauges))
	for n := range r.gauges {
		gnames = append(gnames, n)
	}
	sort.Strings(gnames)
	for _, n := range gnames {
		fmt.Fprintf(w, "# TYPE lms_%s gauge\n", n)
		fmt.Fprintf(w, "lms_%s %d\n", n, r.gauges[n])
	}
}

func splitKey(k string) (method, status string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

func formatFloat(f float64) string { return fmt.Sprintf("%g", f) }

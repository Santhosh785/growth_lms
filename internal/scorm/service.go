package scorm

import (
	"strconv"
	"strings"
)

// normStatus lowercases and trims a controlled-vocabulary status value so the
// derived RuntimeState (and the summary columns persisted from it) compare and
// store consistently. ValidateElement accepts case-insensitive status writes
// (a SCO may send "Completed"), so the same normalization must happen here —
// otherwise a valid mixed-case status would never satisfy the lowercase
// comparisons in isComplete/deriveLessonStatus and completion would be lost.
func normStatus(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// Service is the DB-free facade the handler layer holds (mirroring
// codeexec.Service / ai.Service): it parses/validates manifests and applies a
// SCO's committed CMI elements onto a RuntimeState. It holds no state and no
// database handle by design — package/attempt persistence, feature-gating, and
// the audit trail are the handler and model layers' job — so it is fully
// unit-testable from bytes and maps alone.
type Service struct{}

// NewService returns the stateless SCORM service.
func NewService() *Service { return &Service{} }

// ParseManifest validates an imsmanifest.xml and returns its normalized
// projection. Thin method wrapper so callers depend on the injected Service.
func (s *Service) ParseManifest(data []byte) (*Package, error) { return ParseManifest(data) }

// ValidateElement reports whether a SCO may write value to element under
// version (see the package-level ValidateElement).
func (s *Service) ValidateElement(version Version, element, value string) error {
	return ValidateElement(version, element, value)
}

// RuntimeState is the normalized, denormalized-for-reporting view of a SCO's
// tracked state: the fields that map to scorm_attempts summary columns, plus
// the full committed element map for exact resume and detailed reporting.
type RuntimeState struct {
	Version          Version
	LessonStatus     string // unified 1.2-style status (derived for 2004)
	CompletionStatus string // 2004 cmi.completion_status
	SuccessStatus    string // 2004 cmi.success_status
	ScoreRaw         *float64
	ScoreMin         *float64
	ScoreMax         *float64
	ScoreScaled      *float64
	SessionSeconds   int
	Location         string
	SuspendData      string
	Exit             string
	Complete         bool
	Elements         map[string]string
}

// Summarize folds a full committed CMI element map into a RuntimeState. It does
// not re-validate (ApplyBatch does that on the write path); it reads the
// tracking-relevant elements and derives a unified lesson status. Unknown
// elements are ignored for the summary but preserved in Elements by the caller.
func (s *Service) Summarize(version Version, elements map[string]string) RuntimeState {
	st := RuntimeState{Version: version, Elements: elements}

	get := func(key string) (string, bool) { v, ok := elements[key]; return v, ok }
	setFloat := func(key string, dst **float64) {
		if v, ok := get(key); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				f := f
				*dst = &f
			}
		}
	}

	switch version {
	case Version12:
		if v, ok := get("cmi.core.lesson_status"); ok {
			st.LessonStatus = normStatus(v)
		}
		setFloat("cmi.core.score.raw", &st.ScoreRaw)
		setFloat("cmi.core.score.min", &st.ScoreMin)
		setFloat("cmi.core.score.max", &st.ScoreMax)
		if v, ok := get("cmi.core.session_time"); ok {
			if secs, ok := parseTimespan12(v); ok {
				st.SessionSeconds = secs
			}
		}
		if v, ok := get("cmi.core.lesson_location"); ok {
			st.Location = v
		}
		if v, ok := get("cmi.core.exit"); ok {
			st.Exit = v
		}
	case Version2004:
		if v, ok := get("cmi.completion_status"); ok {
			st.CompletionStatus = normStatus(v)
		}
		if v, ok := get("cmi.success_status"); ok {
			st.SuccessStatus = normStatus(v)
		}
		setFloat("cmi.score.raw", &st.ScoreRaw)
		setFloat("cmi.score.min", &st.ScoreMin)
		setFloat("cmi.score.max", &st.ScoreMax)
		setFloat("cmi.score.scaled", &st.ScoreScaled)
		if v, ok := get("cmi.session_time"); ok {
			if secs, ok := parseDuration2004(v); ok {
				st.SessionSeconds = secs
			}
		}
		if v, ok := get("cmi.location"); ok {
			st.Location = v
		}
		if v, ok := get("cmi.exit"); ok {
			st.Exit = v
		}
		st.LessonStatus = deriveLessonStatus(st.CompletionStatus, st.SuccessStatus)
	}

	if v, ok := get("cmi.suspend_data"); ok {
		st.SuspendData = v
	}
	st.Complete = isComplete(version, st)
	return st
}

// deriveLessonStatus collapses a SCORM 2004 (completion_status, success_status)
// pair into a single 1.2-style lesson status, so reporting shows a uniform
// column across both versions. success (passed/failed) takes precedence when
// known; otherwise completion drives it.
func deriveLessonStatus(completion, success string) string {
	switch success {
	case "passed":
		return "passed"
	case "failed":
		return "failed"
	}
	switch completion {
	case "completed":
		return "completed"
	case "incomplete":
		return "incomplete"
	case "not attempted":
		return "not attempted"
	default:
		return "incomplete"
	}
}

// isComplete reports whether a run's terminal state counts as finished, for the
// scorm_attempts.is_complete flag: a 1.2 status of passed/completed/failed, or
// a 2004 completion_status of completed (or a decisive success_status).
func isComplete(version Version, st RuntimeState) bool {
	switch version {
	case Version12:
		switch st.LessonStatus {
		case "passed", "completed", "failed":
			return true
		}
	case Version2004:
		if st.CompletionStatus == "completed" {
			return true
		}
		if st.SuccessStatus == "passed" || st.SuccessStatus == "failed" {
			return true
		}
	}
	return false
}

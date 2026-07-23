package scorm

import (
	"strconv"
	"strings"
)

// This file models the CMI data model the browser API adapter reads and writes
// during a SCO's run: which elements exist, which are read-only, how a value is
// validated, and how a batch of committed elements folds into a normalized
// RuntimeState the model/reporting layer stores. It covers the scalar elements
// that matter for tracking and completion; the SCORM 2004 collection elements
// (interactions.*, objectives.*) are accepted as writable and carried verbatim
// in the element map for detailed reporting without per-index validation here.

// elementSpec describes one CMI element: whether a SCO may only read it, and an
// optional value validator applied on write.
type elementSpec struct {
	readOnly bool
	validate func(string) error
}

// Controlled vocabularies.
var (
	lessonStatus12   = set("passed", "completed", "failed", "incomplete", "browsed", "not attempted")
	completionStatus = set("completed", "incomplete", "not attempted", "unknown")
	successStatus    = set("passed", "failed", "unknown")
	exit12           = set("time-out", "suspend", "logout", "")
	exit2004         = set("time-out", "suspend", "logout", "normal", "")
)

// elements12 is the SCORM 1.2 CMI element set the runtime accepts.
var elements12 = map[string]elementSpec{
	"cmi.core.student_id":                {readOnly: true},
	"cmi.core.student_name":              {readOnly: true},
	"cmi.core.lesson_location":           {validate: maxLen(255)},
	"cmi.core.credit":                    {readOnly: true},
	"cmi.core.lesson_status":             {validate: oneOf(lessonStatus12)},
	"cmi.core.entry":                     {readOnly: true},
	"cmi.core.score.raw":                 {validate: floatIn(0, 100)},
	"cmi.core.score.min":                 {validate: floatIn(0, 100)},
	"cmi.core.score.max":                 {validate: floatIn(0, 100)},
	"cmi.core.total_time":                {readOnly: true},
	"cmi.core.lesson_mode":               {readOnly: true},
	"cmi.core.exit":                      {validate: oneOf(exit12)},
	"cmi.core.session_time":              {validate: validTimespan12},
	"cmi.suspend_data":                   {validate: maxLen(4096)},
	"cmi.launch_data":                    {readOnly: true},
	"cmi.comments":                       {validate: maxLen(4096)},
	"cmi.comments_from_lms":              {readOnly: true},
	"cmi.student_data.mastery_score":     {readOnly: true},
	"cmi.student_data.max_time_allowed":  {readOnly: true},
	"cmi.student_data.time_limit_action": {readOnly: true},
}

// elements2004 is the SCORM 2004 CMI element set the runtime accepts.
var elements2004 = map[string]elementSpec{
	"cmi.learner_id":           {readOnly: true},
	"cmi.learner_name":         {readOnly: true},
	"cmi.location":             {validate: maxLen(1000)},
	"cmi.credit":               {readOnly: true},
	"cmi.completion_status":    {validate: oneOf(completionStatus)},
	"cmi.success_status":       {validate: oneOf(successStatus)},
	"cmi.entry":                {readOnly: true},
	"cmi.score.raw":            {validate: anyFloat},
	"cmi.score.min":            {validate: anyFloat},
	"cmi.score.max":            {validate: anyFloat},
	"cmi.score.scaled":         {validate: floatIn(-1, 1)},
	"cmi.total_time":           {readOnly: true},
	"cmi.mode":                 {readOnly: true},
	"cmi.exit":                 {validate: oneOf(exit2004)},
	"cmi.session_time":         {validate: validDuration2004},
	"cmi.suspend_data":         {validate: maxLen(64000)},
	"cmi.launch_data":          {readOnly: true},
	"cmi.progress_measure":     {validate: floatIn(0, 1)},
	"cmi.completion_threshold": {readOnly: true},
	"cmi.scaled_passing_score": {readOnly: true},
	"cmi.max_time_allowed":     {readOnly: true},
	"cmi.time_limit_action":    {readOnly: true},
}

// lookup resolves an element's spec for a version, treating the 2004 collection
// elements (interactions.*, objectives.*, comments_from_learner.*) as writable
// opaque values so a SCO can record them and they round-trip in the element map.
func lookup(version Version, element string) (elementSpec, bool) {
	var table map[string]elementSpec
	switch version {
	case Version12:
		table = elements12
	case Version2004:
		table = elements2004
	default:
		return elementSpec{}, false
	}
	if spec, ok := table[element]; ok {
		return spec, true
	}
	if version == Version2004 &&
		(strings.HasPrefix(element, "cmi.interactions.") ||
			strings.HasPrefix(element, "cmi.objectives.") ||
			strings.HasPrefix(element, "cmi.comments_from_learner.")) {
		return elementSpec{validate: maxLen(4096)}, true
	}
	if version == Version12 &&
		(strings.HasPrefix(element, "cmi.interactions.") ||
			strings.HasPrefix(element, "cmi.objectives.")) {
		return elementSpec{validate: maxLen(4096)}, true
	}
	return elementSpec{}, false
}

// ValidateElement reports whether a SCO may write value to element under the
// given version, returning a sentinel error (ErrUnsupportedCMIElement /
// ErrReadOnlyCMIElement / ErrInvalidCMIElementValue) the handler maps to a 400.
func ValidateElement(version Version, element, value string) error {
	spec, ok := lookup(version, element)
	if !ok {
		return wrapf(ErrUnsupportedCMIElement, "%s", element)
	}
	if spec.readOnly {
		return wrapf(ErrReadOnlyCMIElement, "%s", element)
	}
	if spec.validate != nil {
		if err := spec.validate(value); err != nil {
			return wrapf(ErrInvalidCMIElementValue, "%s=%q: %v", element, value, err)
		}
	}
	return nil
}

// --- validators ------------------------------------------------------------

func set(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

func oneOf(vocab map[string]bool) func(string) error {
	return func(v string) error {
		if !vocab[strings.ToLower(strings.TrimSpace(v))] {
			return errValue("not an allowed value")
		}
		return nil
	}
}

func floatIn(min, max float64) func(string) error {
	return func(v string) error {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return errValue("not a number")
		}
		if f < min || f > max {
			return errValue("out of range")
		}
		return nil
	}
}

func anyFloat(v string) error {
	if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
		return errValue("not a number")
	}
	return nil
}

func maxLen(n int) func(string) error {
	return func(v string) error {
		if len(v) > n {
			return errValue("too long")
		}
		return nil
	}
}

func validTimespan12(v string) error {
	if _, ok := parseTimespan12(v); !ok {
		return errValue("not a CMITimespan (HH:MM:SS.ss)")
	}
	return nil
}

func validDuration2004(v string) error {
	if _, ok := parseDuration2004(v); !ok {
		return errValue("not an ISO 8601 duration")
	}
	return nil
}

// errValue is a tiny error type so validators avoid importing errors/fmt for
// one-off messages; the message is folded into ErrInvalidCMIElementValue.
type errValue string

func (e errValue) Error() string { return string(e) }

// parseTimespan12 parses a SCORM 1.2 CMITimespan "HH:MM:SS.ss" into whole
// seconds. Hours may exceed two digits per the spec.
func parseTimespan12(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || m > 59 || sec < 0 || sec >= 60 {
		return 0, false
	}
	return h*3600 + m*60 + int(sec), true
}

// parseDuration2004 parses a SCORM 2004 ISO 8601 duration (e.g. "PT1H30M5S",
// "P1DT2H") into whole seconds. Year/month components are not tracked-time
// realistic and are rejected to avoid ambiguous conversions.
func parseDuration2004(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "P") {
		return 0, false
	}
	body := s[1:]
	if body == "" {
		return 0, false
	}
	datePart, timePart, hasT := strings.Cut(body, "T")
	total := 0
	// Date part: only days (D) and weeks (W) are unambiguous in seconds.
	if datePart != "" {
		days, ok := scanDurationField(datePart, map[byte]int{'W': 7 * 86400, 'D': 86400})
		if !ok {
			return 0, false
		}
		total += days
	}
	if hasT {
		if timePart == "" {
			return 0, false
		}
		t, ok := scanDurationField(timePart, map[byte]int{'H': 3600, 'M': 60, 'S': 1})
		if !ok {
			return 0, false
		}
		total += t
	} else if datePart == "" {
		return 0, false
	}
	return total, true
}

// scanDurationField sums the numeric run before each recognized unit letter.
// Fractional seconds are truncated. Any unrecognized character fails the parse.
func scanDurationField(s string, units map[byte]int) (int, bool) {
	total := 0
	num := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			num += string(c)
			continue
		}
		mult, ok := units[c]
		if !ok || num == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		total += int(f) * mult
		num = ""
	}
	if num != "" {
		return 0, false // trailing number with no unit
	}
	return total, true
}

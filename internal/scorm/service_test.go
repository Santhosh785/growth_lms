package scorm

import (
	"errors"
	"testing"
)

const manifest12 = `<?xml version="1.0" encoding="UTF-8"?>
<manifest identifier="MAN-001" version="1.0"
  xmlns="http://www.imsproject.org/xsd/imscp_rootv1p1p2"
  xmlns:adlcp="http://www.adlnet.org/xsd/adlcp_rootv1p2">
  <metadata>
    <schema>ADL SCORM</schema>
    <schemaversion>1.2</schemaversion>
  </metadata>
  <organizations default="ORG-1">
    <organization identifier="ORG-1">
      <title>Intro Course</title>
      <item identifier="ITEM-1" identifierref="RES-1">
        <title>Module One</title>
        <adlcp:masteryscore>80</adlcp:masteryscore>
      </item>
      <item identifier="ITEM-2" identifierref="RES-2">
        <title>Module Two</title>
      </item>
    </organization>
  </organizations>
  <resources>
    <resource identifier="RES-1" type="webcontent" adlcp:scormtype="sco" href="mod1/index.html"/>
    <resource identifier="RES-2" type="webcontent" adlcp:scormtype="sco" href="mod2/index.html"/>
  </resources>
</manifest>`

const manifest2004 = `<?xml version="1.0" encoding="UTF-8"?>
<manifest identifier="MAN-2004" version="1.0"
  xmlns="http://www.imsglobal.org/xsd/imscp_v1p1"
  xmlns:adlcp="http://www.adlnet.org/xsd/adlcp_v1p3">
  <metadata>
    <schema>ADL SCORM</schema>
    <schemaversion>2004 4th Edition</schemaversion>
  </metadata>
  <organizations default="ORG-A">
    <organization identifier="ORG-A">
      <title>Advanced Course</title>
      <item identifier="I1" identifierref="R1">
        <title>Lesson</title>
      </item>
    </organization>
  </organizations>
  <resources>
    <resource identifier="R1" type="webcontent" adlcp:scormType="sco" href="content/start.html"/>
  </resources>
</manifest>`

func TestParseManifest12(t *testing.T) {
	pkg, err := ParseManifest([]byte(manifest12))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pkg.Version != Version12 {
		t.Errorf("version = %q, want 1.2", pkg.Version)
	}
	if pkg.Identifier != "MAN-001" {
		t.Errorf("identifier = %q", pkg.Identifier)
	}
	if pkg.Title != "Intro Course" {
		t.Errorf("title = %q", pkg.Title)
	}
	if pkg.LaunchHref != "mod1/index.html" {
		t.Errorf("launch = %q, want mod1/index.html", pkg.LaunchHref)
	}
	if len(pkg.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(pkg.Items))
	}
	if pkg.Items[1].LaunchHref != "mod2/index.html" {
		t.Errorf("item 2 launch = %q", pkg.Items[1].LaunchHref)
	}
	if pkg.MasteryScore == nil || *pkg.MasteryScore != 80 {
		t.Errorf("mastery = %v, want 80", pkg.MasteryScore)
	}
}

func TestParseManifest2004(t *testing.T) {
	pkg, err := ParseManifest([]byte(manifest2004))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pkg.Version != Version2004 {
		t.Errorf("version = %q, want 2004", pkg.Version)
	}
	if pkg.LaunchHref != "content/start.html" {
		t.Errorf("launch = %q", pkg.LaunchHref)
	}
	if pkg.Title != "Advanced Course" {
		t.Errorf("title = %q", pkg.Title)
	}
}

func TestParseManifestErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyManifest},
		{"malformed", "<manifest><oops", ErrInvalidManifest},
		{"wrong root", `<other></other>`, ErrInvalidManifest},
		{"unknown version", `<manifest><metadata><schemaversion>9.9</schemaversion></metadata>
			<organizations default="o"><organization identifier="o"><title>t</title></organization></organizations></manifest>`, ErrUnknownVersion},
		{"no org", `<manifest><metadata><schemaversion>1.2</schemaversion></metadata>
			<organizations></organizations></manifest>`, ErrNoDefaultOrganization},
		{"no launchable", `<manifest><metadata><schemaversion>1.2</schemaversion></metadata>
			<organizations default="o"><organization identifier="o"><title>t</title>
			<item identifier="i"><title>x</title></item></organization></organizations>
			<resources></resources></manifest>`, ErrNoLaunchableResource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.in))
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// The default organization is chosen by @default even when it is not first.
func TestParseManifestDefaultOrganizationSelection(t *testing.T) {
	in := `<manifest identifier="M"><metadata><schemaversion>1.2</schemaversion></metadata>
	<organizations default="SECOND">
	  <organization identifier="FIRST"><title>First</title>
	    <item identifier="a" identifierref="ra"><title>A</title></item></organization>
	  <organization identifier="SECOND"><title>Second</title>
	    <item identifier="b" identifierref="rb"><title>B</title></item></organization>
	</organizations>
	<resources>
	  <resource identifier="ra" href="a.html"/>
	  <resource identifier="rb" href="b.html"/>
	</resources></manifest>`
	pkg, err := ParseManifest([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pkg.Title != "Second" || pkg.LaunchHref != "b.html" {
		t.Errorf("chose wrong org: title=%q launch=%q", pkg.Title, pkg.LaunchHref)
	}
}

func TestValidateElement12(t *testing.T) {
	s := NewService()
	if err := s.ValidateElement(Version12, "cmi.core.lesson_status", "completed"); err != nil {
		t.Errorf("valid status rejected: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.lesson_status", "bogus"); !errors.Is(err, ErrInvalidCMIElementValue) {
		t.Errorf("bad status: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.student_id", "x"); !errors.Is(err, ErrReadOnlyCMIElement) {
		t.Errorf("read-only write: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.no_such", "x"); !errors.Is(err, ErrUnsupportedCMIElement) {
		t.Errorf("unknown element: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.score.raw", "150"); !errors.Is(err, ErrInvalidCMIElementValue) {
		t.Errorf("out-of-range score: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.session_time", "00:12:30"); err != nil {
		t.Errorf("valid timespan rejected: %v", err)
	}
	if err := s.ValidateElement(Version12, "cmi.core.session_time", "12m"); !errors.Is(err, ErrInvalidCMIElementValue) {
		t.Errorf("bad timespan: %v", err)
	}
}

func TestValidateElement2004(t *testing.T) {
	s := NewService()
	if err := s.ValidateElement(Version2004, "cmi.score.scaled", "0.85"); err != nil {
		t.Errorf("valid scaled rejected: %v", err)
	}
	if err := s.ValidateElement(Version2004, "cmi.score.scaled", "2"); !errors.Is(err, ErrInvalidCMIElementValue) {
		t.Errorf("scaled out of [-1,1]: %v", err)
	}
	if err := s.ValidateElement(Version2004, "cmi.session_time", "PT1H30M"); err != nil {
		t.Errorf("valid duration rejected: %v", err)
	}
	// interactions.* accepted as writable opaque.
	if err := s.ValidateElement(Version2004, "cmi.interactions.0.id", "q1"); err != nil {
		t.Errorf("interactions rejected: %v", err)
	}
	if err := s.ValidateElement(Version2004, "cmi.total_time", "PT1H"); !errors.Is(err, ErrReadOnlyCMIElement) {
		t.Errorf("total_time should be read-only: %v", err)
	}
}

func TestSummarize12(t *testing.T) {
	s := NewService()
	st := s.Summarize(Version12, map[string]string{
		"cmi.core.lesson_status":   "passed",
		"cmi.core.score.raw":       "92",
		"cmi.core.session_time":    "00:05:00",
		"cmi.core.lesson_location": "page-3",
		"cmi.suspend_data":         "abc",
	})
	if st.LessonStatus != "passed" || !st.Complete {
		t.Errorf("status=%q complete=%v", st.LessonStatus, st.Complete)
	}
	if st.ScoreRaw == nil || *st.ScoreRaw != 92 {
		t.Errorf("score = %v", st.ScoreRaw)
	}
	if st.SessionSeconds != 300 {
		t.Errorf("session = %d, want 300", st.SessionSeconds)
	}
	if st.Location != "page-3" || st.SuspendData != "abc" {
		t.Errorf("location=%q suspend=%q", st.Location, st.SuspendData)
	}
}

func TestSummarize2004DerivesLessonStatus(t *testing.T) {
	s := NewService()
	st := s.Summarize(Version2004, map[string]string{
		"cmi.completion_status": "completed",
		"cmi.success_status":    "passed",
		"cmi.score.scaled":      "0.9",
		"cmi.session_time":      "PT2M",
	})
	if st.LessonStatus != "passed" {
		t.Errorf("derived status = %q, want passed", st.LessonStatus)
	}
	if !st.Complete {
		t.Error("should be complete")
	}
	if st.SessionSeconds != 120 {
		t.Errorf("session = %d, want 120", st.SessionSeconds)
	}
	if st.ScoreScaled == nil || *st.ScoreScaled != 0.9 {
		t.Errorf("scaled = %v", st.ScoreScaled)
	}

	// Incomplete-and-unknown stays incomplete/not complete.
	st2 := s.Summarize(Version2004, map[string]string{"cmi.completion_status": "incomplete"})
	if st2.LessonStatus != "incomplete" || st2.Complete {
		t.Errorf("status=%q complete=%v", st2.LessonStatus, st2.Complete)
	}
}

// A SCO may send a case-variant status ("Completed"); ValidateElement accepts
// it, so Summarize must normalize it or completion is silently lost.
func TestSummarizeMixedCaseStatus(t *testing.T) {
	s := NewService()
	st := s.Summarize(Version12, map[string]string{
		"cmi.core.lesson_status": "Completed",
	})
	if st.LessonStatus != "completed" {
		t.Errorf("lesson status = %q, want normalized 'completed'", st.LessonStatus)
	}
	if !st.Complete {
		t.Error("a mixed-case 'Completed' must still mark the attempt complete")
	}

	st2 := s.Summarize(Version2004, map[string]string{
		"cmi.completion_status": "Completed",
		"cmi.success_status":    "Passed",
	})
	if st2.LessonStatus != "passed" || !st2.Complete {
		t.Errorf("2004 mixed-case: status=%q complete=%v", st2.LessonStatus, st2.Complete)
	}
}

func TestParseDuration2004(t *testing.T) {
	cases := map[string]struct {
		secs int
		ok   bool
	}{
		"PT1H":      {3600, true},
		"PT1H30M5S": {5405, true},
		"PT90M":     {5400, true},
		"P1DT2H":    {93600, true},
		"PT0S":      {0, true},
		"1H":        {0, false},
		"P":         {0, false},
		"PT":        {0, false},
		"garbage":   {0, false},
	}
	for in, want := range cases {
		got, ok := parseDuration2004(in)
		if ok != want.ok || (ok && got != want.secs) {
			t.Errorf("parseDuration2004(%q) = (%d,%v), want (%d,%v)", in, got, ok, want.secs, want.ok)
		}
	}
}

// Package scorm is Task 9's provider-free SCORM 1.2 / 2004 support layer. It
// has two responsibilities and deliberately no others:
//
//   - parse and validate an imsmanifest.xml — detect the SCORM version,
//     resolve the launch href of the default organization, and extract the
//     item tree for a table of contents;
//   - model the CMI runtime data the browser's API adapter reads and writes —
//     classify each element read-only/read-write, validate values, and fold a
//     batch of SetValue calls into a normalized RuntimeState (lesson/
//     completion/success status, score, time) that drives reporting.
//
// Everything database-facing (feature-flag gating, package/attempt persistence,
// the audit trail) lives in the handler and model layers, not here — this
// package never touches Postgres or the filesystem, which keeps it fully
// unit-testable from byte slices and maps alone. Actual asset storage and the
// in-browser runtime (the JS API adapter the SCO calls) are out of scope: this
// package validates what was imported and normalizes what the SCO committed.
package scorm

import (
	"errors"
	"fmt"
	"strings"
)

// Version identifies the CMI runtime a package targets. The string values
// mirror the scorm_packages.version CHECK constraint (migration 000013); keep
// the two in lockstep.
type Version string

const (
	Version12   Version = "1.2"
	Version2004 Version = "2004"
)

// Valid reports whether v is a recognized SCORM version.
func (v Version) Valid() bool { return v == Version12 || v == Version2004 }

// Parsing / validation sentinel errors. Handlers map these to 400 so an author
// gets a clear reason an upload was rejected rather than an opaque 500.
var (
	ErrInvalidManifest        = errors.New("scorm: manifest is not valid XML")
	ErrUnknownVersion         = errors.New("scorm: could not determine SCORM version from manifest")
	ErrNoDefaultOrganization  = errors.New("scorm: manifest has no default organization")
	ErrNoLaunchableResource   = errors.New("scorm: manifest has no launchable resource")
	ErrEmptyManifest          = errors.New("scorm: manifest is empty")
	ErrUnsupportedCMIElement  = errors.New("scorm: unknown CMI element")
	ErrReadOnlyCMIElement     = errors.New("scorm: CMI element is read-only")
	ErrInvalidCMIElementValue = errors.New("scorm: invalid value for CMI element")
)

// Item is one node of a manifest organization's item tree. Children nest to
// form the table of contents a learner navigates. IdentifierRef points at the
// resource an item launches (empty for a grouping node with no content).
type Item struct {
	Identifier    string `json:"identifier"`
	Title         string `json:"title"`
	IdentifierRef string `json:"identifier_ref,omitempty"`
	LaunchHref    string `json:"launch_href,omitempty"`
	Children      []Item `json:"children,omitempty"`
}

// Package is the validated projection of an imsmanifest.xml: the detected
// version, the manifest/default-organization identifiers and title, the
// resolved launch href of the first launchable SCO, and the item tree. This is
// what the handler persists (version/identifier/launch_href as columns, the
// rest as the manifest JSONB) and what the model layer round-trips.
type Package struct {
	Version        Version  `json:"version"`
	Identifier     string   `json:"identifier"`
	OrganizationID string   `json:"organization_id"`
	Title          string   `json:"title"`
	LaunchHref     string   `json:"launch_href"`
	MasteryScore   *float64 `json:"mastery_score,omitempty"`
	Items          []Item   `json:"items"`
}

// wrapf gives a sentinel error extra context while preserving errors.Is.
func wrapf(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...))
}

// normalizeSpace collapses internal whitespace and trims a title/identifier
// read out of XML, where indentation is common.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

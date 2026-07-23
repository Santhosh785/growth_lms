// Package simulations is the DB-free core of Task 9's interactive simulations &
// diagrams module (mirroring internal/scorm and internal/codeexec): it parses,
// validates, and normalizes the author-supplied spec/config so the handler
// layer persists only well-formed content. It holds no state and no database
// handle — persistence, feature-gating, and the audit trail are the handler and
// model layers' job — so it is fully unit-testable from bytes alone.
//
// Two kinds of artifact share the table:
//
//   - a "diagram" is a source string in a known rendering format (mermaid, dot,
//     excalidraw) the client renders read-only;
//   - a "simulation" is a set of input parameters (controls) plus derived
//     outputs (expressions) the client evaluates live as the learner adjusts
//     the inputs.
//
// Nothing here executes the spec — rendering/evaluation is entirely client-side
// — so validation is purely structural (well-formed, within limits), never a
// sandbox concern.
package simulations

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Kind is the artifact type. The values mirror the simulations.kind CHECK in
// migration 000014 — keep the two in lockstep.
type Kind string

const (
	KindSimulation Kind = "simulation"
	KindDiagram    Kind = "diagram"
)

// Diagram source formats the client can render.
var diagramFormats = map[string]bool{"mermaid": true, "dot": true, "excalidraw": true}

// Parameter input types a simulation control can take.
var paramTypes = map[string]bool{"number": true, "boolean": true, "select": true}

// Sentinel validation errors. Each is wrapped with "simulations: " so the
// handler can strip that prefix for a clean client-facing message (see
// simReason in the handler), matching the internal/scorm convention.
var (
	ErrUnknownKind      = errors.New("simulations: kind must be 'simulation' or 'diagram'")
	ErrDiagramMissing   = errors.New("simulations: a diagram artifact needs a diagram spec")
	ErrUnknownFormat    = errors.New("simulations: unknown diagram format")
	ErrEmptySource      = errors.New("simulations: diagram source is empty")
	ErrSourceTooLarge   = errors.New("simulations: diagram source exceeds the size limit")
	ErrSimMissing       = errors.New("simulations: a simulation artifact needs a simulation spec")
	ErrNoParameters     = errors.New("simulations: a simulation needs at least one parameter")
	ErrTooManyParams    = errors.New("simulations: too many parameters")
	ErrParamName        = errors.New("simulations: every parameter needs a name")
	ErrDuplicateParam   = errors.New("simulations: duplicate parameter name")
	ErrParamType        = errors.New("simulations: parameter type must be 'number', 'boolean' or 'select'")
	ErrSelectNoOptions  = errors.New("simulations: a 'select' parameter needs at least one option")
	ErrNumberRange      = errors.New("simulations: parameter min must be <= max")
	ErrOutputName       = errors.New("simulations: every output needs a name")
	ErrDuplicateOutput  = errors.New("simulations: duplicate output name")
	ErrOutputExpr       = errors.New("simulations: every output needs an expression")
	ErrBadSpecJSON      = errors.New("simulations: spec is not valid JSON")
	ErrBadConfigJSON    = errors.New("simulations: config is not valid JSON")
	ErrNegativeInteract = errors.New("simulations: completion_interactions must be >= 0")
)

// Spec is the validated, normalized definition of one artifact. Exactly one of
// Diagram/Simulation is populated, matching Kind.
type Spec struct {
	Kind       Kind            `json:"kind"`
	Diagram    *Diagram        `json:"diagram,omitempty"`
	Simulation *SimulationBody `json:"simulation,omitempty"`
}

// Diagram is a client-rendered diagram source.
type Diagram struct {
	Format string `json:"format"`
	Source string `json:"source"`
}

// SimulationBody is a parameterized model the client evaluates live.
type SimulationBody struct {
	Parameters []Parameter `json:"parameters"`
	Outputs    []Output    `json:"outputs"`
}

// Parameter is one input control.
type Parameter struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Step    *float64 `json:"step,omitempty"`
	Default any      `json:"default,omitempty"`
	Options []string `json:"options,omitempty"`
}

// Output is one derived value; Expr is an opaque formula the client evaluates.
type Output struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Expr  string `json:"expr"`
}

// Config is the optional completion/grading policy for a simulation. Zero value
// = a learner completes only when the client explicitly marks it done.
type Config struct {
	// CompletionInteractions auto-completes the learner's progress once they
	// have recorded this many interactions (0 = never auto-complete).
	CompletionInteractions int `json:"completion_interactions,omitempty"`
	// PassingScore, when set, is the threshold a graded simulation compares a
	// learner's reported score against (rendered client-side; informational to
	// this layer).
	PassingScore *float64 `json:"passing_score,omitempty"`
}

// Service is the stateless DB-free facade the handler layer holds. Limits are
// configurable so the handler can pass operator-tuned caps.
type Service struct {
	MaxSourceBytes int
	MaxParameters  int
}

// DefaultMaxSourceBytes and DefaultMaxParameters bound a single artifact so a
// malicious or accidental giant spec can't bloat a row.
const (
	DefaultMaxSourceBytes = 64 << 10 // 64 KiB of diagram source
	DefaultMaxParameters  = 50
)

// NewService returns a Service with the given limits; non-positive limits fall
// back to the package defaults.
func NewService(maxSourceBytes, maxParameters int) *Service {
	if maxSourceBytes <= 0 {
		maxSourceBytes = DefaultMaxSourceBytes
	}
	if maxParameters <= 0 {
		maxParameters = DefaultMaxParameters
	}
	return &Service{MaxSourceBytes: maxSourceBytes, MaxParameters: maxParameters}
}

// ParseSpec unmarshals and validates a raw spec for the given kind, returning
// the normalized Spec. A malformed spec is a sentinel error the handler surfaces
// as a 400.
func (s *Service) ParseSpec(kind Kind, raw json.RawMessage) (*Spec, error) {
	var spec Spec
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, ErrBadSpecJSON
		}
	}
	spec.Kind = kind
	if err := s.validate(&spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// ParseConfig unmarshals and validates a raw config, returning the normalized
// Config. An empty config is valid (the zero policy).
func (s *Service) ParseConfig(raw json.RawMessage) (*Config, error) {
	var cfg Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, ErrBadConfigJSON
		}
	}
	if cfg.CompletionInteractions < 0 {
		return nil, ErrNegativeInteract
	}
	return &cfg, nil
}

// validate checks and normalizes a spec in place.
func (s *Service) validate(spec *Spec) error {
	switch spec.Kind {
	case KindDiagram:
		spec.Simulation = nil
		return s.validateDiagram(spec.Diagram)
	case KindSimulation:
		spec.Diagram = nil
		return s.validateSimulation(spec.Simulation)
	default:
		return ErrUnknownKind
	}
}

func (s *Service) validateDiagram(d *Diagram) error {
	if d == nil {
		return ErrDiagramMissing
	}
	d.Format = strings.ToLower(strings.TrimSpace(d.Format))
	if !diagramFormats[d.Format] {
		return fmt.Errorf("%w: %q", ErrUnknownFormat, d.Format)
	}
	d.Source = strings.TrimSpace(d.Source)
	if d.Source == "" {
		return ErrEmptySource
	}
	if len(d.Source) > s.MaxSourceBytes {
		return fmt.Errorf("%w (%d bytes)", ErrSourceTooLarge, s.MaxSourceBytes)
	}
	return nil
}

func (s *Service) validateSimulation(sim *SimulationBody) error {
	if sim == nil {
		return ErrSimMissing
	}
	if len(sim.Parameters) == 0 {
		return ErrNoParameters
	}
	if len(sim.Parameters) > s.MaxParameters {
		return fmt.Errorf("%w (max %d)", ErrTooManyParams, s.MaxParameters)
	}
	seen := make(map[string]bool, len(sim.Parameters))
	for i := range sim.Parameters {
		if err := normalizeParam(&sim.Parameters[i], seen); err != nil {
			return err
		}
	}
	outSeen := make(map[string]bool, len(sim.Outputs))
	for i := range sim.Outputs {
		if err := normalizeOutput(&sim.Outputs[i], outSeen); err != nil {
			return err
		}
	}
	return nil
}

func normalizeParam(p *Parameter, seen map[string]bool) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return ErrParamName
	}
	if seen[p.Name] {
		return fmt.Errorf("%w: %q", ErrDuplicateParam, p.Name)
	}
	seen[p.Name] = true
	if p.Label == "" {
		p.Label = p.Name
	}
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if !paramTypes[p.Type] {
		return fmt.Errorf("%w: %q", ErrParamType, p.Type)
	}
	switch p.Type {
	case "select":
		if len(p.Options) == 0 {
			return fmt.Errorf("%w: %q", ErrSelectNoOptions, p.Name)
		}
	case "number":
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return fmt.Errorf("%w: %q", ErrNumberRange, p.Name)
		}
	}
	return nil
}

func normalizeOutput(o *Output, seen map[string]bool) error {
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" {
		return ErrOutputName
	}
	if seen[o.Name] {
		return fmt.Errorf("%w: %q", ErrDuplicateOutput, o.Name)
	}
	seen[o.Name] = true
	if o.Label == "" {
		o.Label = o.Name
	}
	o.Expr = strings.TrimSpace(o.Expr)
	if o.Expr == "" {
		return fmt.Errorf("%w: %q", ErrOutputExpr, o.Name)
	}
	return nil
}

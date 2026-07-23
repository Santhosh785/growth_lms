package simulations

import (
	"encoding/json"
	"errors"
	"testing"
)

func svc() *Service { return NewService(0, 0) }

func TestParseSpec_Diagram_Valid(t *testing.T) {
	raw := json.RawMessage(`{"diagram":{"format":"Mermaid","source":"  graph TD; A-->B  "}}`)
	spec, err := svc().ParseSpec(KindDiagram, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Kind != KindDiagram || spec.Diagram == nil {
		t.Fatalf("expected diagram spec, got %+v", spec)
	}
	if spec.Diagram.Format != "mermaid" {
		t.Errorf("format not lowercased: %q", spec.Diagram.Format)
	}
	if spec.Diagram.Source != "graph TD; A-->B" {
		t.Errorf("source not trimmed: %q", spec.Diagram.Source)
	}
	if spec.Simulation != nil {
		t.Errorf("simulation body should be cleared for a diagram")
	}
}

func TestParseSpec_Diagram_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"missing body", `{}`, ErrDiagramMissing},
		{"unknown format", `{"diagram":{"format":"visio","source":"x"}}`, ErrUnknownFormat},
		{"empty source", `{"diagram":{"format":"dot","source":"   "}}`, ErrEmptySource},
		{"bad json", `{`, ErrBadSpecJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc().ParseSpec(KindDiagram, json.RawMessage(tc.raw))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseSpec_Diagram_SourceTooLarge(t *testing.T) {
	s := NewService(10, 0)
	raw := json.RawMessage(`{"diagram":{"format":"dot","source":"this is definitely more than ten bytes"}}`)
	if _, err := s.ParseSpec(KindDiagram, raw); !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("got %v, want ErrSourceTooLarge", err)
	}
}

func TestParseSpec_Simulation_Valid(t *testing.T) {
	raw := json.RawMessage(`{"simulation":{
		"parameters":[
			{"name":"rate","type":"Number","min":0,"max":10},
			{"name":"mode","type":"select","options":["a","b"]}
		],
		"outputs":[{"name":"result","expr":"rate * 2"}]
	}}`)
	spec, err := svc().ParseSpec(KindSimulation, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Diagram != nil {
		t.Errorf("diagram should be cleared for a simulation")
	}
	if got := spec.Simulation.Parameters[0].Type; got != "number" {
		t.Errorf("type not lowercased: %q", got)
	}
	// Label defaults to Name when omitted.
	if got := spec.Simulation.Parameters[0].Label; got != "rate" {
		t.Errorf("label default: %q", got)
	}
	if got := spec.Simulation.Outputs[0].Label; got != "result" {
		t.Errorf("output label default: %q", got)
	}
}

func TestParseSpec_Simulation_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"missing body", `{}`, ErrSimMissing},
		{"no params", `{"simulation":{"parameters":[]}}`, ErrNoParameters},
		{"no name", `{"simulation":{"parameters":[{"type":"number"}]}}`, ErrParamName},
		{"dup name", `{"simulation":{"parameters":[{"name":"x","type":"number"},{"name":"x","type":"number"}]}}`, ErrDuplicateParam},
		{"bad type", `{"simulation":{"parameters":[{"name":"x","type":"slider"}]}}`, ErrParamType},
		{"select no options", `{"simulation":{"parameters":[{"name":"x","type":"select"}]}}`, ErrSelectNoOptions},
		{"bad range", `{"simulation":{"parameters":[{"name":"x","type":"number","min":5,"max":1}]}}`, ErrNumberRange},
		{"output no name", `{"simulation":{"parameters":[{"name":"x","type":"number"}],"outputs":[{"expr":"x"}]}}`, ErrOutputName},
		{"output no expr", `{"simulation":{"parameters":[{"name":"x","type":"number"}],"outputs":[{"name":"o","expr":" "}]}}`, ErrOutputExpr},
		{"dup output", `{"simulation":{"parameters":[{"name":"x","type":"number"}],"outputs":[{"name":"o","expr":"x"},{"name":"o","expr":"x"}]}}`, ErrDuplicateOutput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc().ParseSpec(KindSimulation, json.RawMessage(tc.raw))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseSpec_TooManyParams(t *testing.T) {
	s := NewService(0, 2)
	raw := json.RawMessage(`{"simulation":{"parameters":[
		{"name":"a","type":"number"},{"name":"b","type":"number"},{"name":"c","type":"number"}]}}`)
	if _, err := s.ParseSpec(KindSimulation, raw); !errors.Is(err, ErrTooManyParams) {
		t.Fatalf("got %v, want ErrTooManyParams", err)
	}
}

func TestParseSpec_UnknownKind(t *testing.T) {
	if _, err := svc().ParseSpec(Kind("widget"), json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("got %v, want ErrUnknownKind", err)
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := svc().ParseConfig(json.RawMessage(`{"completion_interactions":3,"passing_score":0.7}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CompletionInteractions != 3 || cfg.PassingScore == nil || *cfg.PassingScore != 0.7 {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if _, err := svc().ParseConfig(json.RawMessage(`{"completion_interactions":-1}`)); !errors.Is(err, ErrNegativeInteract) {
		t.Fatalf("got %v, want ErrNegativeInteract", err)
	}
	if _, err := svc().ParseConfig(json.RawMessage(`{bad`)); !errors.Is(err, ErrBadConfigJSON) {
		t.Fatalf("got %v, want ErrBadConfigJSON", err)
	}
	// Empty config is the zero policy.
	if cfg, err := svc().ParseConfig(nil); err != nil || cfg.CompletionInteractions != 0 {
		t.Fatalf("empty config should be zero policy: %+v %v", cfg, err)
	}
}

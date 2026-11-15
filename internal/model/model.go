// Package model holds the typed result of a flate run.
package model

import "encoding/json"

// Status describes how an object differs between base and head.
type Status string

// Change statuses emitted by flate.
const (
	StatusAdded   Status = "added"
	StatusChanged Status = "changed"
	StatusRemoved Status = "removed"
)

// Ref identifies a Flux object that produced a rendered object.
type Ref struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}

// Change is one rendered object that differs between base and head.
// Old and New hold the raw manifests and may contain secret values: they must
// be masked before reaching any output sink.
type Change struct {
	Parent     *Ref            `json:"parent,omitempty"`
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Namespace  string          `json:"namespace"`
	Name       string          `json:"name"`
	Status     Status          `json:"status"`
	Old        json.RawMessage `json:"old,omitempty"`
	New        json.RawMessage `json:"new,omitempty"`
}

// Warning is a non-fatal finding flate reports on stderr, for example
// SOPSUnreadable, ExternalSource or UnrelatedFailure.
type Warning struct {
	Code      string
	Snapshot  string // "orig" or "current"; empty when flate did not say
	Kind      string
	Namespace string
	Name      string
	Message   string
	Keys      []string // key names flate lists below the warning, never values
}

// Failure is a unit flate could not render.
type Failure struct {
	Snapshot  string // "orig" or "current"
	Kind      string
	Namespace string
	Name      string
	Reason    string
}

// BrokenUnit is a unit that fails identically in base and head. It has no
// reason on purpose: the error text of such a unit cannot be masked, because
// the diff holds no objects whose secret values could be looked up, so it is
// never read into the model.
type BrokenUnit struct {
	Kind      string
	Namespace string
	Name      string
	// Blocks lists the units ("namespace/name") that cannot render because
	// this one failed.
	Blocks []string
}

// Bounds for Result.Unparsed.
const (
	MaxUnparsedLines      = 50
	MaxUnparsedLineLength = 300
)

// Result is the complete outcome of one flate run.
type Result struct {
	Changes    []Change
	Suppressed []Change
	Warnings   []Warning
	Failures   []Failure
	// BrokenInBothSides holds units that fail when flate builds the head and
	// that flate's diff does not mention: they are broken identically in base
	// and head, so no change to them can be shown.
	BrokenInBothSides []BrokenUnit
	// Unparsed holds non-blank stderr lines that matched no known shape, plus
	// notes when flate's reported counts differ from what was parsed. It keeps
	// partial results from looking clean. Lines are bounded (MaxUnparsedLines,
	// MaxUnparsedLineLength) but are raw flate output: mask them before any
	// output sink.
	Unparsed []string
	ExitCode int
}

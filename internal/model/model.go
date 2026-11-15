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

// Result is the complete outcome of one flate run.
type Result struct {
	Changes    []Change
	Suppressed []Change
	Warnings   []Warning
	Failures   []Failure
	ExitCode   int
}

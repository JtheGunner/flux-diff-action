package main

import (
	"bytes"
	"testing"
)

func TestRootCommand_VersionFlag(t *testing.T) {
	var out bytes.Buffer

	cmd := newRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute --version: %v", err)
	}

	if got, want := out.String(), "flux-diff dev\n"; got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

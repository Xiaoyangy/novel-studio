package main

import "testing"

func TestParseSimulateFlagsNoDiag(t *testing.T) {
	flags, extra, err := parseSimulateFlags("simulate", []string{"--no-diag"})
	if err != nil {
		t.Fatalf("parseSimulateFlags: %v", err)
	}
	if !flags.NoDiag {
		t.Fatal("expected --no-diag to be set")
	}
	if len(extra) != 0 {
		t.Fatalf("expected no positional args, got %v", extra)
	}
}

func TestParseImportSimFlagsNoDiagAndPath(t *testing.T) {
	flags, extra, err := parseSimulateFlags("import-sim", []string{"--no-diag", "profile.json"})
	if err != nil {
		t.Fatalf("parseSimulateFlags: %v", err)
	}
	if !flags.NoDiag {
		t.Fatal("expected --no-diag to be set")
	}
	if len(extra) != 1 || extra[0] != "profile.json" {
		t.Fatalf("expected profile path positional arg, got %v", extra)
	}
}

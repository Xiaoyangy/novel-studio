package main

import (
	"slices"
	"testing"
)

func TestParseCLIOptionsRemovesGlobalFlagsBeforeSubcommandRouting(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--config", "/tmp/novel-config.json", "--writing-assets", "seed-defaults"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if opts.ConfigPath != "/tmp/novel-config.json" {
		t.Fatalf("expected config path to be consumed, got %q", opts.ConfigPath)
	}
	want := []string{"--writing-assets", "seed-defaults"}
	if !slices.Equal(args, want) {
		t.Fatalf("expected routed args %v, got %v", want, args)
	}
}

func TestParseCLIOptionsKeepsHelpTokenForSubcommandUsage(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--config", "/tmp/novel-config.json", "--writing-assets", "--help"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if !opts.Help {
		t.Fatal("expected help marker to be recorded")
	}
	if !hasAnySubcommand(args) {
		t.Fatalf("expected parsed args to retain subcommand token, got %v", args)
	}
	want := []string{"--writing-assets", "--help"}
	if !slices.Equal(args, want) {
		t.Fatalf("expected routed help args %v, got %v", want, args)
	}
}

func TestStripRoutingTokensPreservesSubcommandFlags(t *testing.T) {
	got := stripRoutingTokens(
		[]string{"--review-existing", "--from", "drafts", "--to", "reviews", "--budget", "2"},
		"--review-existing",
	)
	want := []string{"--from", "drafts", "--to", "reviews", "--budget", "2"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected stripped args %v, got %v", want, got)
	}
}

func TestHasAnySubcommandRecognizesZeroInit(t *testing.T) {
	if !hasAnySubcommand([]string{"--zero-init", "--dir", "/tmp/book/output/novel"}) {
		t.Fatal("expected --zero-init to route as a subcommand")
	}
}

package version

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrint(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, Info{Version: "1.2.3", Commit: "abc123", Date: "2026-06-20"})
	got := buf.String()
	for _, want := range []string{"novel-studio v1.2.3", "commit: abc123", "built: 2026-06-20"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Print missing %q in %q", want, got)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":        "dev",
		"(devel)": "dev",
		"dev":     "dev",
		"1.2.3":   "v1.2.3",
		"v1.2.3":  "v1.2.3",
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

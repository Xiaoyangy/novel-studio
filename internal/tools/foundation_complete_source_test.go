package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFoundationCompleteSourceIsExplicitAndBounded(t *testing.T) {
	st := foundationSourceTestStore(t, true)
	content := []byte(`{"text":"` + strings.Repeat("源", 67053) + `"}`)
	path := filepath.Join(st.Dir(), "characters.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewFoundationSourceContextTool(st, "characters")
	if raw, err := tool.Execute(t.Context(), json.RawMessage(`{}`)); err == nil || len(raw) != 0 {
		t.Fatal("ordinary 45000-rune limit changed")
	}
	if raw, err := tool.Execute(t.Context(), json.RawMessage(`{"complete_source":true}`)); err == nil || len(raw) != 0 {
		t.Fatal("model self-enabled host capability")
	}
	result, err := tool.WithCompleteSource(true).Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(result, &packet); err != nil || packet.Truncated || !bytes.Equal([]byte(packet.Content), content) {
		t.Fatalf("complete source changed: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"text":"`+strings.Repeat("a", foundationCompleteSourceMaxBytes)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := tool.Execute(t.Context(), json.RawMessage(`{}`)); err == nil || len(raw) != 0 {
		t.Fatal("complete source bypassed absolute byte limit")
	}
}

func TestFoundationCompleteSourcePreservesLeaseAndSourceGuards(t *testing.T) {
	for _, mode := range []string{"absent-lease", "invalid-json", "invalid-utf8", "unlisted", "linked-source"} {
		t.Run(mode, func(t *testing.T) {
			st := foundationSourceTestStore(t, mode != "absent-lease")
			content := []byte(`{"source":"original"}`)
			path := filepath.Join(st.Dir(), "characters.json")
			args := json.RawMessage(`{}`)
			switch mode {
			case "invalid-json":
				content = []byte(`{"text":`)
			case "invalid-utf8":
				content = []byte{'"', 0xff, '"'}
			case "unlisted":
				args = json.RawMessage(`{"source":"memory.md"}`)
			}
			if mode == "linked-source" {
				other := filepath.Join(t.TempDir(), "source.json")
				if err := os.WriteFile(other, content, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			leasePath := filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json")
			before, _ := os.ReadFile(leasePath)
			if raw, err := NewFoundationSourceContextTool(st, "characters").WithCompleteSource(true).Execute(t.Context(), args); err == nil || len(raw) != 0 {
				t.Fatal("complete capability weakened source guards")
			}
			after, _ := os.ReadFile(leasePath)
			if !bytes.Equal(before, after) {
				t.Fatal("read-only rejection changed lease")
			}
		})
	}
}

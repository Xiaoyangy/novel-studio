package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func foundationSourceTestStore(t *testing.T, lease bool) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if lease {
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "exact-source-test"}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestFoundationSourceContextReturnsOneExactRawSourceAndNoMemory(t *testing.T) {
	st := foundationSourceTestStore(t, true)
	characters := "[\r\n  {\"name\":\"林澄\",\"original\":\"\\u4e00\"}\r\n]\r\n"
	codex := "{\n  \"mechanisms\": [\"共同核验\"]\n}\n"
	for source, raw := range map[string]string{"characters.json": characters, "world_codex.json": codex, "premise.md": "前提\r\n保留尾换行\n", "meta/compass.json": "{\"direction\":\"保留\"}"} {
		if err := os.WriteFile(filepath.Join(st.Dir(), source), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "memory.md"), []byte("PRIVATE_WRITING_MEMORY"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewFoundationSourceContextTool(st, "characters")
	for _, tc := range []struct{ args, source, content string }{
		{`{}`, "characters.json", characters},
		{`{"chapter":1,"profile":"planning"}`, "characters.json", characters},
		{`{"source":"world_codex.json"}`, "world_codex.json", codex},
		{`{"source":"premise"}`, "premise.md", "前提\r\n保留尾换行\n"},
		{`{"source":"compass"}`, "meta/compass.json", "{\"direction\":\"保留\"}"},
	} {
		raw, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		var packet struct {
			Version      string `json:"version"`
			Source       string `json:"source"`
			SourceSHA256 string `json:"source_sha256"`
			Content      string `json:"content"`
			Truncated    bool   `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &packet); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(tc.content))
		if packet.Version != FoundationSourceContextVersion || packet.Source != tc.source || packet.Content != tc.content || packet.Truncated || packet.SourceSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
			t.Fatalf("source was normalized, truncated or misbound: %+v", packet)
		}
		if strings.Contains(string(raw), "PRIVATE_WRITING_MEMORY") || (tc.source != "world_codex.json" && strings.Contains(string(raw), "共同核验")) {
			t.Fatal("exact source packet included unrelated context")
		}
	}
}

func TestFoundationSourceContextRequiresOwnLeaseWithoutCleanupWrites(t *testing.T) {
	for _, kind := range []string{"absent", "expired", "foreign_process", "wrong_mode", "wrong_chapter", "bad_json"} {
		t.Run(kind, func(t *testing.T) {
			st := foundationSourceTestStore(t, kind != "absent")
			leasePath := filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json")
			before, _ := os.ReadFile(leasePath)
			if kind != "absent" {
				var lease domain.PipelineExecutionLock
				if err := json.Unmarshal(before, &lease); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "expired":
					lease.ExpiresAt = time.Now().Add(-time.Minute)
				case "foreign_process":
					lease.ProcessID = os.Getpid() + 100000
				case "wrong_mode":
					lease.Mode = domain.PipelineExecutionProjectAll
				case "wrong_chapter":
					lease.TargetChapter = 2
				}
				before, _ = json.Marshal(lease)
				if kind == "bad_json" {
					before = []byte("{")
				}
				if err := os.WriteFile(leasePath, before, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if raw, err := NewFoundationSourceContextTool(st, "characters").Execute(context.Background(), json.RawMessage(`{}`)); err == nil || len(raw) != 0 {
				t.Fatalf("invalid lease returned a source packet: %s %v", raw, err)
			}
			after, _ := os.ReadFile(leasePath)
			if string(before) != string(after) {
				t.Fatal("read-only rejection rewrote or cleaned up the lease")
			}
		})
	}
}

func TestFoundationSourceContextKeepsLargeMiddleAndRejectsLinkedRoot(t *testing.T) {
	st := foundationSourceTestStore(t, true)
	content := `{"start":"开头","body":"` + strings.Repeat("前", 21000) + "MIDDLE_EXACT_RESOURCE" + strings.Repeat("后", 21000) + `","end":"结尾"}`
	if err := os.WriteFile(filepath.Join(st.Dir(), "characters.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := NewFoundationSourceContextTool(st, "characters").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil || packet.Content != content {
		t.Fatalf("large source lost exact middle or boundaries: %v", err)
	}
	link := filepath.Join(t.TempDir(), "linked-book")
	if err := os.Symlink(st.Dir(), link); err != nil {
		t.Fatal(err)
	}
	if raw, err := NewFoundationSourceContextTool(store.NewStore(link), "characters").Execute(context.Background(), json.RawMessage(`{}`)); err == nil || len(raw) != 0 {
		t.Fatal("linked root was accepted")
	}
}

func TestFoundationSourceContextRejectsTraversalLinksMalformedAndOversize(t *testing.T) {
	for _, kind := range []string{"outside_path", "unlisted", "extra_args", "other_chapter", "draft_profile", "null", "trailing_json", "empty", "invalid_utf8", "invalid_json", "symlink_file", "symlink_parent", "directory", "oversize_runes", "oversize_bytes"} {
		t.Run(kind, func(t *testing.T) {
			st := foundationSourceTestStore(t, true)
			path := filepath.Join(st.Dir(), "characters.json")
			content := []byte(`[{"name":"原始人物"}]`)
			args := json.RawMessage(`{}`)
			switch kind {
			case "outside_path":
				args = json.RawMessage(`{"source":"../characters.json"}`)
			case "unlisted":
				args = json.RawMessage(`{"source":"meta/character_agents/registry.json"}`)
			case "extra_args":
				args = json.RawMessage(`{"source":"characters.json","offset":2}`)
			case "other_chapter":
				args = json.RawMessage(`{"chapter":2}`)
			case "draft_profile":
				args = json.RawMessage(`{"profile":"draft"}`)
			case "null":
				args = json.RawMessage(`null`)
			case "trailing_json":
				args = json.RawMessage(`{} {}`)
			case "empty":
				content = nil
			case "invalid_utf8":
				content = []byte{'"', 0xff, '"'}
			case "invalid_json":
				content = []byte(`[{"name":`)
			case "oversize_runes":
				content = []byte(`{"text":"` + strings.Repeat("文", foundationSourceMaxRunes) + `"}`)
			case "oversize_bytes":
				content = []byte(strings.Repeat(" ", foundationSourceMaxRunes*utf8.UTFMax+1))
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if kind == "symlink_file" {
				outside := filepath.Join(t.TempDir(), "source.json")
				if err := os.WriteFile(outside, content, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			} else if kind == "symlink_parent" {
				// A link to an in-root directory is forbidden too, not only escape.
				if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "compass.json"), content, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(st.Dir(), "meta"), filepath.Join(st.Dir(), "actual-meta")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(st.Dir(), "actual-meta"), filepath.Join(st.Dir(), "meta")); err != nil {
					t.Fatal(err)
				}
				args = json.RawMessage(`{"source":"meta/compass.json"}`)
			} else if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if raw, err := NewFoundationSourceContextTool(st, "characters").Execute(context.Background(), args); err == nil || len(raw) != 0 {
				t.Fatalf("unsafe or incomplete source returned: %s %v", raw, err)
			}
		})
	}
}

func TestFoundationSourceContextSchemaCompatibilityDoesNotBroadenScope(t *testing.T) {
	tool := NewFoundationSourceContextTool(nil, "characters")
	properties := tool.Schema()["properties"].(map[string]any)
	chapter := properties["chapter"].(map[string]any)
	if chapter["minimum"] != 1 || chapter["maximum"] != 1 {
		t.Fatalf("legacy chapter compatibility broadened the target: %v", chapter)
	}
	profile := properties["profile"].(map[string]any)
	values, ok := profile["enum"].([]string)
	if !ok || len(values) != 1 || values[0] != "planning" {
		t.Fatalf("legacy profile compatibility exposed unrelated context: %v", profile)
	}
	if _, ok := properties["source"]; !ok || len(properties) != 3 || tool.Name() != "novel_context" {
		t.Fatal("exact tool schema is not the restricted compatible context API")
	}
}

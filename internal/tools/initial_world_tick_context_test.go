package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestInitialWorldTickExactContextRequiresCurrentUnchangedLease(t *testing.T) {
	for _, kind := range []string{"expired", "foreign-process", "wrong-chapter"} {
		t.Run(kind, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionWorldTick, TargetChapter: 1, Owner: "lease-test"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var lock domain.PipelineExecutionLock
			if err := json.Unmarshal(raw, &lock); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "expired":
				lock.ExpiresAt = time.Now().Add(-time.Minute)
			case "foreign-process":
				lock.ProcessID = os.Getpid() + 100000
			case "wrong-chapter":
				lock.TargetChapter = 2
			}
			raw, _ = json.Marshal(lock)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if data, err := NewContextTool(st, References{}, "default").Execute(t.Context(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`)); err == nil || len(data) > 0 {
				t.Fatal("invalid lease enabled exact or ordinary context")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(raw) {
				t.Fatal("diagnostic removed or rewrote invalid lease")
			}
		})
	}
}

func TestInitialWorldTickExactContextRealBookCopy(t *testing.T) {
	dir := os.Getenv("NOVEL_TICK_CONTEXT_COPY")
	if dir == "" {
		t.Skip("requires disposable book copy")
	}
	st := store.NewStore(dir)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionWorldTick, TargetChapter: 1, Owner: "read-only-source-probe"}); err != nil {
		t.Fatal(err)
	}
	defer st.Runtime.ReleasePipelineExecution("read-only-source-probe")
	raw, err := NewContextTool(st, References{}, "default").Execute(t.Context(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`))
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Version          string                            `json:"version"`
		Sources          map[string]initialWorldTickSource `json:"sources"`
		DispatchContract string                            `json:"dispatch_contract"`
		ChapterOne       domain.OutlineEntry               `json:"chapter_one"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	if packet.Version != InitialWorldTickContextVersion || len(packet.Sources) != 6 {
		t.Fatal("wrong source packet")
	}
	for path, source := range packet.Sources {
		original, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || source.Content != string(original) {
			t.Fatalf("source changed or lost: %s %v", path, err)
		}
		t.Logf("exact source=%s bytes=%d", path, len(original))
	}
	contract, err := BuildInitialWorldTickDispatchContract(st)
	if err != nil || packet.DispatchContract != contract.Block {
		t.Fatal("chapter-one boundary lost")
	}
	if packet.ChapterOne.Chapter != 1 || packet.ChapterOne.CoreEvent != contract.CoreEvent || packet.ChapterOne.Hook != contract.Hook || len(packet.ChapterOne.Scenes) == 0 {
		t.Fatal("complete chapter-one fields missing")
	}
	if _, err := NewContextTool(st, References{}, "default").Execute(t.Context(), json.RawMessage(`{"chapter":2,"profile":"world_simulation"}`)); err == nil {
		t.Fatal("wrong chapter accepted under initial lease")
	}
	t.Logf("complete_source_packet_bytes=%d legacy_world_sim_budget_unchanged=%d", len(raw), contextBudget(1, "world_simulation"))
}

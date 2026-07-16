package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestDraftWriteIntentRecoveryPreservesBoundedStructuralBudget(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	tool := NewDraftChapterTool(st)
	for i, destination := range []string{"第一家", "第二家", "第三家"} {
		prior, err := st.Drafts.LoadDraft(1)
		if err != nil {
			t.Fatal(err)
		}
		candidate := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到"+destination+"。", 100)
		if err := beginDraftWriteIntent(st, 1, prior, candidate, "write", nil); err != nil {
			t.Fatal(err)
		}
		// Simulate a crash after the atomic Markdown replace but before any
		// checkpoint, metrics or local rerender marker was written.
		if err := st.Drafts.SaveDraft(1, candidate); err != nil {
			t.Fatal(err)
		}
		if err := tool.recoverDraftWriteIntent(1); err != nil {
			t.Fatalf("recover attempt %d: %v", i+1, err)
		}
		if _, err := os.Stat(draftWriteIntentPath(st.Dir(), 1)); !os.IsNotExist(err) {
			t.Fatalf("recovered intent %d was not cleared: %v", i+1, err)
		}
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); !escalation.Required || escalation.Attempts != 3 {
		t.Fatalf("crash-recovered structural attempts escaped the bounded budget: %+v", escalation)
	}
	fourth := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到第四家。", 100)
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": fourth, "mode": "write"})
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "render-only") {
		t.Fatalf("a fourth write bypassed recovered structural attempts: %v", err)
	}
}

func TestDraftWriteIntentRecoveryCancelsUnappliedAtomicReplace(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	prior := "第一章\n\n原草稿。"
	if err := st.Drafts.SaveDraft(1, prior); err != nil {
		t.Fatal(err)
	}
	candidate := "第一章\n\n尚未真正写入的新稿。"
	if err := beginDraftWriteIntent(st, 1, prior, candidate, "write", nil); err != nil {
		t.Fatal(err)
	}
	if err := NewDraftChapterTool(st).recoverDraftWriteIntent(1); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Drafts.LoadDraft(1); err != nil || got != prior {
		t.Fatalf("unapplied intent changed prior bytes: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(draftWriteIntentPath(st.Dir(), 1)); !os.IsNotExist(err) {
		t.Fatalf("unapplied intent was not cleared: %v", err)
	}
}

func TestDraftGateInspectionRecoversCleanCrashWrittenCandidate(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	candidate := "第一章\n\n林澈推开门，雨刚停，桌边的人都回头看了他一眼。"
	if err := beginDraftWriteIntent(st, 1, "", candidate, "write", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, candidate); err != nil {
		t.Fatal(err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft"); cp != nil {
		t.Fatalf("fixture unexpectedly checkpointed before recovery: %+v", cp)
	}
	inspection, err := InspectDraftExternalGate(st.Dir(), 1)
	if err != nil || inspection.CurrentBodySHA256 != reviewreport.BodySHA256(candidate) {
		t.Fatalf("gate inspection did not recover the clean candidate: inspection=%+v err=%v", inspection, err)
	}
	refreshed := store.NewStore(st.Dir())
	if cp := refreshed.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft"); cp == nil || cp.Digest != "sha256:"+reviewreport.BodySHA256(candidate) {
		t.Fatalf("clean crash-written candidate remained unbound after inspection: %+v", cp)
	}
	if _, err := os.Stat(draftWriteIntentPath(st.Dir(), 1)); !os.IsNotExist(err) {
		t.Fatalf("inspection did not clear recovered intent: %v", err)
	}
}

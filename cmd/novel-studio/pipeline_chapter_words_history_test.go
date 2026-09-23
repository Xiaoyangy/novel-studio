package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineReconcileCurrentChapterWordsOverSupersededHistory(t *testing.T) {
	prompt := "种子中的旧30万字、100章及每章2700–3300字属于已被替代的篇幅方案，不是当前预算。\n当前篇幅方案：110-110章，全书正文500000-520000字。每章通常4500–5000字。"
	if path := os.Getenv("NOVEL_CHAPTER_WORDS_PROMPT_READONLY"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prompt = string(data)
	}
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2700, Max: 3300}}}); err != nil {
		t.Fatal(err)
	}
	changed, err := pipelineReconcileExplicitPromptChapterWords(st, prompt)
	if err != nil || !changed {
		t.Fatalf("current prompt must repair historical range: changed=%v err=%v", changed, err)
	}
	snapshot, err := st.UserRules.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Structured.ChapterWords; got == nil || got.Min != 4500 || got.Max != 5000 {
		t.Fatalf("wrong live contract: %+v", got)
	}
	changed, err = pipelineReconcileExplicitPromptChapterWords(st, prompt)
	if err != nil || changed {
		t.Fatalf("recovery must be idempotent: changed=%v err=%v", changed, err)
	}
}

func TestPipelineChapterWordRecoveryRegeneratesStaleZeroInit(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	snapshot, err := st.UserRules.Load()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Structured.ChapterWords = &rules.WordRange{Min: 2700, Max: 3300}
	if err := st.UserRules.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	args := append(pipelineZeroInitRegenerationArgs(dir), "--rebuild-rag=false")
	if err := zeroInitPipeline(cliOptions{}, args); err != nil {
		t.Fatal(err)
	}
	if ok, reason := pipelineCurrentZeroInitReadinessState(dir); !ok {
		t.Fatalf("initial fixture not ready: %s", reason)
	}
	prompt := "旧每章2700-3300，现每章4500-5000。"
	statePath := filepath.Join(dir, "meta", "pipeline.json")
	previous := &domain.PipelineState{Stages: []string{"outline-all", "zero-init", "preplan"}, Completed: []string{"outline-all"}, Prompt: prompt, InputDigest: "sha256:same-model-and-prompts", RunIdentity: "sha256:same-flags"}
	if err := savePipelineState(statePath, previous); err != nil {
		t.Fatal(err)
	}
	changed, err := pipelineReconcileExplicitPromptChapterWords(st, prompt)
	if err != nil || !changed {
		t.Fatalf("reconcile: changed=%v err=%v", changed, err)
	}
	if ok, _ := pipelineCurrentZeroInitReadinessState(dir); ok {
		t.Fatal("changed word contract must invalidate old ready receipt")
	}
	if issues := zeroCheckPacingWordContract(dir); len(issues) != 1 || !strings.Contains(issues[0], "4500-5000") {
		t.Fatalf("stale pacing must be detected: %v", issues)
	}
	resumed, err := loadOrInitPipelineState(statePath, previous.Stages, prompt, previous.InputDigest, previous.RunIdentity, false)
	if err != nil || !reflect.DeepEqual(resumed.Completed, previous.Completed) {
		t.Fatalf("same prompt recovery lost completed outline: state=%+v err=%v", resumed, err)
	}
	if err := zeroInitPipeline(cliOptions{}, args); err != nil {
		t.Fatalf("normal zero-init regeneration: %v", err)
	}
	if ok, reason := pipelineCurrentZeroInitReadinessState(dir); !ok {
		t.Fatalf("regenerated zero-init not ready: %s", reason)
	}
	contract, err := st.Methodology.LoadPacingContract()
	if err != nil || contract == nil || contract.ChapterWordMin != 4500 || contract.ChapterWordMax != 5000 {
		t.Fatalf("pacing not regenerated: %+v err=%v", contract, err)
	}
}

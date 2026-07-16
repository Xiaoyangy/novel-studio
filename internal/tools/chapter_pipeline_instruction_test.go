package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func checkpointChapterInstruction(t *testing.T, st *store.Store, chapter int, instruction string, persistText bool) string {
	t.Helper()
	instruction = strings.TrimSpace(instruction)
	sum := sha256.Sum256([]byte(instruction))
	digest := hex.EncodeToString(sum[:])
	request := domain.ChapterRerenderRequest{
		Version: 1, Chapter: chapter, InstructionSHA256: digest, Reason: "test",
	}
	if persistText {
		request.Instruction = instruction
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.rerender_request.json", chapter)))
	path := filepath.Join(st.Dir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "rerender-request", rel); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestChapterPipelineInstructionPersistsAcrossContextProfilesAndBindsSources(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	digest := checkpointChapterInstruction(t, st, 1, "林澈先断电，沈知遥后确认。", true)
	instruction, err := loadChapterPipelineInstruction(st, 1)
	if err != nil || instruction == nil {
		t.Fatalf("load instruction: value=%+v err=%v", instruction, err)
	}
	if instruction.SHA256 != digest || instruction.Token != chapterPipelineInstructionTokenPrefix+digest {
		t.Fatalf("unexpected instruction identity: %+v", instruction)
	}

	tool := NewContextTool(st, References{}, "default")
	for _, profile := range []string{"world_simulation", "planning", "draft"} {
		payload := map[string]any{}
		if err := tool.addChapterPipelineInstructionContext(payload, 1); err != nil {
			t.Fatal(err)
		}
		applyChapterContextProfile(payload, profile)
		contract, ok := payload["chapter_pipeline_instruction"].(map[string]any)
		if !ok || contract["source_token"] != instruction.Token || !strings.Contains(contract["instruction"].(string), "先断电") {
			t.Fatalf("profile %s lost exact instruction: %#v", profile, payload)
		}
	}

	sim := domain.ChapterWorldSimulation{Chapter: 1}
	gap := chapterPipelineInstructionGap(st, sim)
	if !strings.Contains(gap, instruction.Token) || !rerenderMayIgnoreSourceVersionGap(gap) {
		t.Fatalf("instruction freshness gap not exposed as render-only-compatible: %q", gap)
	}
	sim.Sources = []string{instruction.Token}
	if gap := chapterPipelineInstructionGap(st, sim); gap != "" {
		t.Fatalf("exact source token did not satisfy instruction binding: %q", gap)
	}
	merged := map[string]any{}
	applyPlanDetailsSourceAnchors(st, 1, merged, &sim, nil)
	if !contextSourcesContain(stringSliceFromAny(merged["context_sources"]), instruction.Token) {
		t.Fatalf("POV plan did not inherit instruction token: %#v", merged)
	}
}

func TestLegacyChapterPipelineInstructionTracksCurrentPipelinePrompt(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	instruction := "皮卡仍为 pending，不得提前答应。"
	checkpointChapterInstruction(t, st, 1, instruction, false)
	stateRaw, _ := json.Marshal(map[string]any{"prompt": instruction})
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "pipeline.json"), stateRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadChapterPipelineInstruction(st, 1)
	if err != nil || loaded == nil || loaded.Text != instruction {
		t.Fatalf("legacy instruction was not recovered: value=%+v err=%v", loaded, err)
	}
	oldToken := loaded.Token
	stateRaw, _ = json.Marshal(map[string]any{"prompt": "另一轮已变化的要求"})
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "pipeline.json"), stateRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadChapterPipelineInstruction(st, 1)
	if err != nil || loaded == nil || loaded.Text != "另一轮已变化的要求" || loaded.Artifact != "meta/pipeline.json#prompt" {
		t.Fatalf("new current pipeline prompt did not supersede stale request SHA: value=%+v err=%v", loaded, err)
	}
	if loaded.Token == oldToken {
		t.Fatalf("changed current prompt must create a fresh instruction identity: old=%s new=%s", oldToken, loaded.Token)
	}
	if gap := chapterPipelineInstructionGap(st, domain.ChapterWorldSimulation{Chapter: 1, Sources: []string{oldToken}}); !strings.Contains(gap, loaded.Token) {
		t.Fatalf("old simulation source token did not become stale: %q", gap)
	}
}

func TestChapterPipelineInstructionFailsClosedOnArtifactDrift(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	checkpointChapterInstruction(t, st, 1, "原始硬合同", true)
	path := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(path, []byte(`{"chapter":1,"instruction":"已篡改"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadChapterPipelineInstruction(st, 1); err == nil || !strings.Contains(err.Error(), "偏离 checkpoint") {
		t.Fatalf("drifted instruction artifact should fail closed, got %v", err)
	}
}

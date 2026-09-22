package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

// Construct only synthetic immutable archive evidence in t.TempDir. This test
// neither invokes rebase nor reads any production novel/source/process.
func hostLeafCASRebasedFixture(t *testing.T) (cliOptions, string) {
	t.Helper()
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	chars, err := st.Characters.Load()
	publicationArtifactMust(t, err)
	chars[0].InitialState = &domain.CharacterInitialState{Location: "公开窗口", CurrentGoal: "处理本人已核实的资料", CurrentAction: "旧动作", Pressure: "旧压力", KnownFacts: []string{"知道本人的职责", "旧知识边界"}, Relationships: []string{"旧关系边界"}}
	publicationArtifactMust(t, st.Characters.Save(chars))
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	if progress == nil {
		progress = &domain.Progress{Phase: domain.PhaseOutline}
	}
	progress.GenerationID = "old-synthetic-epoch"
	progress.TotalChapters = 100
	progress.CurrentChapter = 0
	progress.InProgressChapter = 0
	publicationArtifactMust(t, st.Progress.Save(progress))
	publicationArtifactMust(t, savePipelineState(filepath.Join(live, "meta/pipeline.json"), &domain.PipelineState{Prompt: "合成测试的原始作者合同", Stages: []string{"architect", "outline-all"}}))
	archive := filepath.Join(pipelineRebaseRunRoot(live), "archives", "sealed-rebase-leaf-cas-fixture", "output", "novel")
	publicationArtifactMust(t, copyPipelineRenderCandidateTree(live, archive))
	root, err := store.DirectoryContentRoot(archive)
	publicationArtifactMust(t, err)
	progress.GenerationID = "new-synthetic-epoch"
	progress.GenerationMode = domain.GenerationModeSimulationRestartFromSeed
	publicationArtifactMust(t, st.Progress.Save(progress))
	receipt := pipelineAllChapterRebaseReceipt{Version: "pipeline-all-chapter-rebase.v1", SourceOutput: live, SourceRoot: root, ArchiveOutput: archive, ArchiveRoot: root, PreviousProgress: filepath.Join(archive, "meta/progress.json"), NewGenerationID: progress.GenerationID, RebasedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err = writePipelinePlanningJSON(filepath.Join(live, "meta/all_chapter_rebase.json"), receipt)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.ValidateRebasedChapterZeroFoundationRefresh())
	config := filepath.Join(t.TempDir(), "config.json")
	publicationArtifactMust(t, os.WriteFile(config, []byte(`{"provider":"ollama","model":"never-called","providers":{"ollama":{"type":"openai","base_url":"http://127.0.0.1:1/v1"}}}`), 0600))
	return cliOptions{ConfigPath: config, Dir: pipelineRebaseRunRoot(live)}, live
}

func TestFoundationHostLeafCASChangesOnlyAuthorizedLeavesWithoutModel(t *testing.T) {
	opts, live := hostLeafCASRebasedFixture(t)
	st := store.NewStore(live)
	chars, err := st.Characters.Load()
	publicationArtifactMust(t, err)
	before, err := os.ReadFile(filepath.Join(live, "characters.json"))
	publicationArtifactMust(t, err)
	protected := map[string][]byte{}
	for _, rel := range pipelineFoundationRepairProtected {
		if rel != "characters.json" && rel != "meta/pipeline.json" && rel != "meta/pipeline_timings.jsonl" {
			protected[rel], _ = os.ReadFile(filepath.Join(live, rel))
		}
	}
	oldJournal, _ := os.ReadFile(filepath.Join(live, store.UsageAuditPath))
	manifest := map[string]any{
		"version": pipelineFoundationRepairVersion, "mode": "host-character-leaf-cas.v1", "target": "characters",
		"instruction":            "仅替换明确授权叶；不得调用模型、改写其余来源或执行rebase。",
		"expected_source_digest": pipelineFoundationRepairSHA(before),
		"leaf_changes": []map[string]any{
			{"character": chars[0].Name, "pointer": "/0/initial_state/known_facts/1", "old": "旧知识边界", "new": "仅持有当前已核实的知识"},
			{"character": chars[0].Name, "pointer": "/0/initial_state/current_action", "old": "旧动作", "new": "处理已到达窗口的请求"},
			{"character": chars[0].Name, "pointer": "/0/initial_state/pressure", "old": "旧压力", "new": "本班时间有限"},
			{"character": chars[0].Name, "pointer": "/0/initial_state/relationships/0", "old": "旧关系边界", "new": "依照本人的现有职责联系"},
		},
	}
	raw, err := json.Marshal(manifest)
	publicationArtifactMust(t, err)
	path := filepath.Join(t.TempDir(), "explicit-leaf-cas.json")
	publicationArtifactMust(t, os.WriteFile(path, raw, 0600))
	original := pipelineFoundationRepairHost
	t.Cleanup(func() { pipelineFoundationRepairHost = original })
	calls := 0
	pipelineFoundationRepairHost = func(bootstrap.Config, assets.Bundle, headless.Options) error {
		calls++
		return fmt.Errorf("host leaf CAS must not invoke a model")
	}
	err = runPipelineFoundationRepair(opts, pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "characters", ArchitectRepairFile: path}, "")
	if err != nil {
		t.Fatalf("verified chapter-zero exact leaf CAS was not available: %v (model_calls=%d)", err, calls)
	}
	if calls != 0 {
		t.Fatalf("host CAS dispatched %d model calls", calls)
	}
	got, err := store.NewStore(live).Characters.Load()
	publicationArtifactMust(t, err)
	if got[0].InitialState.KnownFacts[1] != "仅持有当前已核实的知识" || got[0].InitialState.CurrentAction != "处理已到达窗口的请求" || got[0].InitialState.Pressure != "本班时间有限" || got[0].InitialState.Relationships[0] != "依照本人的现有职责联系" {
		t.Fatal("authorized leaves not replaced")
	}
	chars[0].InitialState.KnownFacts[1] = "仅持有当前已核实的知识"
	chars[0].InitialState.CurrentAction = "处理已到达窗口的请求"
	chars[0].InitialState.Pressure = "本班时间有限"
	chars[0].InitialState.Relationships[0] = "依照本人的现有职责联系"
	if !reflect.DeepEqual(got, chars) {
		t.Fatal("unapproved character values changed")
	}
	for rel, want := range protected {
		actual, _ := os.ReadFile(filepath.Join(live, rel))
		if !bytes.Equal(actual, want) {
			t.Fatalf("protected source changed: %s", rel)
		}
	}
	newJournal, _ := os.ReadFile(filepath.Join(live, store.UsageAuditPath))
	if !bytes.Equal(oldJournal, newJournal) {
		t.Fatal("host CAS created or changed model usage")
	}
	digest, err := tools.FoundationRefreshArtifactsDigest(live, "characters")
	publicationArtifactMust(t, err)
	checkpoint := store.NewStore(live).Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("characters"))
	if checkpoint == nil || checkpoint.Digest != digest {
		t.Fatal("real source epoch/checkpoint missing")
	}
	_, manifestDigest, err := loadPipelineFoundationRepairManifest(path)
	publicationArtifactMust(t, err)
	var audit struct {
		ExecutionMode string `json:"execution_mode"`
		ModelCalls    *int   `json:"model_calls"`
		SourceBefore  string `json:"source_before"`
		SourceAfter   string `json:"source_after"`
	}
	publicationArtifactMust(t, readPipelinePlanningJSON(filepath.Join(live, "meta/foundation_repairs", strings.TrimPrefix(manifestDigest, "sha256:")+".json"), &audit))
	after, _ := os.ReadFile(filepath.Join(live, "characters.json"))
	if audit.ExecutionMode != "host_leaf_cas" || audit.ModelCalls == nil || *audit.ModelCalls != 0 || audit.SourceBefore != pipelineFoundationRepairSHA(before) || audit.SourceAfter != pipelineFoundationRepairSHA(after) {
		t.Fatalf("host audit is not bound to actual zero-model mutation: %+v", audit)
	}
}

func TestFoundationHostLeafCASWhitelistAndExactOldValue(t *testing.T) {
	before := []byte(`[{"name":"甲","initial_state":{"known_facts":["A","B"],"current_action":"old","pressure":"old","relationships":["old"],"resource_balances":[{"resource_id":"res_1111111111111111","actual_amount":9007199254740993}]},"future_unknown":{"amount":1.2300}}]`)
	base := pipelineFoundationRepairManifest{Version: pipelineFoundationRepairVersion, Mode: pipelineFoundationLeafCASMode, Target: "characters", Instruction: "只改既存字符串", ExpectedSourceDigest: pipelineFoundationRepairSHA(before), LeafChanges: []pipelineFoundationLeafChange{{Character: "甲", Pointer: "/0/initial_state/known_facts/1", Old: "B", New: "C"}}}
	for _, name := range []string{"source-sha", "character", "old", "index", "leading-zero", "array-whole", "resource", "identity", "unknown-field", "bool-field", "duplicate", "missing-mode", "unknown-mode", "mixed-resource-authority", "noop"} {
		t.Run(name, func(t *testing.T) {
			m := base
			m.LeafChanges = append([]pipelineFoundationLeafChange(nil), base.LeafChanges...)
			switch name {
			case "source-sha":
				m.ExpectedSourceDigest = projectAllCmdTestDigest("other")
			case "character":
				m.LeafChanges[0].Character = "乙"
			case "old":
				m.LeafChanges[0].Old = "wrong"
			case "index":
				m.LeafChanges[0].Pointer = "/0/initial_state/known_facts/9"
			case "leading-zero":
				m.LeafChanges[0].Pointer = "/00/initial_state/known_facts/1"
			case "array-whole":
				m.LeafChanges[0].Pointer = "/0/initial_state/known_facts"
			case "resource":
				m.LeafChanges[0].Pointer = "/0/initial_state/resource_balances/0/actual_amount"
			case "identity":
				m.LeafChanges[0].Pointer = "/0/name"
			case "unknown-field":
				m.LeafChanges[0].Pointer = "/0/initial_state/arbitrary"
			case "bool-field":
				m.LeafChanges[0].Pointer = "/0/initial_state/location_name_known"
			case "duplicate":
				m.LeafChanges = append(m.LeafChanges, m.LeafChanges[0])
			case "missing-mode":
				m.Mode = ""
			case "unknown-mode":
				m.Mode = "generic-patch"
			case "mixed-resource-authority":
				m.ResourceID = "res_1111111111111111"
			case "noop":
				m.LeafChanges[0].New = "B"
			}
			if _, err := compilePipelineFoundationLeafCAS(m, before); err == nil {
				t.Fatal("unapproved CAS accepted")
			}
		})
	}
	after, err := compilePipelineFoundationLeafCAS(base, before)
	publicationArtifactMust(t, err)
	if !bytes.Contains(after, []byte("9007199254740993")) || !bytes.Contains(after, []byte("1.2300")) {
		t.Fatal("unrelated number lexemes changed")
	}
	publicationArtifactMust(t, validatePipelineFoundationRepairChange(base, before, after))
	if err := validatePipelineFoundationRepairChange(base, before, bytes.Replace(after, []byte("9007199254740993"), []byte("9007199254740992"), 1)); err == nil {
		t.Fatal("unapproved resource change accepted")
	}
	dropped := bytes.Replace(after, []byte(`"future_unknown":{"amount":1.2300},`), nil, 1)
	if bytes.Equal(dropped, after) {
		t.Fatal("invalid-schema fixture failed to remove its field")
	}
	if err := validatePipelineFoundationRepairChange(base, before, dropped); err == nil {
		t.Fatal("unknown existing schema field could be dropped")
	}
}

func TestFoundationHostLeafCASRejectsUnsafeRequestsWithoutPublishing(t *testing.T) {
	for _, mode := range []string{"missing-rebase", "damaged-archive", "active-lease", "zero-init", "detailed-generation", "delivery-ledger", "source-sha", "wrong-character", "wrong-old", "invalid-pressure", "duplicate-knowledge", "unknown-schema", "request-rebase", "mixed-report"} {
		t.Run(mode, func(t *testing.T) {
			opts, live := hostLeafCASRebasedFixture(t)
			st := store.NewStore(live)
			chars, err := st.Characters.Load()
			publicationArtifactMust(t, err)
			flags := pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "characters"}
			switch mode {
			case "missing-rebase":
				publicationArtifactMust(t, os.Remove(filepath.Join(live, "meta/all_chapter_rebase.json")))
			case "damaged-archive":
				var r pipelineAllChapterRebaseReceipt
				publicationArtifactMust(t, readPipelinePlanningJSON(filepath.Join(live, "meta/all_chapter_rebase.json"), &r))
				publicationArtifactMust(t, os.WriteFile(filepath.Join(r.ArchiveOutput, "premise.md"), []byte("changed archive"), 0600))
			case "active-lease":
				publicationArtifactMust(t, st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "must-not-overlap"}))
			case "zero-init":
				mustWriteFile(t, filepath.Join(live, "meta/first_chapter_generation_readiness.json"), `{"ready":true}`)
			case "detailed-generation":
				mustWriteFile(t, filepath.Join(live, "meta/planning/v2/probe.json"), `{"forbidden":true}`)
			case "delivery-ledger":
				mustWriteFile(t, filepath.Join(live, "meta/runtime/chapter_delivery/ledger.json"), `{"forbidden":true}`)
			case "unknown-schema":
				raw, err := os.ReadFile(filepath.Join(live, "characters.json"))
				publicationArtifactMust(t, err)
				raw = bytes.Replace(raw, []byte(`"name":`), []byte(`"preserve_unknown":{"n":9007199254740993},"name":`), 1)
				publicationArtifactMust(t, os.WriteFile(filepath.Join(live, "characters.json"), raw, 0600))
			case "request-rebase":
				flags.RebaseAllChapters = true
			}
			source, err := os.ReadFile(filepath.Join(live, "characters.json"))
			publicationArtifactMust(t, err)
			m := pipelineFoundationRepairManifest{Version: pipelineFoundationRepairVersion, Mode: pipelineFoundationLeafCASMode, Target: "characters", Instruction: "only the explicit host leaf CAS", ExpectedSourceDigest: pipelineFoundationRepairSHA(source), LeafChanges: []pipelineFoundationLeafChange{{Character: chars[0].Name, Pointer: "/0/initial_state/pressure", Old: "旧压力", New: "有限窗口时间"}}}
			switch mode {
			case "source-sha":
				m.ExpectedSourceDigest = projectAllCmdTestDigest("wrong")
			case "wrong-character":
				m.LeafChanges[0].Character = "not-the-bound-character"
			case "wrong-old":
				m.LeafChanges[0].Old = "wrong"
			case "invalid-pressure":
				m.LeafChanges[0].New = ""
			case "duplicate-knowledge":
				m.LeafChanges[0] = pipelineFoundationLeafChange{Character: chars[0].Name, Pointer: "/0/initial_state/known_facts/1", Old: "旧知识边界", New: chars[0].InitialState.KnownFacts[0]}
			case "mixed-report":
				m.ReportDigest = projectAllCmdTestDigest("not-authority-for-this-mode")
			}
			raw, err := json.Marshal(m)
			publicationArtifactMust(t, err)
			flags.ArchitectRepairFile = filepath.Join(t.TempDir(), "manifest.json")
			publicationArtifactMust(t, os.WriteFile(flags.ArchitectRepairFile, raw, 0600))
			before, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			old := pipelineFoundationRepairHost
			calls := 0
			pipelineFoundationRepairHost = func(bootstrap.Config, assets.Bundle, headless.Options) error {
				calls++
				return fmt.Errorf("provider prohibited")
			}
			t.Cleanup(func() { pipelineFoundationRepairHost = old })
			if err := runPipelineFoundationRepair(opts, flags, ""); err == nil {
				t.Fatal("unsafe host source request published")
			}
			after, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			if before != after || calls != 0 {
				t.Fatalf("rejected host CAS changed live or dispatched provider: calls=%d", calls)
			}
		})
	}
}

func TestFoundationHostLocationNameUnknownMissingToFalseWithoutModel(t *testing.T) {
	opts, live := hostLeafCASRebasedFixture(t)
	chars, err := store.NewStore(live).Characters.Load()
	publicationArtifactMust(t, err)
	before, err := os.ReadFile(filepath.Join(live, "characters.json"))
	publicationArtifactMust(t, err)
	manifest := map[string]any{"version": pipelineFoundationRepairVersion, "mode": pipelineFoundationLeafCASMode, "target": "characters", "instruction": "仅将明确角色的缺省位置名可见性标记为未知；不改场所或事实。", "expected_source_digest": pipelineFoundationRepairSHA(before), "location_name_unknown": map[string]any{"character": chars[0].Name, "pointer": "/0/initial_state/location_name_known", "expected_missing": true, "new": false}}
	raw, err := json.Marshal(manifest)
	publicationArtifactMust(t, err)
	path := filepath.Join(t.TempDir(), "privacy.json")
	publicationArtifactMust(t, os.WriteFile(path, raw, 0600))
	original := pipelineFoundationRepairHost
	t.Cleanup(func() { pipelineFoundationRepairHost = original })
	calls := 0
	pipelineFoundationRepairHost = func(bootstrap.Config, assets.Bundle, headless.Options) error {
		calls++
		return fmt.Errorf("no model may decide the host CAS")
	}
	if err := runPipelineFoundationRepair(opts, pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "characters", ArchitectRepairFile: path}, ""); err != nil {
		t.Fatalf("explicit missing-to-false location metadata CAS failed: %v (model_calls=%d)", err, calls)
	}
	got, err := store.NewStore(live).Characters.Load()
	publicationArtifactMust(t, err)
	if calls != 0 || got[0].InitialState.LocationNameKnown == nil || *got[0].InitialState.LocationNameKnown {
		t.Fatal("false-only source metadata was not preserved")
	}
	value := false
	chars[0].InitialState.LocationNameKnown = &value
	if !reflect.DeepEqual(got, chars) {
		t.Fatal("location privacy CAS changed unapproved character data")
	}
}

func TestFoundationHostLocationNameUnknownRejectsNonMissingOrUnapproved(t *testing.T) {
	for _, mode := range []string{"present-null", "present-true", "present-false", "new-null", "new-true", "new-missing", "expected-false", "expected-missing", "wrong-character", "source-drift", "other-field", "unknown-schema", "unknown-descriptor-field"} {
		t.Run(mode, func(t *testing.T) {
			opts, live := hostLeafCASRebasedFixture(t)
			chars, err := store.NewStore(live).Characters.Load()
			publicationArtifactMust(t, err)
			source, err := os.ReadFile(filepath.Join(live, "characters.json"))
			publicationArtifactMust(t, err)
			if strings.HasPrefix(mode, "present-") {
				literal := strings.TrimPrefix(mode, "present-")
				source = bytes.Replace(source, []byte(`"initial_state": {`), []byte(`"initial_state": {"location_name_known":`+literal+`,`), 1)
				publicationArtifactMust(t, os.WriteFile(filepath.Join(live, "characters.json"), source, 0600))
				if !bytes.Contains(source, []byte(`"location_name_known"`)) {
					t.Fatal("present-field fixture insertion failed")
				}
			}
			if mode == "unknown-schema" {
				source = bytes.Replace(source, []byte(`"name":`), []byte(`"future_unknown":{"keep":true},"name":`), 1)
				publicationArtifactMust(t, os.WriteFile(filepath.Join(live, "characters.json"), source, 0600))
			}
			descriptor := map[string]any{"character": chars[0].Name, "pointer": "/0/initial_state/location_name_known", "expected_missing": true, "new": false}
			switch mode {
			case "new-null":
				descriptor["new"] = nil
			case "new-true":
				descriptor["new"] = true
			case "new-missing":
				delete(descriptor, "new")
			case "expected-false":
				descriptor["expected_missing"] = false
			case "expected-missing":
				delete(descriptor, "expected_missing")
			case "wrong-character":
				descriptor["character"] = "not-the-bound-character"
			case "other-field":
				descriptor["pointer"] = "/0/initial_state/arbitrary_flag"
			case "unknown-descriptor-field":
				descriptor["arbitrary_flag"] = false
			}
			manifest := map[string]any{"version": pipelineFoundationRepairVersion, "mode": pipelineFoundationLeafCASMode, "target": "characters", "instruction": "only explicit false metadata", "expected_source_digest": pipelineFoundationRepairSHA(source), "location_name_unknown": descriptor}
			if mode == "source-drift" {
				publicationArtifactMust(t, os.WriteFile(filepath.Join(live, "characters.json"), append(source, '\n'), 0600))
			}
			raw, err := json.Marshal(manifest)
			publicationArtifactMust(t, err)
			path := filepath.Join(t.TempDir(), "privacy.json")
			publicationArtifactMust(t, os.WriteFile(path, raw, 0600))
			before, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			old := pipelineFoundationRepairHost
			calls := 0
			pipelineFoundationRepairHost = func(bootstrap.Config, assets.Bundle, headless.Options) error {
				calls++
				return fmt.Errorf("model prohibited")
			}
			t.Cleanup(func() { pipelineFoundationRepairHost = old })
			if err := runPipelineFoundationRepair(opts, pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "characters", ArchitectRepairFile: path}, ""); err == nil {
				t.Fatal("non-missing or unapproved bool CAS accepted")
			}
			after, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			if before != after || calls != 0 {
				t.Fatalf("rejected bool request changed live or called model: calls=%d", calls)
			}
		})
	}
}

func TestFoundationRepairLegacyManifestWireGolden(t *testing.T) {
	m := repairTestManifest()
	raw, err := json.Marshal(m)
	publicationArtifactMust(t, err)
	digest, err := pipelineProjectAllDigestE(m)
	publicationArtifactMust(t, err)
	// Captured against the actual 61372db repair structs via Go overlay.
	const old = "sha256:1df611f3b65799f18e9b113d3c7735a8225effb4d3c100887af4ed114fe8fbe6"
	if len(raw) != 570 || pipelineBytesSHA(raw) != old || digest != old || bytes.Contains(raw, []byte(`"mode"`)) || bytes.Contains(raw, []byte(`"leaf_changes"`)) || bytes.Contains(raw, []byte(`"location_name_unknown"`)) {
		t.Fatalf("legacy repair wire/digest changed: %s bytes=%d", digest, len(raw))
	}
}

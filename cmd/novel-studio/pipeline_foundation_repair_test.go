package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func repairTestManifest() pipelineFoundationRepairManifest {
	return pipelineFoundationRepairManifest{Version: pipelineFoundationRepairVersion, Target: "characters", Instruction: "HOST_ONLY_FIX_NOTE", ReportDigest: "sha256:" + strings.Repeat("a", 64), ExpectedSourceDigest: "sha256:" + strings.Repeat("b", 64), ResourceID: "res_1111111111111111", AllowedJSONPointers: []string{"/0/initial_state/resource_balances/0/readable_facts"}, NewResources: []pipelineFoundationRepairResource{{CharacterName: "甲", ResourceID: "res_2222222222222222", Purpose: "artifact_material", ExpectedName: "记录纸", ExpectedUnit: "张"}}}
}

func TestFoundationRepairExactFieldAuthorization(t *testing.T) {
	m := repairTestManifest()
	before := []byte(`[{"name":"甲","initial_state":{"resource_balances":[{"resource_id":"res_1111111111111111","actual_amount":1}]}},{"name":"乙"},{"name":"丙"}]`)
	valid := `[{"name":"甲","initial_state":{"resource_balances":[{"resource_id":"res_1111111111111111","actual_amount":1,"readable_facts":[{"id":"entry","text":"原始条目"}]},{"resource_id":"res_2222222222222222","name":"记录纸","actual_amount":3,"unit":"张"}]}},{"name":"乙"},{"name":"丙"}]`
	if err := validatePipelineFoundationRepairChange(m, before, []byte(valid)); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"old quantity":        strings.Replace(valid, `"actual_amount":1`, `"actual_amount":2`, 1),
		"new character":       strings.Replace(valid, `{"name":"丙"}`, `{"name":"丙"},{"name":"丁"}`, 1),
		"unapproved ID":       strings.Replace(valid, "res_2222222222222222", "res_3333333333333333", 1),
		"unknown stock":       strings.Replace(valid, `"actual_amount":3`, `"actual_amount":null`, 1),
		"historical evidence": strings.Replace(valid, `"unit":"张"`, `"unit":"张","readable_facts":[{"id":"history","text":"过去已经确认"}]`, 1),
		"owner rename":        strings.Replace(valid, `"name":"甲"`, `"name":"丁"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePipelineFoundationRepairChange(m, before, []byte(raw)); err == nil {
				t.Fatal("accepted unauthorized field/resource")
			}
		})
	}
	if bytes.Contains(before, []byte("readable_facts")) {
		t.Fatal("validator mutated source")
	}
	for _, p := range []string{"/0", "/0/initial_state", "/0/initial_state/resource_balances", "/01/initial_state/resource_balances/0/readable_facts"} {
		bad := m
		bad.AllowedJSONPointers = []string{p}
		if err := validatePipelineFoundationRepairManifest(bad); err == nil {
			t.Fatalf("broad pointer accepted: %s", p)
		}
	}
}

func TestFoundationRepairCLIRequiresExplicitHostOnlyBoundary(t *testing.T) {
	base := []string{"--architect-repair-file", "repair.json", "--refresh-architect", "--architect-target", "characters", "--stages", "architect"}
	if _, _, err := parsePipelineFlags(base); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--architect-repair-file", "repair.json"}, append(append([]string{}, base...), "--new-novel"), append(append([]string{}, base...), "--init-only"), {"--architect-repair-file", "repair.json", "--refresh-architect", "--architect-target", "characters", "--stages", "architect,outline-all"}} {
		if _, _, err := parsePipelineFlags(args); err == nil {
			t.Fatalf("invalid repair args accepted: %v", args)
		}
	}
	file := filepath.Join(t.TempDir(), "repair.json")
	m := repairTestManifest()
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, digest, err := loadPipelineFoundationRepairManifest(file)
	if err != nil || digest == "" || got.Instruction != m.Instruction {
		t.Fatalf("manifest: %+v %s %v", got, digest, err)
	}
	for _, bad := range [][]byte{append(raw, []byte(` {}`)...), bytes.Replace(raw, []byte(`"target"`), []byte(`"unknown_target"`), 1)} {
		if err := os.WriteFile(file, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadPipelineFoundationRepairManifest(file); err == nil {
			t.Fatal("malformed/unknown manifest accepted")
		}
	}
}

func foundationRepairFlowFixture(t *testing.T) (cliOptions, string, pipelineFoundationRepairManifest) {
	t.Helper()
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	chars, err := st.Characters.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range chars {
		chars[i].InitialState = &domain.CharacterInitialState{Location: "便利店", CurrentGoal: "核对自己见到的资料", Pressure: "有限时间", KnownFacts: []string{"知道自己的职责"}}
	}
	chars[0].InitialState.ResourceBalances = []domain.InitialCharacterResourceV2{{ResourceID: "res_1111111111111111", Name: "原账", PerceivedName: "原账", PerceivedLabel: "原账", Unit: "册", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}}}
	if err := st.Characters.Save(chars); err != nil {
		t.Fatal(err)
	}
	outline := []domain.OutlineEntry{{Chapter: 1, Title: "起", CoreEvent: "出现冲突", Scenes: []string{"便利店"}}, {Chapter: 2, Title: "承", CoreEvent: "独立核对", Scenes: []string{"便利店"}}, {Chapter: 3, Title: "合", CoreEvent: "三章结束", Scenes: []string{"便利店"}}}
	if err := st.Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetTotalChapters(3); err != nil {
		t.Fatal(err)
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	compass.EstimatedScale = "1-1卷，3-3章，每章2200—2500字"
	compass.NonNegotiables = []string{"三章结束"}
	if err := st.Outline.SaveCompass(*compass); err != nil {
		t.Fatal(err)
	}
	state := domain.PipelineState{Stages: append([]string(nil), defaultPipelineStages...), Prompt: "三名角色，三章结束。每章2200—2500字。", Completed: []string{"architect", "outline-all", "zero-init", "preplan"}}
	if err := savePipelineState(filepath.Join(live, "meta/pipeline.json"), &state); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "meta/pipeline_timings.jsonl", "preserved cold start 23:23:37\n")
	input, err := agents.BuildArcRehearsalInput(st, domain.ArcRehearsalInput{ArcID: "arc_repair", ArcFirstChapter: 1, ArcLastChapter: 3, BaseCanonRoot: "sha256:" + strings.Repeat("a", 64), SourceRoot: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	body := domain.ArcRehearsalBody{Summary: "资料缺失，不能细推", UnresolvedItems: []string{"补齐原始来源"}, MaterialChecks: []domain.ArcRehearsalMaterialCheck{{Operation: "核读原账", RequiresReadable: true, ResourceRefs: []string{"res_1111111111111111"}, Status: "missing", Explanation: "原账没有可读字段"}}}
	for _, entry := range input.Outline {
		body.Chapters = append(body.Chapters, domain.ArcRehearsalChapter{Chapter: entry.Chapter, ConditionalForecast: "若资料真实存在则可能核对", Assumptions: []string{"资料实际可读"}, CausalLinks: []string{"先有来源后核对"}, TimeResourceChecks: []string{"保留工时"}})
	}
	for _, o := range input.CharacterObservations {
		body.CharacterConflicts = append(body.CharacterConflicts, domain.ArcRehearsalCharacterConflict{Character: o.Character, CurrentGoal: o.CurrentGoal, Conflicts: []string{"资料未知"}, ConditionalChoices: []string{"等待来源"}})
	}
	for _, c := range input.HardContracts {
		body.ContractChecks = append(body.ContractChecks, domain.ArcRehearsalContractCheck{Contract: c, Assessment: "conditional", Conditions: []string{"资料来源齐全"}})
	}
	call := func(role string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "fixture", Model: "fixture", UsageIDs: []string{"synthetic-" + role}, ToolCallID: role, ResponseDigest: "sha256:" + strings.Repeat("c", 64)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect")})
	if err != nil {
		t.Fatal(err)
	}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveArcRehearsalReport(input, draft, report); err != nil {
		t.Fatal(err)
	}
	m := repairTestManifest()
	m.NewResources = []pipelineFoundationRepairResource{{CharacterName: chars[0].Name, ResourceID: "res_2222222222222222", Purpose: "artifact_material", ExpectedName: "记录纸", ExpectedUnit: "张"}, {CharacterName: chars[0].Name, ResourceID: "res_3333333333333333", Purpose: "writing_tool", ExpectedName: "笔", ExpectedUnit: "支"}}
	m.ReportDigest = report.ReportDigest
	raw, _ := os.ReadFile(filepath.Join(live, "characters.json"))
	m.ExpectedSourceDigest = pipelineFoundationRepairSHA(raw)
	root := pipelineRebaseRunRoot(live)
	rebaseAllTestWriteFile(t, root, "config.json", `{"provider":"ollama","model":"fixture","providers":{"ollama":{"type":"openai","base_url":"http://127.0.0.1:1/v1"}}}`)
	return cliOptions{ConfigPath: filepath.Join(root, "config.json"), Dir: root}, live, m
}

func TestFoundationRepairRebaseCandidateNeverPublishesRejectedSource(t *testing.T) {
	for _, bad := range []string{"new-character", "old-amount", "unapproved-resource", "wrong-name", "wrong-unit", "generator-instead-of-pen", "model-error", ""} {
		t.Run(fmt.Sprintf("bad=%v", bad), func(t *testing.T) {
			opts, live, m := foundationRepairFlowFixture(t)
			original, _ := os.ReadFile(filepath.Join(live, "meta/pipeline.json"))
			var state domain.PipelineState
			if err := json.Unmarshal(original, &state); err != nil {
				t.Fatal(err)
			}
			oldHost := pipelineFoundationRepairHost
			defer func() { pipelineFoundationRepairHost = oldHost }()
			calls := 0
			pipelineFoundationRepairHost = func(cfg bootstrap.Config, _ assets.Bundle, h headless.Options) error {
				calls++
				if cfg.OutputDir == live || !h.PreserveUserRules || !h.SkipQueueReplay || !strings.Contains(h.Prompt, m.Instruction) {
					t.Fatal("host did not run in scoped candidate")
				}
				st := store.NewStore(cfg.OutputDir)
				chars, err := st.Characters.Load()
				if err != nil {
					return err
				}
				chars[0].InitialState.ResourceBalances[0].ReadableFacts = []domain.ResourceReadableFactV2{{ID: "entry", Text: "原始条目"}}
				paper, pen := 3.0, 1.0
				chars[0].InitialState.ResourceBalances = append(chars[0].InitialState.ResourceBalances, domain.InitialCharacterResourceV2{ResourceID: "res_2222222222222222", Name: "记录纸", PerceivedName: "记录纸", PerceivedLabel: "记录纸", Unit: "张", ActualAmount: &paper, Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}}, domain.InitialCharacterResourceV2{ResourceID: "res_3333333333333333", Name: "笔", PerceivedName: "笔", PerceivedLabel: "笔", Unit: "支", ActualAmount: &pen, Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}})
				switch bad {
				case "new-character":
					extra := chars[1]
					extra.Name = "越权新角色"
					chars = append(chars, extra)
				case "old-amount":
					chars[0].InitialState.ResourceBalances[0].ActualAmount = &paper
				case "unapproved-resource":
					chars[0].InitialState.ResourceBalances[1].ResourceID = "res_4444444444444444"
				case "wrong-name":
					chars[0].InitialState.ResourceBalances[2].Name = "备用发电机"
				case "wrong-unit":
					chars[0].InitialState.ResourceBalances[2].Unit = "台"
				case "generator-instead-of-pen":
					chars[0].InitialState.ResourceBalances[2].Name = "备用发电机"
					chars[0].InitialState.ResourceBalances[2].Unit = "台"
				}
				raw, _ := json.Marshal(chars)
				args, _ := json.Marshal(map[string]any{"type": "characters", "content": json.RawMessage(raw)})
				owner := "fixture-repair"
				if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
					return err
				}
				defer st.Runtime.ReleasePipelineExecution(owner)
				_, err = tools.NewSaveFoundationTool(st).WithFoundationTypeRestriction("characters").WithFoundationRefreshEpoch(true).WithOneShotFoundationRefresh(true).WithDeferredFoundationFinalization(h.DeferFoundationFinalization).Execute(context.Background(), args)
				if (bad == "wrong-name" || bad == "wrong-unit" || bad == "generator-instead-of-pen") && err != nil {
					t.Fatalf("fixture must reach successful typed SaveFoundation before field authorization rejects publication: %v", err)
				}
				if err == nil && bad == "model-error" {
					return fmt.Errorf("synthetic provider failure after candidate save")
				}
				return err
			}
			plan := &pipelineFoundationRepairPlan{Manifest: m, Digest: projectAllCmdTestDigest("manifest")}
			before, err := store.DirectoryContentRoot(live)
			if err != nil {
				t.Fatal(err)
			}
			err = pipelineRebaseAllChaptersWithFoundationRepair(opts, plan)
			if bad != "" {
				if err == nil {
					t.Fatal("unauthorized candidate published")
				}
				after, _ := store.DirectoryContentRoot(live)
				if before != after {
					t.Fatalf("failed repair changed live: %s != %s: %v", before, after, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			var got domain.PipelineState
			if err := readPipelinePlanningJSON(filepath.Join(live, "meta/pipeline.json"), &got); err != nil {
				t.Fatal(err)
			}
			if got.Prompt != state.Prompt || strings.Contains(got.Prompt, m.Instruction) || len(got.Completed) != 0 {
				t.Fatal("repair poisoned creative prompt or kept stale graph")
			}
			if raw, _ := os.ReadFile(filepath.Join(live, "meta/pipeline_timings.jsonl")); !bytes.HasPrefix(raw, []byte("preserved cold start 23:23:37\n")) {
				t.Fatal("cold start history lost")
			}
			if err := store.NewStore(live).ValidateRebasedChapterZeroFoundationRefresh(); err != nil {
				t.Fatalf("published repair lost valid rebase authority: %v", err)
			}
			// A second target consumes the original report from the verified
			// archive, without another rebase or any loss of the first repair.
			m.Target = "update_compass"
			m.ResourceID = ""
			m.NewResources = nil
			m.AllowedJSONPointers = []string{"/non_negotiables", "/author_contracts"}
			m.RequiredAuthorRefs = []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}
			compassRaw, _ := os.ReadFile(filepath.Join(live, "meta/compass.json"))
			m.ExpectedSourceDigest = pipelineFoundationRepairSHA(compassRaw)
			manifestPath := filepath.Join(pipelineRebaseRunRoot(live), "compass-repair.json")
			manifestRaw, _ := json.Marshal(m)
			if err := os.WriteFile(manifestPath, manifestRaw, 0600); err != nil {
				t.Fatal(err)
			}
			omitRequired := true
			pipelineFoundationRepairHost = func(cfg bootstrap.Config, _ assets.Bundle, h headless.Options) error {
				calls++
				st := store.NewStore(cfg.OutputDir)
				if cfg.OutputDir == live {
					t.Fatal("second target wrote live")
				}
				catalog, err := st.LoadAuthorSources()
				if err != nil || catalog == nil {
					return fmt.Errorf("catalog: %v", err)
				}
				if len(catalog.Sources) != 1 || catalog.Sources[0].Text != state.Prompt || strings.Contains(catalog.Sources[0].Text, m.Instruction) {
					t.Fatal("repair note entered author catalog")
				}
				if _, err := st.Outline.LoadCompass(); err == nil {
					t.Fatal("new source-bound mode accepted the old unbound compass")
				}
				owner := "fixture-compass"
				if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
					return err
				}
				defer st.Runtime.ReleasePipelineExecution(owner)
				if _, err := tools.NewFoundationSourceContextTool(st, "update_compass").Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
					return fmt.Errorf("raw migration context: %w", err)
				}
				var compass domain.StoryCompass
				if err := json.Unmarshal(compassRaw, &compass); err != nil {
					return err
				}
				compass.AuthorContracts = &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest, Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}}
				if omitRequired {
					compass.AuthorContracts.Refs = nil
				}
				compass.NonNegotiables = nil
				raw, _ := json.Marshal(compass)
				args, _ := json.Marshal(map[string]any{"type": "update_compass", "content": json.RawMessage(raw)})
				_, err = tools.NewSaveFoundationTool(st).WithFoundationTypeRestriction("update_compass").WithFoundationRefreshEpoch(true).WithOneShotFoundationRefresh(true).WithDeferredFoundationFinalization(h.DeferFoundationFinalization).Execute(context.Background(), args)
				return err
			}
			beforeMissing, _ := store.DirectoryContentRoot(live)
			if err := runPipelineFoundationRepair(opts, pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "update_compass", ArchitectRepairFile: manifestPath}, ""); err == nil || !strings.Contains(err.Error(), "omitted required original author") {
				t.Fatalf("missing required author paragraph was not rejected at publication: %v", err)
			}
			afterMissing, _ := store.DirectoryContentRoot(live)
			if beforeMissing != afterMissing {
				t.Fatal("missing author requirement candidate changed live")
			}
			omitRequired = false
			if err := runPipelineFoundationRepair(opts, pipelineFlags{Stages: "architect", RefreshArchitect: true, ArchitectTarget: "update_compass", ArchitectRepairFile: manifestPath}, ""); err != nil {
				t.Fatalf("second COW source repair: %v", err)
			}
			if calls != 3 {
				t.Fatalf("unexpected model invocation count=%d", calls)
			}
			if err := store.NewStore(live).ValidateRebasedChapterZeroFoundationRefresh(); err != nil {
				t.Fatalf("second COW lost original rebase authorization: %v", err)
			}
			if err := readPipelinePlanningJSON(filepath.Join(live, "meta/pipeline.json"), &got); err != nil || got.Prompt != state.Prompt {
				t.Fatalf("second target changed original Prompt: %v", err)
			}
		})
	}
}

func TestFoundationRepairSidecarDefersOnlyFinalization(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			live := seedZeroInitProject(t)
			st := store.NewStore(live)
			if err := st.Progress.UpdatePhase(domain.PhaseInit); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(live, "meta/progress.json"))
			ragBefore, err := pipelineRebaseRAGAuthorityRoot(live)
			if err != nil {
				t.Fatal(err)
			}
			chars, err := st.Characters.Load()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(chars)
			args, _ := json.Marshal(map[string]any{"type": "characters", "content": json.RawMessage(raw)})
			if _, err := tools.NewSaveFoundationTool(st).WithDeferredFoundationFinalization(deferred).Execute(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(live, "meta/progress.json"))
			ragAfter, err := pipelineRebaseRAGAuthorityRoot(live)
			if err != nil {
				t.Fatal(err)
			}
			if deferred {
				if !bytes.Equal(before, after) || ragBefore != ragAfter {
					t.Fatal("repair sidecar changed phase or frozen retrieval")
				}
			} else {
				p, err := st.Progress.Load()
				if err != nil || p.Phase != domain.PhaseWriting || ragBefore == ragAfter {
					t.Fatalf("ordinary save lost legacy finalization: phase=%v rag_changed=%v err=%v", p.Phase, ragBefore != ragAfter, err)
				}
			}
		})
	}
}

func TestFoundationRepairPreflightDriftAndStartedGenerationAreZeroWrite(t *testing.T) {
	for _, kind := range []string{"prompt", "source", "report", "timer", "generation"} {
		t.Run(kind, func(t *testing.T) {
			_, live, m := foundationRepairFlowFixture(t)
			p := &pipelineFoundationRepairPlan{Manifest: m, Digest: projectAllCmdTestDigest("manifest")}
			switch kind {
			case "prompt":
				p.ExplicitPrompt = "temporary fix pretending to be author"
			case "source":
				p.Manifest.ExpectedSourceDigest = projectAllCmdTestDigest("changed")
			case "report":
				p.Manifest.ReportDigest = projectAllCmdTestDigest("absent")
			case "timer":
				rebaseAllTestWriteFile(t, live, "meta/runtime/chapter_delivery/ledger.json", `{"started_at":"2026-09-13T15:23:37Z"}`)
			case "generation":
				if err := os.MkdirAll(filepath.Join(live, "meta/planning/v2"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.DirectoryContentRoot(live)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.preflight(live); err == nil {
				t.Fatal("drifted or already detailed source accepted")
			}
			after, _ := store.DirectoryContentRoot(live)
			if before != after {
				t.Fatal("invalid repair preflight changed live")
			}
		})
	}
}

func TestFoundationRepairFrozenOutlineCatalogIsOptionalAndAuthenticated(t *testing.T) {
	_, live, _ := foundationRepairFlowFixture(t)
	st := store.NewStore(live)
	legacy, err := loadPipelineOutlineAllFrozenFoundation(live)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := legacy.Authorities[store.AuthorSourcesPath]; ok {
		t.Fatal("legacy absence acquired a new authority")
	}
	value, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: "三章结束。"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAuthorSources(value); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPipelineOutlineAllFrozenFoundation(live); err == nil {
		t.Fatal("raw frozen compass bypassed new source validation")
	}
	var compass domain.StoryCompass
	raw, _ := os.ReadFile(filepath.Join(live, "meta/compass.json"))
	if err := json.Unmarshal(raw, &compass); err != nil {
		t.Fatal(err)
	}
	compass.NonNegotiables = nil
	compass.AuthorContracts = &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: value.Digest, Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}}
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	bound, err := loadPipelineOutlineAllFrozenFoundation(live)
	if err != nil {
		t.Fatal(err)
	}
	catalogBytes, _ := os.ReadFile(filepath.Join(live, store.AuthorSourcesPath))
	if bound.Root == legacy.Root || bound.Authorities[store.AuthorSourcesPath] != string(catalogBytes) {
		t.Fatal("frozen source root did not bind complete original author bytes")
	}
	text, err := pipelineArchitectPrompt(live, "三章结束。")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "非空 non_negotiables") || strings.Contains(text, "用户硬规则：复杂项目") {
		t.Fatal("Architect prompt still invents a user hard contract")
	}
}

func TestFoundationRepairFreshAuthorCaptureAndEmptyBoundContracts(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output/novel")
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const original = "  原始作者段落，允许人物自己选择。\r\n\r\n三章结束。  "
	rebaseAllTestWriteFile(t, root, "brainstorm.md", "独立确认的构思原文。\n")
	if err := ensurePipelineArchitectAuthorSources(live, original); err != nil {
		t.Fatal(err)
	}
	catalog, err := st.LoadAuthorSources()
	if err != nil || catalog == nil {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	seenPrompt, seenBrainstorm := false, false
	for _, source := range catalog.Sources {
		if source.ID == "startup_prompt" {
			seenPrompt = true
			if source.Text != original || strings.Contains(source.Text, "[创作指令]") {
				t.Fatal("host label replaced original author bytes")
			}
		}
		if source.ID == "brainstorm" {
			seenBrainstorm = true
			if source.Text != "独立确认的构思原文。\n" {
				t.Fatal("brainstorm bytes changed")
			}
		}
	}
	if !seenPrompt || !seenBrainstorm {
		t.Fatal("independent author sources absent")
	}
	before, _ := os.ReadFile(filepath.Join(live, store.AuthorSourcesPath))
	if err := ensurePipelineArchitectAuthorSources(live, "HOST_ONLY_FIX_NOTE"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(live, store.AuthorSourcesPath))
	if !bytes.Equal(before, after) {
		t.Fatal("resume overwrote original catalog")
	}
	compass := domain.StoryCompass{EndingDirection: "软终局", OpenThreads: []string{"开放选择"}, EstimatedScale: "3-3章", AuthorContracts: &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest, Refs: []domain.AuthorSourceParagraphRefV1{}}}
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Outline.LoadCompass()
	if err != nil || len(loaded.NonNegotiables) != 0 || !pipelineCompassHasAuthorContractBoundary(loaded) || pipelineArchitectCompassNeedsRepair(live) {
		t.Fatalf("valid explicitly empty author contracts were forced into invented constraints: %+v %v", loaded, err)
	}
	if pipelineCompassHasAuthorContractBoundary(&domain.StoryCompass{}) {
		t.Fatal("legacy missing contracts were silently accepted")
	}
	legacy := seedZeroInitProject(t)
	if err := ensurePipelineArchitectAuthorSources(legacy, original); err != nil {
		t.Fatal(err)
	}
	oldCatalog, err := store.NewStore(legacy).LoadAuthorSources()
	if err != nil || oldCatalog != nil {
		t.Fatal("legacy foundation silently upgraded")
	}
}

func TestFoundationRepairOutlineContractLabelsKeepOriginalSources(t *testing.T) {
	compass := domain.StoryCompass{EndingDirection: "软终局不得冒充作者原文", NonNegotiables: []string{"真正作者段落"}, OpenThreads: []string{"软开放线索"}, AuthorContracts: &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: projectAllCmdTestDigest("catalog"), Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}}}
	values := pipelineOutlineAllContractRegistry(compass)
	if len(values) != 2 || values[0].Ref.Kind != domain.StoryContractNonNegotiable || values[0].Source != "真正作者段落" || values[1].Source != "软开放线索" {
		t.Fatalf("new contract ID/text shifted: %+v", values)
	}
	for _, entry := range values {
		if entry.Ref.SourceDigest != pipelineFoundationRepairSHA([]byte(entry.Source)) {
			t.Fatal("source label mismatches its digest")
		}
	}
	copy := copyPipelineCompassAuthorContracts(compass.AuthorContracts)
	copy.Refs[0].SourceID = "changed"
	if compass.AuthorContracts.Refs[0].SourceID != "startup_prompt" {
		t.Fatal("receipt binding aliases mutable compass")
	}
	compass.AuthorContracts = nil
	legacy := pipelineOutlineAllContractRegistry(compass)
	if len(legacy) != 3 || legacy[0].Ref.Kind != domain.StoryContractEnding || legacy[0].Source != compass.EndingDirection {
		t.Fatal("legacy contract registry changed")
	}
}

func TestFoundationRepairOutlineReceiptCarriesEmptyAndNonemptySourceMode(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			_, live, _ := foundationRepairFlowFixture(t)
			st := store.NewStore(live)
			catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: "三个角色、三章、每章2200—2500字。"}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(live, "meta/compass.json"))
			var compass domain.StoryCompass
			if err := json.Unmarshal(raw, &compass); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveAuthorSources(catalog); err != nil {
				t.Fatal(err)
			}
			compass.NonNegotiables = nil
			compass.AuthorContracts = &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest}
			if !empty {
				compass.AuthorContracts.Refs = []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}}
			}
			if err := st.Outline.SaveCompass(compass); err != nil {
				t.Fatal(err)
			}
			loaded, err := st.Outline.LoadCompass()
			if err != nil {
				t.Fatal(err)
			}
			compass = *loaded
			if err := activatePipelineSealedTwoPassModeAtOutput(live); err != nil {
				t.Fatal(err)
			}
			progress, err := st.Progress.Load()
			if err != nil {
				t.Fatal(err)
			}
			progress.GenerationID = "repair-outline-generation"
			if err := st.Progress.Save(progress); err != nil {
				t.Fatal(err)
			}
			owner := "fixture-outline-repair"
			if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1, PlanDigest: "fixture-plan", Owner: owner, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			defer st.Runtime.ReleasePipelineExecution(owner)
			lock, err := st.Runtime.InspectPipelineExecution()
			if err != nil {
				t.Fatal(err)
			}
			identity := domain.OutlineAllModelIdentity{CoordinatorProvider: "fixture", CoordinatorModel: "fixture", CoordinatorReasoning: "medium", ArchitectProvider: "fixture", ArchitectModel: "fixture", ArchitectReasoning: "medium"}
			modelDigest, err := domain.ComputeOutlineAllModelIdentityDigest(identity)
			if err != nil {
				t.Fatal(err)
			}
			target := domain.BookScaleTarget{Range: domain.BookScaleRange{MinVolumes: 1, MaxVolumes: 1, MinChapters: 3, MaxChapters: 3}, TargetVolumes: 1, TargetChapters: 3, TargetWords: 6600, TargetWordsPerChapter: 2200}
			digest := projectAllCmdTestDigest("source")
			receipt, err := ensurePipelineOutlineAllReceipt(st, *lock, compass, target, digest, digest, digest, digest, "repair-outline-attempt", live, identity, modelDigest, digest)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.AuthorContracts == nil || len(receipt.NonNegotiables) != len(compass.AuthorContracts.Refs) {
				t.Fatal("new receipt lost empty/nonempty author mode")
			}
			if _, err := store.NewStore(live).LoadOutlineAllExecutionReceipt(); err != nil {
				t.Fatal(err)
			}
			if _, err := ensurePipelineOutlineAllReceipt(st, *lock, compass, target, digest, digest, digest, digest, "repair-outline-attempt", live, identity, modelDigest, digest); err != nil {
				t.Fatalf("resume lost author binding: %v", err)
			}
		})
	}
}

func TestFoundationRepairCopyNamespaceRejectsAliasesBeforeWrite(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output/novel")
	if err := os.MkdirAll(live, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(live, pipelineRebaseCandidateRoot(live)); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineFoundationRepairCopyTarget(live, filepath.Join(pipelineRebaseCandidateRoot(live), "attempt/output")); err == nil {
		t.Fatal("copy target aliased live before publisher safety check")
	}
	if err := validatePipelineFoundationRepairCopyTarget(live, filepath.Join(root, "unrelated/output")); err == nil {
		t.Fatal("foreign destination accepted")
	}
	if entries, err := os.ReadDir(live); err != nil || len(entries) != 0 {
		t.Fatal("namespace validation wrote live")
	}
}

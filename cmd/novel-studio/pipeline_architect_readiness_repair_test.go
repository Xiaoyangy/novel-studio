package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestArchitectReadinessRepairRoutesActualFindingsToOneFoundation(t *testing.T) {
	for _, kind := range []string{"world_rules", "world_codex", "book_world"} {
		t.Run(kind, func(t *testing.T) {
			dir := seedZeroInitProject(t)
			st := store.NewStore(dir)
			switch kind {
			case "world_rules":
				rules, err := st.World.LoadWorldRules()
				if err != nil {
					t.Fatal(err)
				}
				rules[0].Rule = ""
				if err := st.World.SaveWorldRules(rules); err != nil {
					t.Fatal(err)
				}
			case "world_codex":
				codex, err := st.LoadWorldCodex()
				if err != nil || codex == nil {
					t.Fatalf("load codex: %v", err)
				}
				codex.SchemaVersion = domain.CurrentWorldCodexSchemaVersion
				codex.Mechanisms = nil
				codex.CounterfactualTests = nil
				if err := st.SaveWorldCodex(*codex); err != nil {
					t.Fatal(err)
				}
			case "book_world":
				world, err := st.World.LoadBookWorld()
				if err != nil || world == nil {
					t.Fatalf("load world: %v", err)
				}
				world.Version = domain.CurrentBookWorldSchemaVersion
				world.Factions[0].Relations = []domain.FactionRelation{{Target: "missing-faction", Kind: "rivals"}}
				if err := st.World.SaveBookWorld(*world); err != nil {
					t.Fatal(err)
				}
			}
			readiness := assessArchitectReadiness(dir)
			target, err := pipelineArchitectReadinessRepairTarget(readiness)
			if err != nil || target.Type != kind {
				t.Fatalf("wrong repair target: %+v %v; issues=%v", target, err, readiness.Issues)
			}
			opts, err := pipelineArchitectRepairHeadlessOptions(dir, "三章完结，角色不能提前知道秘密。", nil, readiness, target)
			if err != nil {
				t.Fatal(err)
			}
			if opts.FoundationRefreshTarget != kind || !opts.OneShotFoundationRefresh || !opts.StopAfterFoundationChange ||
				!opts.PreserveUserRules || !opts.PreserveCheckpointsOnStart || !opts.DisableFlowRouter || !opts.RecordFoundationRefreshEpoch ||
				opts.FoundationChangeCheckpointStep != tools.FoundationRefreshCheckpointStep(kind) ||
				len(opts.FoundationChangeArtifacts) != 1 || opts.FoundationChangeArtifacts[0] != kind+".json" {
				t.Fatalf("repair is not source-bound and one-shot: %+v", opts)
			}
			if !strings.Contains(opts.Prompt, fmt.Sprintf("save_foundation(type=%q)", kind)) ||
				!strings.Contains(opts.Prompt, "不得凭空新增势力") || !strings.Contains(opts.Prompt, "findings") {
				t.Fatalf("repair prompt lost exact source/finding boundaries: %s", opts.Prompt)
			}
			if kind == "world_codex" && (!strings.Contains(opts.Prompt, "mechanisms") || !strings.Contains(opts.Prompt, "change_evidence=")) {
				t.Fatal("codex repair lacks mechanism findings or revision evidence")
			}
			for _, other := range []string{"world_rules", "world_codex", "book_world"} {
				if other != kind && strings.Contains(opts.Prompt, "[当前 "+other+".json]") {
					t.Fatalf("repair multiplied writable source snapshots with %s", other)
				}
			}
		})
	}
}

func TestArchitectReadinessRepairRejectsUnknownOrUnrelatedFailures(t *testing.T) {
	known := domain.WorldCoherenceFinding{Code: "codex.mechanisms.missing", Severity: domain.WorldCoherenceSeverityError, Subject: "world_codex.mechanisms", Message: "机制不能为空"}
	for _, tc := range []struct {
		name     string
		findings []domain.WorldCoherenceFinding
		extra    string
	}{
		{"unknown source", []domain.WorldCoherenceFinding{{Severity: "error", Subject: "characters[0]", Message: "角色错误"}}, ""},
		{"prefix lookalike", []domain.WorldCoherenceFinding{{Severity: "error", Subject: "world_codex_backup.mechanisms", Message: "错误"}}, ""},
		{"mixed unknown source", []domain.WorldCoherenceFinding{known, {Severity: "error", Subject: "unowned", Message: "未知错误"}}, ""},
		{"unrelated readiness issue", []domain.WorldCoherenceFinding{known}, "第一章大纲缺少 title/core_event"},
		{"warning only", []domain.WorldCoherenceFinding{{Severity: "warning", Subject: "world_codex.schema_version", Message: "旧版兼容"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := domain.WorldCoherenceReport{Findings: tc.findings}
			readiness := architectReadiness{WorldCoherence: &report, Issues: report.BlockingIssues()}
			if tc.extra != "" {
				readiness.Issues = append(readiness.Issues, tc.extra)
			}
			if _, err := pipelineArchitectReadinessRepairTarget(readiness); err == nil {
				t.Fatal("unknown evidence granted model mutation authority")
			}
		})
	}
}

func TestArchitectReadinessRepairSelectsOneSourceAndReassessesRemaining(t *testing.T) {
	report := domain.WorldCoherenceReport{Findings: []domain.WorldCoherenceFinding{
		{Code: "codex.probe", Severity: "error", Subject: "world_codex.counterfactual_tests[0]", Message: "缺少禁止后果"},
		{Code: "world.route", Severity: "error", Subject: "book_world.routes[0]", Message: "缺少耗时"},
	}}
	readiness := architectReadiness{WorldCoherence: &report, Issues: report.BlockingIssues()}
	first, err := pipelineArchitectReadinessRepairTarget(readiness)
	if err != nil || first.Type != "world_codex" {
		t.Fatalf("first target=%+v err=%v", first, err)
	}
	if findings := pipelineArchitectRepairFindings(readiness, first.Type); len(findings) != 1 || findings[0].Subject != "world_codex.counterfactual_tests[0]" {
		t.Fatalf("first repair can modify unrelated findings: %+v", findings)
	}
	report.Findings = report.Findings[1:]
	readiness.Issues = report.BlockingIssues()
	second, err := pipelineArchitectReadinessRepairTarget(readiness)
	if err != nil || second.Type != "book_world" {
		t.Fatalf("reassessed target=%+v err=%v", second, err)
	}
	if _, err := pipelineArchitectRepairHeadlessOptions(t.TempDir(), "", nil, readiness, first); err == nil {
		t.Fatal("stale target retained authority after evidence changed")
	}
}

func TestArchitectAutoRepairDoesNotInventV2Factions(t *testing.T) {
	for _, codexV2 := range []bool{false, true} {
		dir := t.TempDir()
		st := store.NewStore(dir)
		world := domain.BookWorld{Version: domain.CurrentBookWorldSchemaVersion, Factions: []domain.WorldFaction{{
			ID: "ferry", Name: "渡船组", Relations: []domain.FactionRelation{{Target: "unknown", Kind: "rival"}},
		}}}
		if codexV2 {
			world.Version = 1
			if err := st.SaveWorldCodex(domain.WorldCodex{SchemaVersion: domain.CurrentWorldCodexSchemaVersion}); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.World.SaveBookWorld(world); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "book_world.json")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := pipelineAutoRepairBookWorldStructure(dir); err != nil || changed {
			t.Fatalf("v2 automatic faction invention: changed=%v err=%v", changed, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("v2 authored world was changed by legacy auto-repair")
		}
	}
}

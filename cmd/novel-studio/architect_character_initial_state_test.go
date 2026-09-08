package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func enableInitialStateReadinessProtocol(t *testing.T, st *store.Store) {
	t.Helper()
	codex, err := st.LoadWorldCodex()
	if err != nil || codex == nil {
		t.Fatalf("load codex: %v", err)
	}
	codex.CharacterViewVersion = domain.CurrentWorldCharacterViewVersion
	if err := st.SaveWorldCodex(*codex); err != nil {
		t.Fatal(err)
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		t.Fatal(err)
	}
	for i := range rules {
		rules[i].CharacterView = "只依照实际出示的原件核对当前记录。"
	}
	if err := st.World.SaveWorldRules(rules); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectInitialStateReadinessRoutesMissingOpeningStatesToOneShotCharacters(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	enableInitialStateReadinessProtocol(t, st)
	readiness := assessArchitectReadiness(dir)
	if readiness.Ready || len(readiness.SourceFindings) != 3 || !readiness.WorldCoherence.Ready {
		t.Fatalf("opening-state gaps were not isolated from world issues: %+v", readiness)
	}
	for _, finding := range readiness.SourceFindings {
		if finding.Source != "characters.json" || finding.Code != "characters.initial_state.missing" ||
			!strings.HasSuffix(finding.Subject, ".initial_state") {
			t.Fatalf("missing explicit source/path for opening gap: %+v", finding)
		}
	}
	target, err := pipelineArchitectReadinessRepairTarget(readiness)
	if err != nil || target.Type != "characters" {
		t.Fatalf("missing initial state routed to wrong author source: %+v %v", target, err)
	}
	opts, err := pipelineArchitectRepairHeadlessOptions(dir, "保留每个角色自己的开局事实。", nil, readiness, target)
	if err != nil {
		t.Fatal(err)
	}
	if opts.FoundationRefreshTarget != "characters" || !opts.OneShotFoundationRefresh || !opts.PreserveUserRules ||
		!opts.StopAfterFoundationChange || !opts.RecordFoundationRefreshEpoch ||
		opts.FoundationChangeCheckpointStep != tools.FoundationRefreshCheckpointStep("characters") ||
		!reflect.DeepEqual(opts.FoundationChangeArtifacts, []string{"characters.json"}) {
		t.Fatalf("opening-state repair escaped one-shot character authority: %+v", opts)
	}
	for _, want := range []string{"save_foundation(type=\"characters\")", "initial_state", "known_facts", "book_world.places", "不能从未来", "保留全部角色身份"} {
		if !strings.Contains(opts.Prompt, want) {
			t.Fatalf("repair lacks %q guard", want)
		}
	}
}

func TestArchitectInitialStateReadinessAcceptsLocationsAndRechecksOldReadyReceipt(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	enableInitialStateReadinessProtocol(t, st)
	characters, err := st.Characters.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range characters {
		location := "old_block"
		if i%2 != 0 {
			location = "便利店"
		}
		characters[i].InitialState = &domain.CharacterInitialState{
			Location: location, CurrentGoal: "核对自己眼前的凭据", Pressure: "只能依已经知道的事实判断",
			KnownFacts: []string{"自己确实持有一份当前记录"},
		}
	}
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	ready := assessArchitectReadiness(dir)
	if !ready.Ready || len(ready.SourceFindings) != 0 {
		t.Fatalf("valid explicit opening facts rejected: %+v", ready)
	}
	if err := writeArchitectReadiness(dir, ready); err != nil {
		t.Fatal(err)
	}
	if ok, reason := architectReadinessState(dir); !ok {
		t.Fatalf("valid opening-state receipt rejected: %s", reason)
	}
	path := filepath.Join(dir, "characters.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	characters[0].InitialState.Location = "不存在的第五场景"
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	bad := assessArchitectReadiness(dir)
	if bad.Ready || len(bad.SourceFindings) != 1 || bad.SourceFindings[0].Subject != "characters[0].initial_state.location" {
		t.Fatalf("unknown opening location was not attributed precisely: %+v", bad)
	}
	if ok, reason := architectReadinessState(dir); ok || !strings.Contains(reason, "角色开局态尚未就绪") {
		t.Fatalf("old ready receipt bypassed new explicit opening-state check: %v %s", ok, reason)
	}
}

func TestArchitectInitialStateRequirementsRespectTierAndProtagonistRoles(t *testing.T) {
	characters := []domain.Character{
		{Name: "核心", Tier: "core"}, {Name: "重要", Tier: "important"}, {Name: "默认重要"},
		{Name: "主角", Tier: "secondary", Role: "主角"},
		{Name: "普通配角", Tier: "secondary"}, {Name: "装饰人物", Tier: "decorative"},
		{Name: "背景父亲", Tier: "decorative", Role: "主角父亲"},
	}
	findings := architectCharacterInitialStateFindings(characters, &domain.WorldCodex{CharacterViewVersion: 1}, nil)
	if len(findings) != 4 {
		t.Fatalf("important-character scope widened or lost protagonist: %+v", findings)
	}
}

func TestArchitectInitialStateLegacyReadinessAndSerializationRemainUnchanged(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	before := assessArchitectReadiness(dir)
	characters, err := st.Characters.Load()
	if err != nil {
		t.Fatal(err)
	}
	characters[0].InitialState = &domain.CharacterInitialState{}
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	after := assessArchitectReadiness(dir)
	before.GeneratedAt, after.GeneratedAt = "", ""
	if !reflect.DeepEqual(before, after) || !after.Ready {
		t.Fatalf("legacy protocol acquired a new readiness condition: before=%+v after=%+v", before, after)
	}
	raw, err := json.Marshal(after)
	if err != nil || strings.Contains(string(raw), "source_findings") {
		t.Fatalf("legacy readiness serialization changed: %s err=%v", raw, err)
	}
}

func TestArchitectInitialStateRepairRejectsUnownedSourceCodeOrPath(t *testing.T) {
	valid := architectSourceFinding{Source: "characters.json", WorldCoherenceFinding: domain.WorldCoherenceFinding{
		Code: "characters.initial_state.missing", Severity: "error", Subject: "characters[0].initial_state", Message: "缺少初态",
	}}
	for _, mutate := range []func(*architectSourceFinding){
		func(f *architectSourceFinding) { f.Source = "book_world.json" },
		func(f *architectSourceFinding) { f.Code = "please_rewrite_characters" },
		func(f *architectSourceFinding) { f.Subject = "characters[0].arc" },
		func(f *architectSourceFinding) { f.Subject = "characters[0].initial_state../world_codex" },
	} {
		finding := valid
		mutate(&finding)
		readiness := architectReadiness{SourceFindings: []architectSourceFinding{finding}, Issues: []string{finding.blockingMessage()}}
		if _, err := pipelineArchitectReadinessRepairTarget(readiness); err == nil {
			t.Fatalf("unowned typed finding granted mutation authority: %+v", finding)
		}
	}
	readiness := architectReadiness{Issues: []string{"characters[0].initial_state：缺少初态"}, WorldCoherence: &domain.WorldCoherenceReport{}}
	if _, err := pipelineArchitectReadinessRepairTarget(readiness); err == nil {
		t.Fatal("free-text issue alone granted character rewrite authority")
	}
}

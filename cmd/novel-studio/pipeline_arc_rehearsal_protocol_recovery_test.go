package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func rehearsalProtocolRecoveryFixture(t *testing.T) (*store.Store, bootstrap.Config, domain.ArcRehearsalInput) {
	t.Helper()
	opts, st, _ := v3RestartEntryFixture(t)
	publicationArtifactMust(t, st.Characters.Save([]domain.Character{{Name: "甲", Role: "值班员", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "值班室", CurrentGoal: "依本人所见核对", KnownFacts: []string{"知道本人职责"}}}}))
	cfg, _, err := loadPipelineDefaultInputsReadOnly(opts)
	publicationArtifactMust(t, err)
	input, err := buildPipelineArcRehearsalInput(st, cfg)
	publicationArtifactMust(t, err)
	return st, cfg, input
}

func rehearsalProtocolRecoveryDraft(t *testing.T, st *store.Store, input domain.ArcRehearsalInput, report bool) domain.ArcRehearsalDraft {
	t.Helper()
	body := domain.ArcRehearsalBody{Summary: "恢复测试的条件预测", MaterialChecks: []domain.ArcRehearsalMaterialCheck{{Operation: "无外部物料的测试操作", Status: "not_required", Explanation: "不证明真实小说可达性"}}}
	for _, c := range input.Outline {
		body.Chapters = append(body.Chapters, domain.ArcRehearsalChapter{Chapter: c.Chapter, ConditionalForecast: "若条件成立则可能执行", Assumptions: []string{"尚未执行"}, CausalLinks: []string{"先有条件"}, TimeResourceChecks: []string{"实际工时待核"}})
	}
	for _, o := range input.CharacterObservations {
		body.CharacterConflicts = append(body.CharacterConflicts, domain.ArcRehearsalCharacterConflict{Character: o.Character, CurrentGoal: o.CurrentGoal, Conflicts: []string{"条件待核"}, ConditionalChoices: []string{"本人独立选择"}})
	}
	for _, contract := range input.HardContracts {
		body.ContractChecks = append(body.ContractChecks, domain.ArcRehearsalContractCheck{Contract: contract, Assessment: "conditional", Conditions: []string{"尚需实际履行"}})
	}
	call := func(role string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "test", Model: "no-provider", UsageIDs: []string{role + "-" + input.InputDigest}, ToolCallID: role, ResponseDigest: projectAllCmdTestDigest(role)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect")})
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.SaveArcRehearsalDraft(input, draft))
	if report {
		value, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter")})
		publicationArtifactMust(t, err)
		publicationArtifactMust(t, st.SaveArcRehearsalReport(input, draft, value))
	}
	return draft
}

func rehearsalProtocolLegacyInput(t *testing.T, current domain.ArcRehearsalInput) domain.ArcRehearsalInput {
	t.Helper()
	legacy, err := agents.LegacyArcRehearsalProtocolDigest()
	publicationArtifactMust(t, err)
	if legacy == current.ProtocolDigest {
		t.Fatal("new and legacy transports share a protocol identity")
	}
	current.ProtocolDigest = legacy
	value, err := domain.FinalizeArcRehearsalInput(current)
	publicationArtifactMust(t, err)
	return value
}

func TestPipelineRehearsalProtocolRecoversExactLegacyDraftOrReport(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "report"}[completed], func(t *testing.T) {
			st, cfg, current := rehearsalProtocolRecoveryFixture(t)
			legacy := rehearsalProtocolLegacyInput(t, current)
			draft := rehearsalProtocolRecoveryDraft(t, st, legacy, completed)
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			got, err := buildPipelineArcRehearsalInput(st, cfg)
			publicationArtifactMust(t, err)
			if got.InputDigest != legacy.InputDigest || got.ProtocolDigest != legacy.ProtocolDigest {
				t.Fatal("CLI upgrade discarded the exact prior protocol/draft")
			}
			loaded, _, err := st.LoadArcRehearsalDraftForInput(got.InputDigest)
			publicationArtifactMust(t, err)
			if loaded == nil || loaded.DraftDigest != draft.DraftDigest {
				t.Fatal("recovery replaced the original Architect draft")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("read-only protocol selection rewrote history")
			}
		})
	}
}

func TestPipelineRehearsalProtocolRejectsAmbiguityAndCorruptHistory(t *testing.T) {
	for _, mode := range []string{"ambiguous", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			st, cfg, current := rehearsalProtocolRecoveryFixture(t)
			legacy := rehearsalProtocolLegacyInput(t, current)
			draft := rehearsalProtocolRecoveryDraft(t, st, legacy, false)
			if mode == "ambiguous" {
				rehearsalProtocolRecoveryDraft(t, st, current, false)
			} else {
				path := filepath.Join(st.Dir(), store.ArcRehearsalRoot, "by_input", strings.TrimPrefix(legacy.InputDigest, "sha256:")+".json")
				raw, err := os.ReadFile(path)
				publicationArtifactMust(t, err)
				changed := bytes.ReplaceAll(raw, []byte(draft.DraftDigest), []byte("sha256:"+strings.Repeat("f", 64)))
				if bytes.Equal(raw, changed) {
					t.Fatal("test did not corrupt the selected draft reference")
				}
				publicationArtifactMust(t, os.WriteFile(path, changed, 0o600))
			}
			if _, err := buildPipelineArcRehearsalInput(st, cfg); err == nil {
				t.Fatal("invalid history silently started a new protocol")
			}
		})
	}
}

func TestPipelineRehearsalProtocolDoesNotReuseChangedSources(t *testing.T) {
	st, cfg, current := rehearsalProtocolRecoveryFixture(t)
	legacy := rehearsalProtocolLegacyInput(t, current)
	rehearsalProtocolRecoveryDraft(t, st, legacy, false)
	characters, err := st.Characters.Load()
	publicationArtifactMust(t, err)
	characters[0].InitialState.KnownFacts = append(characters[0].InitialState.KnownFacts, "新的作者初始资料")
	publicationArtifactMust(t, st.Characters.Save(characters))
	got, err := buildPipelineArcRehearsalInput(st, cfg)
	publicationArtifactMust(t, err)
	if got.ProtocolDigest != current.ProtocolDigest || got.InputDigest == current.InputDigest || got.SourceRoot == current.SourceRoot {
		t.Fatal("changed source was laundered into an old draft")
	}
}

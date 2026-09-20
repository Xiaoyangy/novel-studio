package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func rehearsalScopePreviousDeltaInput(t *testing.T, current domain.ArcRehearsalInput) domain.ArcRehearsalInput {
	t.Helper()
	previous, err := agents.ReviewDeltaArcRehearsalProtocolDigest()
	publicationArtifactMust(t, err)
	current.ProtocolDigest = previous
	input, err := domain.FinalizeArcRehearsalInput(current)
	publicationArtifactMust(t, err)
	return input
}

func TestPipelineRehearsalContractScopePreservesExactDeltaDraftAndNonreadyReport(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "nonready-report"}[completed], func(t *testing.T) {
			st, cfg, current := rehearsalProtocolRecoveryFixture(t)
			previous := rehearsalScopePreviousDeltaInput(t, current)
			draft := rehearsalProtocolRecoveryDraft(t, st, previous, false)
			var savedReport *domain.ArcRehearsalReport
			if completed {
				body := draft.Body
				body.ContractChecks = append([]domain.ArcRehearsalContractCheck(nil), body.ContractChecks...)
				body.ContractChecks[0].Assessment = "unresolved"
				report, err := domain.FinalizeArcRehearsalReport(previous, draft, domain.ArcRehearsalReport{Body: body, Call: domain.ArcRehearsalCall{Role: "world_arbiter", Provider: "test", Model: "no-provider", UsageIDs: []string{"review"}, ToolCallID: "review", ResponseDigest: projectAllCmdTestDigest("review")}})
				publicationArtifactMust(t, err)
				publicationArtifactMust(t, st.SaveArcRehearsalReport(previous, draft, report))
				savedReport = &report
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			got, err := buildPipelineArcRehearsalInput(store.NewStore(st.Dir()), cfg)
			publicationArtifactMust(t, err)
			if got.InputDigest != previous.InputDigest || got.ProtocolDigest != previous.ProtocolDigest || got.InputDigest == current.InputDigest {
				t.Fatal("scope upgrade replaced the frozen delta input")
			}
			loaded, _, err := st.LoadArcRehearsalDraftForInput(got.InputDigest)
			publicationArtifactMust(t, err)
			if loaded == nil || loaded.DraftDigest != draft.DraftDigest {
				t.Fatal("scope upgrade lost original draft")
			}
			if savedReport != nil {
				report, err := st.LoadVerifiedArcRehearsalForInput(got.InputDigest)
				publicationArtifactMust(t, err)
				if report == nil || report.ReportDigest != savedReport.ReportDigest || report.ReadyForDetail || requirePipelineArcRehearsalReady(report) == nil {
					t.Fatal("new guidance reinterpreted or authorized an old nonready report")
				}
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("read-only recovery changed exact source or report bytes")
			}
		})
	}
}

func TestPipelineRehearsalContractScopeRejectsAmbiguousOrCorruptDeltaHistory(t *testing.T) {
	for _, mode := range []string{"ambiguous-current", "ambiguous-full-body", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			st, cfg, current := rehearsalProtocolRecoveryFixture(t)
			previous := rehearsalScopePreviousDeltaInput(t, current)
			rehearsalProtocolRecoveryDraft(t, st, previous, false)
			switch mode {
			case "ambiguous-current":
				rehearsalProtocolRecoveryDraft(t, st, current, false)
			case "ambiguous-full-body":
				rehearsalProtocolRecoveryDraft(t, st, rehearsalProtocolLegacyInput(t, current), false)
			case "corrupt":
				path := filepath.Join(st.Dir(), store.ArcRehearsalRoot, "by_input", strings.TrimPrefix(previous.InputDigest, "sha256:")+".json")
				publicationArtifactMust(t, os.WriteFile(path, []byte(`{"corrupt":true}`), 0600))
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if _, err := buildPipelineArcRehearsalInput(st, cfg); err == nil {
				t.Fatal("invalid history silently chose new guidance")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("failed recovery wrote files")
			}
		})
	}
}

func TestPipelineRehearsalContractScopeDoesNotReuseChangedSources(t *testing.T) {
	st, cfg, current := rehearsalProtocolRecoveryFixture(t)
	rehearsalProtocolRecoveryDraft(t, st, rehearsalScopePreviousDeltaInput(t, current), false)
	characters, err := st.Characters.Load()
	publicationArtifactMust(t, err)
	characters[0].InitialState.KnownFacts = append(characters[0].InitialState.KnownFacts, "新作者源须重新预演")
	publicationArtifactMust(t, st.Characters.Save(characters))
	got, err := buildPipelineArcRehearsalInput(st, cfg)
	publicationArtifactMust(t, err)
	if got.ProtocolDigest != current.ProtocolDigest || got.InputDigest == current.InputDigest || got.SourceRoot == current.SourceRoot {
		t.Fatal("changed source borrowed old delta authority")
	}
}

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type rehearsalReviewCorrectionModel struct {
	base         arcRehearsalFakeModel
	reviewCalls  int
	sawRejection bool
}

func (*rehearsalReviewCorrectionModel) SupportsTools() bool { return true }
func (m *rehearsalReviewCorrectionModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var payload struct {
		Draft *domain.ArcRehearsalDraft `json:"architect_draft"`
	}
	for _, message := range messages {
		if message.Role == agentcore.RoleUser {
			if err := decodeArcRehearsalFakePayload([]byte(message.TextContent()), &payload); err != nil {
				return nil, err
			}
		}
		if message.Role == agentcore.RoleTool {
			raw, _ := json.Marshal(message)
			m.sawRejection = m.sawRejection || strings.Contains(string(raw), "review cannot rewrite declared execution dependencies")
		}
	}
	if payload.Draft != nil {
		m.reviewCalls++
	}
	rewrite := payload.Draft != nil && m.reviewCalls == 1
	m.base.bodyFactory = func(input domain.ArcRehearsalInput) domain.ArcRehearsalBody {
		body := arcRehearsalTestBody(input, false)
		if rewrite {
			body.MaterialChecks[0].CapabilityRequirements[0].Key = "rewritten_read_ledger"
		}
		return body
	}
	return m.base.Generate(ctx, messages, specs, opts...)
}
func (m *rehearsalReviewCorrectionModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}

func TestArcRehearsalReviewSubmissionCanCorrectBeforeSuccessAndResumeDraft(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			st, input := arcRehearsalTestInput(t)
			hooks := ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}}
			var originalDraft *domain.ArcRehearsalDraft
			if resume {
				first := &arcRehearsalFakeModel{failReview: true}
				models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", first)}
				if _, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, hooks); !errors.Is(err, context.Canceled) {
					t.Fatalf("draft interruption: %v", err)
				}
				var err error
				originalDraft, _, err = store.NewStore(st.Dir()).LoadArcRehearsalDraftForInput(input.InputDigest)
				selectionMust(t, err)
				if originalDraft == nil || first.calls != 2 {
					t.Fatal("did not retain the first actual Architect draft")
				}
			}
			before := arcRehearsalSourceFiles(t, st.Dir())
			model := &rehearsalReviewCorrectionModel{}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
			report, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, hooks)
			selectionMust(t, err)
			wanted := []string{"architect", "world_arbiter", "world_arbiter"}
			if resume {
				wanted = wanted[1:]
			}
			if report == nil || !report.ReadyForDetail || !model.sawRejection || model.reviewCalls != 2 || !reflect.DeepEqual(model.base.roles, wanted) {
				t.Fatalf("review did not receive and correct pre-submit failure: roles=%v seen=%v", model.base.roles, model.sawRejection)
			}
			if report.Call.ToolCallID != fmt.Sprintf("arc-call-%d", len(wanted)) {
				t.Fatal("report bound a rejected tool call instead of the actual accepted response")
			}
			loaded, bound, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
			selectionMust(t, err)
			if loaded == nil || bound == nil || bound.InputDigest != input.InputDigest || loaded.ReportDigest != report.ReportDigest {
				t.Fatal("corrected report did not round-trip")
			}
			if originalDraft != nil && originalDraft.DraftDigest != report.DraftDigest {
				t.Fatal("resume rewrote the saved Architect draft")
			}
			if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
				t.Fatal("submission retry modified source/canon")
			}
		})
	}
}

func rehearsalReviewSubmitDraft(t *testing.T, input domain.ArcRehearsalInput) domain.ArcRehearsalDraft {
	t.Helper()
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: arcRehearsalTestBody(input, false), Call: domain.ArcRehearsalCall{Role: "architect", Provider: "test", Model: "fixture", UsageIDs: []string{"actual-fixture-draft"}, ToolCallID: "draft", ResponseDigest: "sha256:" + strings.Repeat("a", 64)}})
	selectionMust(t, err)
	return draft
}

func TestArcRehearsalReviewToolRejectsRewriteAndTamperedHostDraftWithoutSuccess(t *testing.T) {
	_, input := arcRehearsalTestInput(t)
	draft := rehearsalReviewSubmitDraft(t, input)
	original, _ := json.Marshal(draft)
	changed := rehearsalCapabilityClone(t, draft.Body)
	changed.MaterialChecks[0].CapabilityRequirements[0].Key = "rewritten_key"
	raw, _ := json.Marshal(changed)
	// Architect has no prior draft constraints; this is valid standalone input.
	architect := &submitArcRehearsalTool{input: input}
	if _, err := architect.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	tool := &submitArcRehearsalTool{input: input, draft: &draft}
	if _, err := tool.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "review cannot rewrite") || tool.body != nil {
		t.Fatalf("review rewrite succeeded before finalize: %v", err)
	}
	if _, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: changed}); err == nil || !strings.Contains(err.Error(), "review cannot rewrite") {
		t.Fatal("finalize lost its repeated defensive check")
	}
	for _, tamper := range []func(*domain.ArcRehearsalDraft){
		func(d *domain.ArcRehearsalDraft) { d.DraftDigest = "sha256:" + strings.Repeat("f", 64) },
		func(d *domain.ArcRehearsalDraft) { d.InputDigest = "sha256:" + strings.Repeat("f", 64) },
		func(d *domain.ArcRehearsalDraft) { d.Call.Role = "world_arbiter" },
	} {
		copy := draft
		tamper(&copy)
		tool := &submitArcRehearsalTool{input: input, draft: &copy}
		valid, _ := json.Marshal(draft.Body)
		if _, err := tool.Execute(context.Background(), valid); err == nil || tool.body != nil {
			t.Fatal("tampered host draft granted successful submission")
		}
	}
	after, _ := json.Marshal(draft)
	if string(original) != string(after) {
		t.Fatal("review validation modified original draft/call metadata")
	}
}

func TestArcRehearsalReviewPreservesOriginalKeysButAllowsValidAdditions(t *testing.T) {
	st, input, _ := rehearsalCapabilityFixture(t)
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: rehearsalCapabilityBody(input), Call: rehearsalReviewSubmitDraft(t, input).Call})
	selectionMust(t, err)
	review := rehearsalCapabilityClone(t, draft.Body)
	review.MaterialChecks[0].CapabilityRequirements = append(review.MaterialChecks[0].CapabilityRequirements, domain.ArcRehearsalCapabilityRequirementV1{Key: "hold_ledger", Kind: "resource_use", ActorRef: input.CharacterObservations[0].AgentID, ResourceRefs: []string{"res_1111111111111111"}})
	selectionMust(t, domain.ValidateArcRehearsalReviewBody(input, draft.Body, review))
	// Independent display order is not a newly frozen chronology constraint.
	review.MaterialChecks[0].CapabilityRequirements[0], review.MaterialChecks[0].CapabilityRequirements[1] = review.MaterialChecks[0].CapabilityRequirements[1], review.MaterialChecks[0].CapabilityRequirements[0]
	selectionMust(t, domain.ValidateArcRehearsalReviewBody(input, draft.Body, review))
	tool := &submitArcRehearsalTool{input: input, draft: &draft}
	raw, _ := json.Marshal(review)
	_, err = tool.Execute(context.Background(), raw)
	selectionMust(t, err)
	call := domain.ArcRehearsalCall{Role: "world_arbiter", Provider: "test", Model: "fixture", UsageIDs: []string{"actual-fixture-review"}, ToolCallID: "review", ResponseDigest: "sha256:" + strings.Repeat("b", 64)}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: *tool.body, Call: call})
	selectionMust(t, err)
	selectionMust(t, st.SaveArcRehearsalReport(input, draft, report))
	loaded, bound, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if loaded == nil || bound == nil || !loaded.ReadyForDetail || bound.InputDigest != input.InputDigest || loaded.DraftDigest != draft.DraftDigest {
		t.Fatal("additive review did not preserve the original input/draft binding")
	}
	for name, mutate := range map[string]func(*domain.ArcRehearsalBody){
		"old-key": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].Key = "renamed_measure"
		},
		"old-kind": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].Kind = "resource_use" },
		"old-actor": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ActorRef = input.CharacterObservations[1].AgentID
		},
		"old-recipient": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[5].CapabilityRequirements[0].RecipientRef = input.CharacterObservations[0].AgentID
		},
		"old-resource": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{rehearsalPaperID}
		},
		"old-mechanism": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].MechanismRefs = nil },
		"old-depends-on": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].DependsOn = append(b.MaterialChecks[6].CapabilityRequirements[0].DependsOn, "fuel_measure")
		},
		"old-artifact-ref": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].ArtifactRef = "draft_note"
		},
		"old-material-inputs": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[3].CapabilityRequirements[0].MaterialInputs[0].Amount = 2
		},
		"removed-old-key": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[0].CapabilityRequirements = b.MaterialChecks[0].CapabilityRequirements[:1]
			b.MaterialChecks[0].RequiresReadable = false
		},
		"duplicate-new-key": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[0].CapabilityRequirements[0].Key = "read_ledger" },
		"new-later-dependency": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[0].CapabilityRequirements[0].DependsOn = []string{"sign_note"}
		},
		"new-unknown-resource": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[0].CapabilityRequirements[0].ResourceRefs = []string{"res_9999999999999999"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := rehearsalCapabilityClone(t, review)
			mutate(&bad)
			tool := &submitArcRehearsalTool{input: input, draft: &draft}
			raw, _ := json.Marshal(bad)
			if _, err := tool.Execute(context.Background(), raw); err == nil || tool.body != nil {
				t.Fatal("changed old dependency or invalid addition was accepted by tool")
			}
			if _, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: bad, Call: call}); err == nil {
				t.Fatal("finalize lost full validation or old-key preservation")
			}
			candidate := report
			candidate.Body = bad
			if err := st.SaveArcRehearsalReport(input, draft, candidate); err == nil {
				t.Fatal("store bypassed the same defensive validator")
			}
		})
	}
}

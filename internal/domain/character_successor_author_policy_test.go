package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func successorAuthorPolicyFixture() CharacterAgentSuccessorPlan {
	return CharacterAgentSuccessorPlan{
		Version: CharacterAgentSuccessorPlanVersion, ParentGenerationID: "pg2_parent", BaseCanonChapter: 0,
		TriggerChapter: 1, ArcFirstChapter: 1, ArcLastChapter: 1, BookLastChapter: 3,
		ArbitrationDigest: "sha256:arbiter", AcceptedCanonRoot: "sha256:canon", EndingDirection: "legacy ending",
		NonNegotiables: []string{"preserve author constraint"}, HardContractConflicts: []string{"route is unavailable"},
		RevisedChapters:  []OutlineEntry{{Chapter: 1, Title: "Changed route", CoreEvent: "Take the remaining road", Hook: "The gate is closed", Scenes: []string{"Negotiate at the gate"}}},
		ArchitectSummary: "Change only the soft route.", CreatedAt: "2026-09-14T00:00:00Z",
	}
}

func TestCharacterAgentSuccessorAuthorPolicyPreservesLegacyGolden(t *testing.T) {
	const original = `{"version":"character-agent-successor-plan.v1","parent_generation_id":"pg2_parent","base_canon_chapter":0,"trigger_chapter":1,"arc_first_chapter":1,"arc_last_chapter":1,"book_last_chapter":3,"arbitration_digest":"sha256:arbiter","accepted_canon_root":"sha256:canon","ending_direction":"legacy ending","non_negotiables":["preserve author constraint"],"hard_contract_conflicts":["route is unavailable"],"revised_chapters":[{"chapter":1,"title":"Changed route","core_event":"Take the remaining road","hook":"The gate is closed","scenes":["Negotiate at the gate"]}],"architect_summary":"Change only the soft route.","created_at":"2026-09-14T00:00:00Z","digest":""}`
	plan, err := FinalizeCharacterAgentSuccessorPlan(successorAuthorPolicyFixture())
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(original)))
	if plan.Digest != wantDigest {
		t.Fatalf("empty new marker changed legacy digest: %s want %s", plan.Digest, wantDigest)
	}
	plan.Digest = ""
	raw, err := json.Marshal(plan)
	if err != nil || string(raw) != original {
		t.Fatalf("legacy wire bytes changed: %s %v", raw, err)
	}
	plan.EndingDirection = ""
	if _, err := FinalizeCharacterAgentSuccessorPlan(plan); err == nil {
		t.Fatal("legacy successor no longer requires ending direction")
	}
}

func TestCharacterAgentSuccessorAuthorPolicyAllowsOnlyMarkedSoftEnding(t *testing.T) {
	plan := successorAuthorPolicyFixture()
	plan.AuthorContractPolicy = AuthorSourcesPolicyV1
	plan.EndingDirection = ""
	finalized, err := FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil || finalized.AuthorContractPolicy != AuthorSourcesPolicyV1 || finalized.EndingDirection != "" {
		t.Fatalf("source-marked soft ending remained mandatory: %+v %v", finalized, err)
	}
	if !reflect.DeepEqual(finalized.NonNegotiables, plan.NonNegotiables) || !reflect.DeepEqual(finalized.HardContractConflicts, plan.HardContractConflicts) {
		t.Fatal("soft ending mode altered true hard constraints")
	}
	raw, err := json.Marshal(finalized)
	if err != nil {
		t.Fatal(err)
	}
	var loaded CharacterAgentSuccessorPlan
	if err := json.Unmarshal(raw, &loaded); err != nil || !reflect.DeepEqual(loaded, finalized) {
		t.Fatalf("author marker disappeared in JSON: %v", err)
	}
	for _, mode := range []string{"unknown_policy", "spaced_policy", "missing_contracts", "empty_contracts", "missing_conflict", "incomplete_chapters", "chapter_order"} {
		t.Run(mode, func(t *testing.T) {
			bad := plan
			bad.RevisedChapters = append([]OutlineEntry(nil), plan.RevisedChapters...)
			switch mode {
			case "unknown_policy":
				bad.AuthorContractPolicy = "author-sources.v999"
			case "spaced_policy":
				bad.AuthorContractPolicy = " " + AuthorSourcesPolicyV1
			case "missing_contracts":
				bad.NonNegotiables = nil
			case "empty_contracts":
				bad.NonNegotiables = []string{" "}
			case "missing_conflict":
				bad.HardContractConflicts = nil
			case "incomplete_chapters":
				bad.RevisedChapters = nil
			case "chapter_order":
				bad.RevisedChapters[0].Chapter = 2
			}
			if _, err := FinalizeCharacterAgentSuccessorPlan(bad); err == nil {
				t.Fatalf("%s relaxed unrelated successor constraints", mode)
			}
		})
	}
	plan.EndingDirection = "Optional soft source hint"
	withHint, err := FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil || strings.Contains(strings.Join(withHint.NonNegotiables, "\n"), plan.EndingDirection) {
		t.Fatalf("source hint was promoted to hard contract: %+v %v", withHint, err)
	}
}

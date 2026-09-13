package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCharacterAgentSuccessorAuthorPolicyStoreRoundTripAndTamper(t *testing.T) {
	for _, mode := range []string{"empty_soft_ending", "hint_then_deleted_marker", "unknown_marker"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			plan := domain.CharacterAgentSuccessorPlan{
				ParentGenerationID: "pg2_author_parent", BaseCanonChapter: 0, TriggerChapter: 1, ArcFirstChapter: 1, ArcLastChapter: 1, BookLastChapter: 3,
				ArbitrationDigest: "sha256:arbitration", AcceptedCanonRoot: "sha256:canon", AuthorContractPolicy: domain.AuthorSourcesPolicyV1,
				NonNegotiables: []string{"不得修改作者限制。"}, HardContractConflicts: []string{"当前路径不可行。"}, ArchitectSummary: "只改本章柔性路径。",
				RevisedChapters: []domain.OutlineEntry{{Chapter: 1, Title: "转向", CoreEvent: "换一条仍然可走的路", Hook: "发现新阻碍", Scenes: []string{"现场确认路径"}}},
			}
			if mode == "hint_then_deleted_marker" {
				plan.EndingDirection = "未来可能和解，这是软方向"
			}
			var err error
			plan, err = domain.FinalizeCharacterAgentSuccessorPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.CharacterAgents.SaveSuccessorPlan(plan, true); err != nil {
				t.Fatal(err)
			}
			loaded, err := NewStore(st.Dir()).CharacterAgents.LoadCurrentSuccessorPlan()
			if err != nil || loaded == nil || !reflect.DeepEqual(*loaded, plan) {
				t.Fatalf("Store lost marker/soft hint across reload: %+v %v", loaded, err)
			}
			before, err := DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if err := st.CharacterAgents.SaveSuccessorPlan(plan, true); err != nil {
				t.Fatal(err)
			}
			after, err := DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatalf("idempotent marked plan changed bytes: %v", err)
			}
			changed := plan
			if mode == "unknown_marker" {
				changed.AuthorContractPolicy = "model-invented-policy"
			} else {
				changed.AuthorContractPolicy = ""
			}
			path := characterAgentSuccessorPlanPath(plan.ParentGenerationID, plan.Digest)
			if err := newIO(st.Dir()).WriteJSON(path, changed); err != nil {
				t.Fatal(err)
			}
			corrupt, err := os.ReadFile(filepath.Join(st.Dir(), path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(st.Dir()).CharacterAgents.LoadCurrentSuccessorPlan(); err == nil {
				t.Fatal("marker tamper/downgrade silently changed successor constraint policy")
			}
			afterCorrupt, err := os.ReadFile(filepath.Join(st.Dir(), path))
			if err != nil || string(corrupt) != string(afterCorrupt) {
				t.Fatalf("read repaired modified immutable plan: %v", err)
			}
		})
	}
}

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCharacterExecutionContextKeepsLargeCanonicalAuthorityWithoutModelPacket(t *testing.T) {
	st := newPhaseTestStore(t)
	state, _ := installPlanningContextAccessProjectAll(t, st, 3, "independent-large-context")
	state.CumulativeState = append(state.CumulativeState, domain.ProjectedPlanningStateFactV2{Category: "character_state", StableID: "large-history", Subject: "actor", Field: "history", Value: strings.Repeat("已验真历史，不是要重复发给模型的文本。", 12000), ThroughChapter: 2})
	var err error
	state.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), projectAllStateContextPath)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "")
	view, err := tool.PrepareCharacterAgentExecutionContext(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if view.ProjectAllState == nil || !reflect.DeepEqual(state, *view.ProjectAllState) {
		t.Fatal("host authority was cropped or re-signed")
	}
	wantToken, _ := domain.ProjectedPlanningContextSourceTokenV2(state.ContextDigest)
	if view.ProjectAllSourceToken != wantToken || view.AccessSourceToken == "" {
		t.Fatal("source binding lost")
	}
	if err := consumePlanningContextAccessReceipt(st, 3, domain.PlanningContextAccessSimulate, []string{view.AccessSourceToken}); err != nil {
		t.Fatal(err)
	}
	if err := consumePlanningContextAccessReceipt(st, 3, domain.PlanningContextAccessSimulate, []string{view.AccessSourceToken}); err == nil {
		t.Fatal("one-shot access became reusable")
	}
	after, _ := os.ReadFile(path)
	if string(raw) != string(after) {
		t.Fatal("authoritative state changed")
	}
	if receipt, err := st.RAG.LoadLatestRAGFactReceipt(3); err != nil || receipt != nil {
		t.Fatalf("orchestration unexpectedly performed RAG: %v/%v", receipt, err)
	}
}

func TestCharacterExecutionContextRejectsWrongIdentityBeforeReceipt(t *testing.T) {
	for _, mode := range []string{"wrong_chapter", "foreign_process", "corrupt_state", "corrupt_partial", "render", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			st := newPhaseTestStore(t)
			_, _ = installPlanningContextAccessProjectAll(t, st, 1, "independent-reject")
			ctx := context.Background()
			chapter := 1
			switch mode {
			case "wrong_chapter":
				chapter = 2
			case "foreign_process", "render":
				lock, err := st.Runtime.InspectPipelineExecution()
				if err != nil || lock == nil {
					t.Fatal(err)
				}
				if mode == "foreign_process" {
					lock.ProcessID = os.Getpid() + 100000
				} else {
					lock.Mode = domain.PipelineExecutionRender
					lock.PlanDigest = "sha256:" + strings.Repeat("1", 64)
				}
				raw, _ := json.Marshal(lock)
				if err := os.WriteFile(filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt_state":
				if err := os.WriteFile(filepath.Join(st.Dir(), projectAllStateContextPath), []byte(`{"context_digest":"forged"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt_partial":
				if err := os.WriteFile(filepath.Join(st.Dir(), "drafts/01.plan.partial.json"), []byte(`{broken`), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := NewContextTool(st, References{}, "").PrepareCharacterAgentExecutionContext(ctx, chapter); err == nil {
				t.Fatal("invalid host context accepted")
			}
			if receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessSimulate); err != nil || receipt != nil {
				t.Fatalf("rejected context wrote a receipt: %v/%v", receipt, err)
			}
		})
	}
}

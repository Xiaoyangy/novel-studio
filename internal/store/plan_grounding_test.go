package store

import (
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPlanGroundingAuditImmutableConcurrentAndPathBound(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	input := domain.PlanGroundingInput{Policy: domain.PlanGroundingPolicyV1, ReviewProtocol: "sha256:" + strings.Repeat("1", 64), Plan: domain.ChapterPlan{Chapter: 1, Goal: "保持原选择"}}
	receipt, err := domain.FinalizePlanGroundingReceipt(input, domain.PlanGroundingVerdict{Pass: true})
	if err != nil {
		t.Fatal(err)
	}
	audit := domain.PlanGroundingAudit{Input: input, Receipt: receipt}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := NewStore(st.Dir()).SavePlanGroundingAudit(audit); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	loaded, err := NewStore(st.Dir()).LoadPlanGroundingAudit(receipt.InputDigest)
	if err != nil || loaded == nil {
		t.Fatal(err)
	}
	if _, err := st.LoadPlanGroundingAudit("../../outside"); err == nil {
		t.Fatal("unsafe digest path accepted")
	}
	changed := audit
	changed.Input.Plan.Goal = "偷偷增加新剧情"
	if err := st.SavePlanGroundingAudit(changed); err == nil {
		t.Fatal("changed input inherited receipt")
	}
	changed.Receipt, err = domain.FinalizePlanGroundingReceipt(changed.Input, domain.PlanGroundingVerdict{Pass: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := planGroundingAuditPath(receipt.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.io.WriteJSON(path, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadPlanGroundingAudit(receipt.InputDigest); err == nil {
		t.Fatal("valid receipt accepted at another input's path")
	}
}

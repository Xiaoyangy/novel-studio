package agents

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestProjectAllPartialResumePreservesFreshPromptBytes(t *testing.T) {
	packet := json.RawMessage(`{"message":"并调用 plan_structure，然后用 plan_details 分批 finalize","saved":"原值"}`)
	boundary := ProjectedArcBoundary{Volume: 1, Arc: 1, Title: "首弧", FirstChapter: 1, LastChapter: 10, BookLastChapter: 110, Goal: "完成首弧"}
	fresh := projectAllPlannerPrompt(2, packet, boundary, "全书正文目标总量500000字、共110章。", nil)
	// Golden captured from the pre-resume RunProjectedChapterPlanning format.
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(fresh))); got != "8bd1c2c06a5aa02dcf12d817e6900b54397844974f6ce1d143e645fc42826639" {
		t.Fatalf("fresh host prompt changed: %s", got)
	}
	repair := projectAllPlannerPrompt(2, packet, boundary, "全书正文目标总量500000字、共110章。", errors.New("missing initial_state"))
	if !strings.Contains(repair, string(packet)) {
		t.Fatal("host dispatch change rewrote source JSON")
	}
}

func TestProjectAllPartialRepairKeepsActualErrorAndContext(t *testing.T) {
	packet := json.RawMessage(`{"structure_source_status":"ready","saved_core":{"project_promise":"paid unchanged"},"fields_present":["causal_beats"]}`)
	cause := errors.New("causal_simulation.initial_state[0].action_tendency missing")
	boundary := ProjectedArcBoundary{Volume: 1, Arc: 1, Title: "首弧", FirstChapter: 1, LastChapter: 10, BookLastChapter: 110, Goal: "完成首弧"}
	prompt := projectAllPlannerPrompt(2, packet, boundary, "全书正文目标总量500000字、共110章。", cause)
	for _, required := range []string{string(packet), cause.Error(), "第2章", "不要重做 plan_structure", "不要重发整份计划", "不要重新推演角色", "保留所有无关字段", "plan_details(finalize=true)"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("recovery lost %q", required)
		}
	}
	if strings.Contains(prompt, "并调用 plan_structure") {
		t.Fatal("partial recovery told planner to reauthor structure")
	}
	for _, required := range []string{"本弧范围第1-10章", "全书正文目标总量500000字、共110章。", "只有第110章才是全书末章", "craft receipt", "fact receipt", "render_capacity", "predecessor_contract", "carried-forward"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("recovery dropped host boundary %q", required)
		}
	}
}

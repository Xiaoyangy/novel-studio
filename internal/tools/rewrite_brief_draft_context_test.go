package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestLoadRewriteBriefHumanSupplements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reviews", "02_rewrite_brief.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# brief\n\n## 必须修正\n\n- 评审示例。\n\n## 当前候选稿人工验收补充（现在）\n\n- 采购只留一个票据异常。\n- 禁止随机下雨。\n\n## 验收条件\n\n- 通过。\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadRewriteBriefHumanSupplements(dir, 2)
	if len(got) != 1 || !strings.Contains(got[0], "采购只留一个票据异常") || strings.Contains(got[0], "评审示例") {
		t.Fatalf("human supplements = %#v", got)
	}
}

func TestCompactDraftRewriteBriefKeepsHumanRulesAndDropsVerboseEvidence(t *testing.T) {
	issues := []domain.ConsistencyIssue{
		{Type: "aesthetic", Severity: "warning", Description: "对白连续承担说明功能。后面是很长的示例动作。", Evidence: "不要注入正文"},
		{Type: "aesthetic", Severity: "warning", Description: "采购段像清单。示例：逐笔列票。"},
		{Type: "pacing", Severity: "warning", Description: "章末太快。示例：突然下雨。"},
		{Type: "hook", Severity: "warning", Description: "第四条不应进入 draft profile。"},
	}
	container := map[string]any{"rewrite_brief": map[string]any{
		"reason":                       "正式复审未通过",
		"issues":                       issues,
		"mechanical_gate":              map[string]any{"rewrite_focus": []string{"随机绊桌腿", "飞虫停灯面"}},
		"human_acceptance_supplements": []string{"禁止随机下雨；采购只留一个票据异常。"},
	}}
	compactDraftRewriteBrief(container)
	brief := container["rewrite_brief"].(map[string]any)
	if _, exists := brief["mechanical_gate"]; exists {
		t.Fatal("draft rewrite brief retained verbose mechanical gate")
	}
	gotIssues, ok := brief["issues"].([]map[string]string)
	if !ok || len(gotIssues) != 3 || strings.Contains(gotIssues[0]["problem"], "示例动作") {
		t.Fatalf("compacted issues = %#v", brief["issues"])
	}
	if !strings.Contains(strings.Join(brief["human_acceptance_supplements"].([]string), "\n"), "采购只留一个票据异常") {
		t.Fatalf("human supplement missing: %#v", brief)
	}
}

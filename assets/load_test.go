package assets

import (
	"strings"
	"testing"
)

func TestLoadReferencesIncludesProductionPlaybook(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.ProductionPlaybook, "章节任务单") {
		t.Fatalf("expected production playbook to be loaded")
	}
	if !strings.Contains(bundle.References.ProductionPlaybook, "质量债务") {
		t.Fatalf("expected production playbook to include quality debt rules")
	}
}

func TestLoadReferencesIncludesHumanFeelCraft(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.HumanFeelCraft, "同桌是只假装高冷的猫") {
		t.Fatalf("expected human feel craft source to be loaded")
	}
	if !strings.Contains(bundle.References.HumanFeelCraft, "物件回扣") {
		t.Fatalf("expected human feel craft to include object callback rules")
	}
}

func TestLoadReferencesIncludesCharacterAndEmotionalCraft(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.CharacterBuilding, "人物塑造原则") {
		t.Fatalf("expected character building reference to be loaded")
	}
	if !strings.Contains(bundle.References.EmotionalNarrativeCraft, "情感叙事与人物推进写法") {
		t.Fatalf("expected emotional narrative craft reference to be loaded")
	}
	if !strings.Contains(bundle.References.EmotionalNarrativeCraft, "长循环处理规则") {
		t.Fatalf("expected emotional narrative craft to define loop handling")
	}
}

func TestLoadReferencesIncludesFictionParagraphing(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.FictionParagraphing, "小说正文分段规范") {
		t.Fatalf("expected fiction paragraphing reference to be loaded")
	}
	if !strings.Contains(bundle.References.FictionParagraphing, "文字墙候选") {
		t.Fatalf("expected fiction paragraphing reference to include long paragraph handling")
	}
}

func TestLoadReferencesIncludesWritingTechniquesDigest(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "19 篇写作技巧文章") {
		t.Fatalf("expected writing techniques digest source count to be loaded")
	}
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "中文标点") {
		t.Fatalf("expected writing techniques digest to include punctuation rules")
	}
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "过渡章") {
		t.Fatalf("expected writing techniques digest to include article-level extraction")
	}
}

func TestLoadReferencesIncludesRAGWritingGuidelines(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.RAGWritingGuidelines, "retrieval_trace") {
		t.Fatalf("expected RAG writing guidelines to mention retrieval trace")
	}
	if !strings.Contains(bundle.References.RAGWritingGuidelines, "弱召回") {
		t.Fatalf("expected RAG writing guidelines to define weak recall handling")
	}
}

func TestLoadReferencesIncludesWebReferenceGuidelines(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.WebReferenceGuidelines, "web_reference_brief") {
		t.Fatalf("expected web reference guidelines to mention web_reference_brief")
	}
	if !strings.Contains(bundle.References.WebReferenceGuidelines, "热梗") {
		t.Fatalf("expected web reference guidelines to define trend language handling")
	}
}

func TestWritingPromptsRemainProjectNeutral(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.Prompts.Writer, "章节目标以本轮 task 为最高优先级") {
		t.Fatal("writer prompt must pin every planning tool to the task chapter")
	}
	if !strings.Contains(bundle.Prompts.Planner, "章节号以本轮 task 为最高优先级") {
		t.Fatal("planner prompt must pin every planning tool to the task chapter")
	}

	combined := strings.Join([]string{
		bundle.Prompts.Planner,
		bundle.Prompts.Writer,
		bundle.Prompts.Drafter,
		bundle.Prompts.Editor,
	}, "\n")
	for _, leaked := range []string{
		"许闻溪", "梁渡", "夏岚", "傅行简", "程棠",
		"澄光生活", "溪流助手", "江烬", "阴司银行", "黑伞先生",
	} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("builtin writing prompts leaked project-specific term %q", leaked)
		}
	}
}

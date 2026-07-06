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

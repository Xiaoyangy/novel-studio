package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRewriteFactIdentityNormalizesQuoteGlyphsWithoutChangingWords(t *testing.T) {
	variants := []string{
		"章末贺骁只问‘什么货’；不得写已同意。",
		"章末贺骁只问“什么货”；不得写已同意。",
		"章末贺骁只问「什么货」；不得写已同意。",
		"章末贺骁只问'什么货'；不得写已同意。",
	}
	want := rewriteFactIdentity(variants[0])
	for _, variant := range variants[1:] {
		if rewriteFactIdentity(variant) != want {
			t.Fatalf("quote-only variant changed protected fact identity:\nwant=%q\nvariant=%q", want, rewriteFactIdentity(variant))
		}
	}
	if rewriteFactIdentity("章末保留 unmatched ' quote。") == rewriteFactIdentity(`章末保留 unmatched " quote。`) {
		t.Fatal("unpaired ASCII single quote must not be normalized")
	}
}

func TestCanonicalPreserveFactsDoesNotFuzzyDeduplicate(t *testing.T) {
	facts := []string{
		"两碗豆腐脑收入12元。",
		"两碗豆腐脑收入十二元。",
		"林澈先叫停，沈知遥后到场。",
		"沈知遥先到场，林澈后叫停。",
		"贺骁同意借车。",
		"贺骁未同意借车。",
	}
	got := canonicalPreserveFacts(nil, facts)
	if len(got) != len(facts) {
		t.Fatalf("number spelling, order, semantic or negation differences were collapsed: got=%#v", got)
	}
	for i := range facts {
		if got[i] != facts[i] {
			t.Fatalf("fact order/spelling changed at %d: got=%q want=%q", i, got[i], facts[i])
		}
	}
}

func TestCanonicalPreserveFactsKeepsAuthoritativeSpellingFirst(t *testing.T) {
	source := []string{"只点“少糖”的两碗豆腐脑。", "林澈先叫停。"}
	model := []string{"只点「少糖」的两碗豆腐脑。", "模型新增约束。"}
	got := canonicalPreserveFacts(source, model)
	want := []string{source[0], source[1], model[1]}
	if len(got) != len(want) {
		t.Fatalf("canonical facts length mismatch: got=%#v want=%#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical fact %d mismatch: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestFactCoveredByConstraintsUsesExactFactIdentity(t *testing.T) {
	fact := "只点“少糖”的两碗豆腐脑。"
	if !factCoveredByConstraints(fact, []string{"只点'少糖'的两碗豆腐脑。"}) {
		t.Fatal("paired quote variant should cover the same fact")
	}
	if factCoveredByConstraints(fact, []string{"前缀说明：只点“少糖”的两碗豆腐脑。"}) {
		t.Fatal("containing prose must not cover an exact protected fact")
	}
}

func TestIncomingRewriteFactCoverageRejectsParaphraseAtomically(t *testing.T) {
	canonical := "章末贺骁只问“喂”或“什么货”；借车保持未知。"
	incoming := []domain.ChapterRewriteFactCoverage{
		{Fact: canonical, SimulationEvidence: []string{"精确证据"}},
		{Fact: "章末贺骁只问喂或什么货；借车保持未知。", SimulationEvidence: []string{"删掉引号"}},
	}
	got, err := canonicalizeIncomingRewriteFactCoverage([]string{canonical}, incoming)
	if err == nil || !strings.Contains(err.Error(), "禁止删引号") {
		t.Fatalf("paraphrased fact was not rejected: got=%+v err=%v", got, err)
	}
	if got != nil {
		t.Fatalf("mixed valid/invalid submission must be atomic, got staged coverage: %+v", got)
	}
}

func TestIncomingRewriteFactCoverageCanonicalizesQuoteGlyphAndDeduplicates(t *testing.T) {
	canonical := "不得埋“异常渠道”“开始留意资金”的线索。"
	incoming := []domain.ChapterRewriteFactCoverage{
		{Fact: `不得埋"异常渠道""开始留意资金"的线索。`, SimulationEvidence: []string{"旧证据"}},
		{Fact: "不得埋「异常渠道」「开始留意资金」的线索。", SimulationEvidence: []string{"新证据"}},
	}
	got, err := canonicalizeIncomingRewriteFactCoverage([]string{canonical}, incoming)
	if err != nil || len(got) != 1 || got[0].Fact != canonical || len(got[0].SimulationEvidence) != 1 || got[0].SimulationEvidence[0] != "新证据" {
		t.Fatalf("canonical coverage mismatch: got=%+v err=%v", got, err)
	}
	if gaps := rewriteFactCoverageIntegrityGaps([]string{canonical}, append(got, got[0])); len(gaps) != 1 || !strings.Contains(gaps[0], "duplicate") {
		t.Fatalf("persisted duplicate was not revalidated: %+v", gaps)
	}
	if gaps := rewriteFactCoverageIntegrityGaps([]string{canonical}, []domain.ChapterRewriteFactCoverage{{Fact: "概括后的另一条事实"}}); len(gaps) != 1 || !strings.Contains(gaps[0], "unexpected") {
		t.Fatalf("persisted unexpected fact was not revalidated: %+v", gaps)
	}
}

func TestMergeRewriteFactCoverageKeepsCanonicalFactAcrossQuoteVariants(t *testing.T) {
	canonical := "不得埋“异常渠道”“开始留意资金”的线索。"
	existing := []domain.ChapterRewriteFactCoverage{{Fact: canonical, SimulationEvidence: []string{"旧证据"}}}
	incoming := []domain.ChapterRewriteFactCoverage{{Fact: "不得埋「异常渠道」「开始留意资金」的线索。", SimulationEvidence: []string{"新证据"}}}
	merged := mergeRewriteFactCoverage(existing, incoming)
	if len(merged) != 1 || merged[0].Fact != canonical || len(merged[0].SimulationEvidence) != 1 || merged[0].SimulationEvidence[0] != "新证据" {
		t.Fatalf("quote variant duplicated or rewrote canonical fact: %+v", merged)
	}
}

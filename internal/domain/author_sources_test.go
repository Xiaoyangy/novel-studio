package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func authorSourcesDomainFixture(t *testing.T) AuthorSourcesV1 {
	t.Helper()
	value, err := FinalizeAuthorSourcesV1(AuthorSourcesV1{Sources: []AuthorSourceV1{{ID: "author-1", Text: "  主角必须保持11岁，不得改成18岁。\r\n第二行不得删除否定。 \r\n \t\r\n第100章以前不得结局。\r\n\r\n不能凭空完成工作。  "}}})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAuthorSourcesFinalizePreservesOriginalTextAndRejectsDrift(t *testing.T) {
	text := " \n不得把11岁改成18岁。\r\n\r\n保留全部原文。 \t"
	catalog, err := FinalizeAuthorSourcesV1(AuthorSourcesV1{Sources: []AuthorSourceV1{{ID: "initial", Text: text}}})
	if err != nil || catalog.Sources[0].Text != text || catalog.Sources[0].Digest == "" || catalog.Digest == "" {
		t.Fatalf("source original bytes changed: %+v %v", catalog, err)
	}
	again, err := FinalizeAuthorSourcesV1(catalog)
	if err != nil || !reflect.DeepEqual(again, catalog) {
		t.Fatalf("catalog finalization is not idempotent: %v", err)
	}
	for _, mode := range []string{"text", "source_digest", "catalog_digest", "duplicate", "oversized", "bad_id", "invalid_utf8"} {
		t.Run(mode, func(t *testing.T) {
			bad := catalog
			bad.Sources = append([]AuthorSourceV1(nil), catalog.Sources...)
			switch mode {
			case "text":
				bad.Sources[0].Text = strings.Replace(text, "11", "18", 1)
			case "source_digest":
				bad.Sources[0].Digest = "sha256:bad"
			case "catalog_digest":
				bad.Digest = "sha256:bad"
			case "duplicate":
				bad.Sources = append(bad.Sources, bad.Sources[0])
			case "oversized":
				bad.Sources[0].Text = strings.Repeat("x", (1<<20)+1)
			case "bad_id":
				bad.Sources[0].ID = "author\x00id"
			case "invalid_utf8":
				bad.Sources[0].Text = string([]byte{0xff})
			}
			if _, err := FinalizeAuthorSourcesV1(bad); err == nil {
				t.Fatalf("%s source corruption accepted", mode)
			}
		})
	}
}

func TestAuthorSourcesAcceptsOriginalUnicodeAndSpacedSourceLabels(t *testing.T) {
	catalog, err := FinalizeAuthorSourcesV1(AuthorSourcesV1{Sources: []AuthorSourceV1{{ID: "project:人物视角.md", Text: "不得修改叙事视角。"}, {ID: "style rules.md", Text: "保持作者原文。"}}})
	if err != nil || catalog.Sources[0].ID != "project:人物视角.md" || catalog.Sources[1].ID != "style rules.md" {
		t.Fatalf("author source equality keys lost original labels: %+v %v", catalog, err)
	}
}

func TestAuthorSourceParagraphReferenceRequiresExplicitZeroOrIndex(t *testing.T) {
	for _, raw := range []string{`{"source_id":"author-1"}`, `{"source_id":"author-1","paragraph":null}`, `{"paragraph":0}`, `{"source_id":null,"paragraph":0}`, `{"source_id":"author-1","paragraph":0,"offset":2}`, `null`} {
		var ref AuthorSourceParagraphRefV1
		if err := json.Unmarshal([]byte(raw), &ref); err == nil {
			t.Fatalf("incomplete or ambiguous reference selected paragraph zero: %s", raw)
		}
	}
	var ref AuthorSourceParagraphRefV1
	if err := json.Unmarshal([]byte(`{"source_id":"project:人物视角.md","paragraph":0}`), &ref); err != nil || ref.SourceID != "project:人物视角.md" || ref.Paragraph != 0 {
		t.Fatalf("explicit legitimate zero paragraph failed: %+v %v", ref, err)
	}
}

func TestAuthorSourceParagraphsSplitOnlyBlankLines(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		text := " \t" + ending + "  不得删掉否定。" + ending + "第二行数字仍是100。  " + ending + "\t " + ending + "  完整第二段。  " + ending
		want := []string{"不得删掉否定。" + ending + "第二行数字仍是100。", "完整第二段。"}
		if got := AuthorSourceParagraphsV1(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("paragraph bytes for %q: got %#v want %#v", ending, got, want)
		}
	}
	if got := AuthorSourceParagraphsV1(" \n\t\r\n "); len(got) != 0 {
		t.Fatalf("blank input created fake paragraphs: %#v", got)
	}
}

func TestAuthorSourceCompassMaterializesExactWholeParagraphs(t *testing.T) {
	catalog := authorSourcesDomainFixture(t)
	compass := StoryCompass{EndingDirection: "模型建议以后建立新秩序", AuthorContracts: &CompassAuthorContractsV1{Policy: AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest, Refs: []AuthorSourceParagraphRefV1{{SourceID: "author-1", Paragraph: 0}, {SourceID: "author-1", Paragraph: 1}}}}
	materialized, err := MaterializeCompassAuthorContractsV1(compass, catalog)
	if err != nil {
		t.Fatal(err)
	}
	want := AuthorSourceParagraphsV1(catalog.Sources[0].Text)[:2]
	if !reflect.DeepEqual(materialized.NonNegotiables, want) || compass.NonNegotiables != nil {
		t.Fatal("host materialization changed the original paragraph or mutated input")
	}
	for _, mode := range []string{"substring", "negation", "number", "duplicate", "missing", "negative", "range", "catalog", "policy", "nil"} {
		t.Run(mode, func(t *testing.T) {
			bad := materialized
			binding := *materialized.AuthorContracts
			binding.Refs = append([]AuthorSourceParagraphRefV1(nil), binding.Refs...)
			bad.AuthorContracts = &binding
			bad.NonNegotiables = append([]string(nil), materialized.NonNegotiables...)
			switch mode {
			case "substring":
				bad.NonNegotiables[0] = "主角必须保持11岁"
			case "negation":
				bad.NonNegotiables[0] = strings.Replace(bad.NonNegotiables[0], "不得", "可以", 1)
			case "number":
				bad.NonNegotiables[1] = strings.Replace(bad.NonNegotiables[1], "100", "10", 1)
			case "duplicate":
				binding.Refs[1] = binding.Refs[0]
			case "missing":
				binding.Refs[0].SourceID = "not-author"
			case "negative":
				binding.Refs[0].Paragraph = -1
			case "range":
				binding.Refs[0].Paragraph = 99
			case "catalog":
				binding.SourcesDigest = "sha256:bad"
			case "policy":
				binding.Policy = "model-summary"
			case "nil":
				bad.AuthorContracts = nil
			}
			if _, err := MaterializeCompassAuthorContractsV1(bad, catalog); err == nil {
				t.Fatalf("%s was accepted as exact author contract", mode)
			}
		})
	}
	compass.AuthorContracts.Refs = nil
	empty, err := MaterializeCompassAuthorContractsV1(compass, catalog)
	if err != nil || len(empty.NonNegotiables) != 0 {
		t.Fatalf("empty selection invented mandatory contracts: %+v %v", empty, err)
	}
}

func TestAuthorSourceCompassJSONAndHardContractMode(t *testing.T) {
	catalog := authorSourcesDomainFixture(t)
	compass, err := MaterializeCompassAuthorContractsV1(StoryCompass{EndingDirection: "soft proposed ending", AuthorContracts: &CompassAuthorContractsV1{Policy: catalog.Policy, SourcesDigest: catalog.Digest, Refs: []AuthorSourceParagraphRefV1{{SourceID: "author-1", Paragraph: 0}}}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(compass)
	if err != nil {
		t.Fatal(err)
	}
	var loaded StoryCompass
	if err := json.Unmarshal(raw, &loaded); err != nil || !reflect.DeepEqual(loaded, compass) {
		t.Fatalf("binding disappeared in custom JSON decoder: %v", err)
	}
	if got := CompassHardContractsV1(loaded); !reflect.DeepEqual(got, compass.NonNegotiables) {
		t.Fatal("new mode promoted soft ending to a hard contract")
	}
	for _, ref := range BuildStoryContractRegistry(loaded) {
		if ref.Kind == StoryContractEnding {
			t.Fatal("structural registry promoted new soft ending")
		}
	}
	legacy := StoryCompass{EndingDirection: "old ending", NonNegotiables: []string{"old hard contract"}}
	if got := CompassHardContractsV1(legacy); !reflect.DeepEqual(got, []string{"old ending", "old hard contract"}) {
		t.Fatalf("legacy hard contract behavior changed: %#v", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["non_negotiables"] = []any{compass.NonNegotiables[0], 42}
	bad, _ := json.Marshal(decoded)
	if err := json.Unmarshal(bad, &loaded); err == nil {
		t.Fatal("bound decoder silently discarded a non-string hard contract")
	}
}

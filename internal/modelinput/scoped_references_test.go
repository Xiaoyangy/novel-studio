package modelinput

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestScopedReferencesPreserveFactsNumbersOrderAndProse(t *testing.T) {
	source := "src_" + strings.Repeat("a", 64)
	digest := "sha256:" + strings.Repeat("b", 64)
	resource := "res_0123456789abcdef"
	text := "本人记忆含旧资源 " + resource + " 和来源 " + source + "；不要修改任何字。"
	input := json.RawMessage(`{"agent_id":"ca_0123456789abcdef","source_stimulus_digest":"` + digest + `","resources":[{"resource_id":"` + resource + `","evidence_refs":["` + source + `","` + source + `"],"perception":{"amount":0.006423611111111112}}],"known_facts":[{"id":"fact_0123456789abcdef","text":"` + text + `","source":"` + source + `"}],"memory":[{"text":"` + text + `"}],"large":9007199254740993,"mechanism_refs":["M_RESOURCE"],"source_path":"/resource_id/` + resource + `"}`)
	before := append([]byte(nil), input...)
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	view := codec.ModelView()
	if !strings.Contains(string(view.Body), text) || !strings.Contains(string(view.Body), "9007199254740993") || !strings.Contains(string(view.Body), "0.006423611111111112") || !strings.Contains(string(view.Body), `"M_RESOURCE"`) {
		t.Fatal("view changed prose, exact numerical spelling or meaningful mechanism names")
	}
	if codec.Alias(source) == source || codec.Alias(resource) == resource || !strings.Contains(string(view.Body), `"evidence_refs":["`+codec.Alias(source)+`","`+codec.Alias(source)+`"]`) {
		t.Fatal("typed references were not shortened consistently or duplicate/order was changed")
	}
	expanded, err := codec.ExpandArguments(view.Body, view.Binding)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := referenceJSON(input)
	got, _ := referenceJSON(expanded)
	if !reflect.DeepEqual(want, got) || string(input) != string(before) {
		t.Fatal("round trip changed canonical input or mutated caller data")
	}
	view.Body[0] = '['
	if codec.ModelView().Body[0] != '{' {
		t.Fatal("caller mutation changed the frozen model view")
	}
}

func TestScopedReferencesAreBoundToExactInputAndNeverRewriteFreeText(t *testing.T) {
	first, err := NewScopedReferenceCodec(KindWorldArbitration, map[string]any{"proposal_digest": "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := NewScopedReferenceCodec(KindWorldArbitration, map[string]any{"proposal_digest": "sha256:" + strings.Repeat("b", 64)})
	if _, err := second.ExpandArguments(first.ModelView().Body, first.Binding()); err == nil {
		t.Fatal("foreign Host binding reinterpreted local aliases")
	}
	if _, err := first.ExpandArguments(json.RawMessage(`{"evidence_refs":["@ref999"]}`), first.Binding()); err == nil {
		t.Fatal("unknown handle accepted")
	}
	raw := json.RawMessage(`{"decision":"@ref1 is just a spoken word","state_after":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evidence_refs":["@ref1"]}`)
	if err := first.ValidateModelArguments(raw, first.Binding()); err == nil {
		t.Fatal("model prose containing a generated transport handle was accepted")
	}
	expanded, err := first.ExpandArguments(raw, first.Binding())
	if err != nil || !strings.Contains(string(expanded), `"decision":"@ref1 is just a spoken word"`) {
		t.Fatal("free text was treated as a reference")
	}
	if _, err := first.ExpandArguments(json.RawMessage(`{} {}`), first.Binding()); err == nil {
		t.Fatal("trailing args were accepted")
	}
}

func TestScopedReferencesMapCommunicationConflictAndReceivedFactEdges(t *testing.T) {
	communication := "src_" + strings.Repeat("a", 64)
	conflict := "sha256:" + strings.Repeat("b", 64)
	received := "recv_" + strings.Repeat("c", 64)
	input := map[string]any{
		"communications": []any{map[string]string{"id": communication, "text": "原通信正文不改"}},
		"conflicts":      []any{map[string]string{"id": conflict, "feedback": "原冲突正文不改"}},
		"received_facts": []any{map[string]string{"id": received, "source_id": communication, "text": "原接收正文不改"}},
	}
	codec, err := NewScopedReferenceCodec(KindWorldArbitration, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{communication, conflict, received} {
		if codec.Alias(reference) == reference {
			t.Fatal("a typed opaque communication/conflict/recv reference was not shortened")
		}
	}
	args, _ := json.Marshal(map[string]any{
		"passive_receptions": []any{map[string]string{"communication_id": codec.Alias(communication)}},
		"resolutions":        []any{map[string]any{"conflict_ids": []string{codec.Alias(conflict), codec.Alias(conflict)}}},
		"conflict_id":        codec.Alias(conflict),
		"knowledge_refs":     []string{codec.Alias(received)},
	})
	if err := codec.ValidateModelArguments(args, codec.Binding()); err != nil {
		t.Fatalf("valid typed reference edges were classified as prose: %v", err)
	}
	expanded, err := codec.ExpandArguments(args, codec.Binding())
	if err != nil {
		t.Fatal(err)
	}
	wanted, _ := json.Marshal(map[string]any{
		"passive_receptions": []any{map[string]string{"communication_id": communication}},
		"resolutions":        []any{map[string]any{"conflict_ids": []string{conflict, conflict}}},
		"conflict_id":        conflict,
		"knowledge_refs":     []string{received},
	})
	if string(expanded) != string(wanted) {
		t.Fatal("reference expansion changed linked identities, duplicates or order")
	}
}

func TestScopedReferencesRejectUnknownAndEmbeddedHandlesBeforePersistence(t *testing.T) {
	source := "src_" + strings.Repeat("a", 64)
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, map[string]string{"source": source})
	if err != nil {
		t.Fatal(err)
	}
	alias := codec.Alias(source)
	for _, key := range []string{"decision", "intended_action", "feedback", "text", "task_id"} {
		for _, value := range []string{alias, "搬动" + alias, "msg_" + alias + "_backup", alias + "suffix", "旧包中的@ref999", "new_@ref999_suffix"} {
			args, _ := json.Marshal(map[string]string{key: value})
			if err := codec.ValidateModelArguments(args, codec.Binding()); err == nil {
				t.Errorf("unknown/embedded transport handle escaped in %s=%q", key, value)
			}
		}
	}
	for _, key := range []string{"id", "communication_id", "conflict_id", "source_id", "resource_id"} {
		for _, value := range []string{"msg_" + alias, alias + "_backup", "before_" + alias + "after", "@ref999", "msg_@ref999_suffix"} {
			args, _ := json.Marshal(map[string]string{key: value})
			if err := codec.ValidateModelArguments(args, codec.Binding()); err == nil {
				t.Errorf("transport handle escaped into a new persistent %s=%q", key, value)
			}
			if _, err := codec.ExpandArguments(args, codec.Binding()); err == nil {
				t.Errorf("direct expansion accepted an invalid typed %s=%q", key, value)
			}
		}
	}
	args, _ := json.Marshal(map[string]any{"constraints": []string{"普通限制", "旧包@ref999"}, "conflict_ids": []string{alias}})
	if codec.ValidateModelArguments(args, codec.Binding()) == nil {
		t.Fatal("array prose bypassed the transport-handle check")
	}
	ordinary := json.RawMessage(`{"id":"new_message_id","decision":"保留记录","task_id":"ongoing_inspection"}`)
	if err := codec.ValidateModelArguments(ordinary, codec.Binding()); err != nil {
		t.Fatalf("ordinary new domain IDs were forbidden: %v", err)
	}
}

func TestScopedReferencesPreserveExactOriginalLiteralsWithoutMintingDerivativeIDs(t *testing.T) {
	identifier := "legacy_@ref1_suffix"
	prose := "原有标签 @ref2tail 和 @ref999 必须保留。"
	input := map[string]any{"id": identifier, "text": prose, "resource_id": "res_0123456789abcdef", "knowledge_refs": []string{"src_" + strings.Repeat("a", 64)}}
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{"res_0123456789abcdef", "src_" + strings.Repeat("a", 64)} {
		if alias := codec.Alias(original); alias == "@ref1" || alias == "@ref2" || alias == "@ref999" {
			t.Fatal("generated alias shadowed original literal handle text")
		}
	}
	view := codec.ModelView()
	if err := codec.ValidateModelArguments(view.Body, view.Binding); err != nil {
		t.Fatal("original source literals were rejected")
	}
	expanded, err := codec.ExpandArguments(view.Body, view.Binding)
	if err != nil {
		t.Fatal(err)
	}
	wanted, _ := json.Marshal(input)
	if string(expanded) != string(wanted) {
		t.Fatal("original literal identity or prose changed")
	}
	newID, _ := json.Marshal(map[string]string{"id": "new_" + identifier})
	if codec.ValidateModelArguments(newID, view.Binding) == nil {
		t.Fatal("an existing prose token licensed a newly minted handle-bearing identity")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := codec.ValidateModelArguments(view.Body, view.Binding); err != nil {
				t.Error(err)
			}
			if _, err := codec.ExpandArguments(view.Body, view.Binding); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
}

func TestScopedReferencesAvoidAuthorIdentifierCollisionAndAreDeterministic(t *testing.T) {
	input := map[string]any{"id": "@ref1", "text": "原本就写着 @ref2 的标签", "resource_id": "res_0123456789abcdef", "knowledge_refs": []string{"src_" + strings.Repeat("c", 64)}}
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	if codec.Alias("res_0123456789abcdef") == "@ref1" || codec.Alias("res_0123456789abcdef") == "@ref2" {
		t.Fatal("literal author identifier was shadowed")
	}
	expanded, err := codec.ExpandArguments(codec.ModelView().Body, codec.Binding())
	if err != nil || !strings.Contains(string(expanded), `"id":"@ref1"`) {
		t.Fatal("original literal handle-looking identifier was rejected")
	}
	if err := codec.ValidateModelArguments(json.RawMessage(`{"decision":"保留原来 @ref2 标签"}`), codec.Binding()); err != nil {
		t.Fatal("original literal prose was forbidden")
	}
	bad, _ := json.Marshal(map[string]string{"decision": "搬动" + codec.Alias("res_0123456789abcdef")})
	if err := codec.ValidateModelArguments(bad, codec.Binding()); err == nil {
		t.Fatal("generated transport alias escaped into persistent decision text")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			other, err := NewScopedReferenceCodec(KindCharacterObservation, input)
			if err != nil || other.Binding() != codec.Binding() || string(other.ModelView().Body) != string(codec.ModelView().Body) {
				t.Error("same source did not produce identical view/mapping identity")
			}
			if _, err := codec.ExpandArguments(codec.ModelView().Body, codec.Binding()); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
}

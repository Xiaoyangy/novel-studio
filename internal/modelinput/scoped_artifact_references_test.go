package modelinput

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestScopedArtifactDeliveryVersionUsesExistingScopeOnly(t *testing.T) {
	// The production C6 source has this same shape: an existing resource
	// version and the custodian's independently authored access authorization.
	version := "sha256:" + strings.Repeat("b", 64)
	resource := "res_" + strings.Repeat("c", 16)
	input := map[string]any{
		"world_stimulus": map[string]any{"physical_state": map[string]any{"resources": []any{map[string]any{"resource_id": resource, "artifact": map[string]any{"version_digest": version}}}}},
		"proposals":      []any{map[string]any{"artifact_access": []any{map[string]any{"resource_id": resource, "version_digest": version, "to_character": "乙", "access": "shared"}}}},
	}
	before, _ := json.Marshal(input)
	codec, err := NewScopedArtifactReferenceCodecV1(KindWorldArbitration, input)
	if err != nil {
		t.Fatal(err)
	}
	alias := codec.Alias(version)
	if alias == version || !strings.HasPrefix(alias, "@ref") {
		t.Fatal("known artifact version was not scoped in the actual input shape")
	}
	canonical := map[string]any{"resource_deliveries": []any{map[string]any{"resource_id": resource, "artifact_version_digest": version}}}
	raw := json.RawMessage(`{"resource_deliveries":[{"resource_id":"` + codec.Alias(resource) + `","artifact_version_digest":"` + alias + `"}]}`)
	if err := codec.ValidateModelArguments(raw, codec.Binding()); err != nil {
		t.Fatalf("existing delivery version was mistaken for a declaration: %v", err)
	}
	expanded, err := codec.ExpandArguments(raw, codec.Binding())
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal(expanded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, canonical) {
		t.Fatalf("delivery did not restore its exact original digest: %s", expanded)
	}
	// Direct canonical arguments stay byte-equivalent after JSON normalization.
	canonicalRaw, _ := json.Marshal(canonical)
	unchanged, err := codec.ExpandArguments(canonicalRaw, codec.Binding())
	if err != nil || string(unchanged) != string(canonicalRaw) {
		t.Fatal("canonical delivery version changed")
	}
	for _, bad := range []json.RawMessage{
		json.RawMessage(`{"resource_deliveries":[{"artifact_version_digest":"@ref999999"}]}`),
		json.RawMessage(`{"resource_deliveries":[{"artifact_version_digest":"prefix ` + alias + `"}]}`),
		json.RawMessage(`{"output_requests":[{"artifact_version_digest":"` + alias + `"}]}`),
		json.RawMessage(`{"resource_deliveries":[{"output_requests":[{"artifact_version_digest":"` + alias + `"}]}]}`),
		json.RawMessage(`{"resource_deliveries":[{"output_key":"` + alias + `"}]}`),
	} {
		if codec.ValidateModelArguments(bad, codec.Binding()) == nil {
			t.Fatalf("unknown/embedded handle or declaration accepted: %s", bad)
		}
	}
	foreign, err := NewScopedArtifactReferenceCodecV1(KindWorldArbitration, map[string]any{"version_digest": "sha256:" + strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.ExpandArguments(raw, foreign.Binding()); err == nil {
		t.Fatal("foreign scope binding accepted")
	}
	legacy, err := NewScopedReferenceCodec(KindWorldArbitration, input)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ValidateModelArguments(raw, legacy.Binding()) == nil {
		t.Fatal("legacy non-artifact codec changed its field semantics")
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("scoping/expansion rewrote the source artifact or proposal")
	}
}

func TestScopedArtifactReferencesRoundTripWithoutAliasingDeclarations(t *testing.T) {
	fact := "fact_" + strings.Repeat("a", 32)
	version := "sha256:" + strings.Repeat("b", 64)
	resource := "res_" + strings.Repeat("c", 16)
	input := map[string]any{"known_facts": []any{map[string]any{"id": fact, "text": "本人已知事实"}}, "artifact_views": []any{map[string]any{"resource_id": resource, "version_digest": version, "claims": []any{map[string]any{"claim_id": fact, "source_refs": []string{fact}}}}}, "proposals": []any{map[string]any{"self_tasks": []any{map[string]any{"output_requests": []any{map[string]any{"output_key": fact, "claims": []any{map[string]any{"claim_id": fact, "source_refs": []string{fact}}}}}}}}}}
	codec, err := NewScopedArtifactReferenceCodecV1(KindWorldArbitration, input)
	if err != nil {
		t.Fatal(err)
	}
	if codec.Binding().Policy != ScopedArtifactReferenceViewPolicyV1 {
		t.Fatal("missing distinct policy")
	}
	var view map[string]any
	if err := json.Unmarshal(codec.ModelView().Body, &view); err != nil {
		t.Fatal(err)
	}
	request := view["proposals"].([]any)[0].(map[string]any)["self_tasks"].([]any)[0].(map[string]any)["output_requests"].([]any)[0].(map[string]any)
	if request["output_key"] != fact || request["claims"].([]any)[0].(map[string]any)["claim_id"] != fact {
		t.Fatal("local declaration name was rewritten")
	}
	if request["claims"].([]any)[0].(map[string]any)["source_refs"].([]any)[0] != codec.Alias(fact) {
		t.Fatal("typed source refs were not aliased")
	}
	args := map[string]any{"self_tasks": []any{map[string]any{"output_requests": []any{map[string]any{"output_key": fact, "claims": []any{map[string]any{"claim_id": fact, "source_refs": []string{codec.Alias(fact)}}}}}}}, "artifact_reads": []any{map[string]any{"resource_id": codec.Alias(resource), "version_digest": codec.Alias(version), "claim_ids": []string{codec.Alias(fact)}}}, "self_executions": []any{map[string]any{"output_results": []any{map[string]any{"output_key": fact, "claim_ids": []string{fact}}}}}}
	raw, _ := json.Marshal(args)
	if err := codec.ValidateModelArguments(raw, codec.Binding()); err != nil {
		t.Fatal(err)
	}
	expanded, err := codec.ExpandArguments(raw, codec.Binding())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(expanded), "@ref") {
		t.Fatal("artifact reference handles escaped into canonical args")
	}
	for _, bad := range []string{`{"self_tasks":[{"output_requests":[{"output_key":"` + codec.Alias(fact) + `"}]}]}`, `{"self_tasks":[{"output_requests":[{"claims":[{"claim_id":"` + codec.Alias(fact) + `"}]}]}]}`, `{"self_executions":[{"output_results":[{"claim_ids":["` + codec.Alias(fact) + `"]}]}]}`, `{"claims":[{"source_refs":["@ref999999"]}]}`, `{"text":"source ` + codec.Alias(fact) + `"}`} {
		if codec.ValidateModelArguments(json.RawMessage(bad), codec.Binding()) == nil {
			t.Fatalf("accepted malformed reference/declaration: %s", bad)
		}
	}
	legacy, err := NewScopedReferenceCodec(KindWorldArbitration, input)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Binding().Policy != ScopedReferenceViewPolicy || legacy.Binding() == codec.Binding() {
		t.Fatal("legacy binding was reinterpreted")
	}
	if _, err := codec.ExpandArguments(raw, legacy.Binding()); err == nil {
		t.Fatal("cross-policy binding accepted")
	}
}

func TestScopedArtifactReferencesKeepLegacyConstructorBytes(t *testing.T) {
	input := map[string]any{"source_refs": []string{"sha256:" + strings.Repeat("a", 64)}, "output_key": "ordinary", "claim_id": "local", "version_digest": "sha256:" + strings.Repeat("b", 64)}
	legacy, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(legacy.ModelView())
	binding := legacy.Binding()
	_, err = NewScopedArtifactReferenceCodecV1(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again.ModelView())
	if string(before) != string(after) || binding != again.Binding() {
		t.Fatal("artifact codec changed old constructor state")
	}
	if strings.Contains(string(legacy.ModelView().Body), "@ref") {
		t.Fatal("legacy unrecognized artifact fields began aliasing")
	}
}

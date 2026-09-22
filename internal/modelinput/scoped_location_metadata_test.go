package modelinput

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScopedLocationMetadataMasksOnlyFourActorPaths(t *testing.T) {
	input := json.RawMessage(`{"agent_id":"ca_1111111111111111","location":"秘密港","self_experiences":[{"location":"秘密港"},{"location":"第二港"}],"resource_views":[{"known_placement":{"location":"秘密港"}}],"artifact_views":[{"placement":{"location":"第二港"},"claims":[{"text":"通信说这里叫秘密港","location":"保留作者声明"}]}],"received_facts":[{"text":"秘密港"}],"nested":{"location":"不投影的字段"}}`)
	before := string(input)
	codec, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Location        string `json:"location"`
		SelfExperiences []struct {
			Location string `json:"location"`
		} `json:"self_experiences"`
		ResourceViews []struct {
			KnownPlacement struct {
				Location string `json:"location"`
			} `json:"known_placement"`
		} `json:"resource_views"`
		ArtifactViews []struct {
			Placement struct {
				Location string `json:"location"`
			} `json:"placement"`
			Claims []struct{ Text, Location string } `json:"claims"`
		} `json:"artifact_views"`
		ReceivedFacts []struct{ Text string }   `json:"received_facts"`
		Nested        struct{ Location string } `json:"nested"`
	}
	if err := json.Unmarshal(codec.ModelView().Body, &view); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(view.Location, "@loc_") || view.Location != view.SelfExperiences[0].Location || view.Location != view.ResourceViews[0].KnownPlacement.Location || view.ArtifactViews[0].Placement.Location != view.SelfExperiences[1].Location || view.Location == view.SelfExperiences[1].Location {
		t.Fatal("typed locations lost exact identity or were not hidden")
	}
	if view.ArtifactViews[0].Claims[0].Text != "通信说这里叫秘密港" || view.ArtifactViews[0].Claims[0].Location != "保留作者声明" || view.ReceivedFacts[0].Text != "秘密港" || view.Nested.Location != "不投影的字段" {
		t.Fatal("location projection changed authored facts or an unlisted path")
	}
	if string(input) != before {
		t.Fatal("location projection changed canonical input")
	}
	if _, err := NewScopedLocationMetadataCodecV1(KindWorldArbitration, input); err == nil {
		t.Fatal("location projection accepted a non-actor packet")
	}
}

func TestScopedLocationMetadataRestoresOnlyBoundRootLocation(t *testing.T) {
	input := json.RawMessage(`{"agent_id":"ca_1111111111111111","location":"秘密港","resource_views":[{"resource_id":"res_2222222222222222"}],"cycle_context":{"index":1}}`)
	codec, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	var view struct{ Location string }
	if err := json.Unmarshal(codec.ModelView().Body, &view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(codec.ModelView().Body), "秘密港") {
		t.Fatal("private mapping leaked into the model body")
	}
	arguments, _ := json.Marshal(map[string]any{"location": view.Location, "decision": "原地观察", "resource_reads": []any{map[string]any{"resource_id": codec.Alias("res_2222222222222222")}}})
	if err := codec.ValidateModelArguments(arguments, codec.Binding()); err != nil {
		t.Fatal(err)
	}
	expanded, err := codec.ExpandArguments(arguments, codec.Binding())
	if err != nil {
		t.Fatal(err)
	}
	if string(expanded) != `{"decision":"原地观察","location":"秘密港","resource_reads":[{"resource_id":"res_2222222222222222"}]}` {
		t.Fatalf("wrong canonical arguments: %s", expanded)
	}
	foreign, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, json.RawMessage(strings.Replace(string(input), `"index":1`, `"index":2`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.ValidateModelArguments(arguments, foreign.Binding()); err == nil {
		t.Fatal("foreign binding accepted")
	}
	if _, err := codec.ExpandArguments(arguments, foreign.Binding()); err == nil {
		t.Fatal("foreign expansion binding accepted")
	}
	for _, bad := range []string{
		`{"location":"@loc_foreign_1"}`,
		`{"location":"prefix ` + view.Location + `"}`,
		`{"nested":{"location":"` + view.Location + `"}}`,
		`{"location":["` + view.Location + `"]}`,
		`{"decision":"` + view.Location + `"}`,
		`{"knowledge_refs":["` + view.Location + `"]}`,
		`{"resource_reads":[{"resource_id":"` + view.Location + `"}]}`,
		`{"communications":[{"text":"前往` + view.Location + `"}]}`,
		`{"self_tasks":[{"output_requests":[{"claims":[{"text":"` + view.Location + `"}]}]}]}`,
	} {
		if err := codec.ValidateModelArguments(json.RawMessage(bad), codec.Binding()); err == nil {
			t.Fatalf("accepted escaping location: %s", bad)
		}
		if _, err := codec.ExpandArguments(json.RawMessage(bad), codec.Binding()); err == nil {
			t.Fatalf("expanded escaping location: %s", bad)
		}
	}
	if err := foreign.ValidateModelArguments(arguments, foreign.Binding()); err == nil {
		t.Fatal("same ordinal from another input accepted")
	}
	knownFacts := json.RawMessage(`{"location":"` + view.Location + `","communications":[{"text":"读到的材料说这里叫秘密港"}],"self_tasks":[{"output_requests":[{"claims":[{"text":"对方报告秘密港"}]}]}]}`)
	if err := codec.ValidateModelArguments(knownFacts, codec.Binding()); err != nil {
		t.Fatal(err)
	}
	knownExpanded, err := codec.ExpandArguments(knownFacts, codec.Binding())
	if err != nil || !strings.Contains(string(knownExpanded), `"text":"读到的材料说这里叫秘密港"`) || !strings.Contains(string(knownExpanded), `"text":"对方报告秘密港"`) {
		t.Fatal("location metadata policy erased legitimate name-bearing statements")
	}
}

func TestScopedLocationMetadataPreservesLegacyViewsAndBindings(t *testing.T) {
	input := json.RawMessage(`{"agent_id":"ca_1111111111111111","location":"秘密港","source_refs":["sha256:` + strings.Repeat("a", 64) + `"],"version_digest":"sha256:` + strings.Repeat("b", 64) + `"}`)
	for _, tc := range []struct {
		name  string
		build func(ExactAgentPacketKind, any) (*ScopedReferenceCodec, error)
		body  string
	}{
		{"legacy", NewScopedReferenceCodec, `{"agent_id":"@ref1","location":"秘密港","source_refs":["sha256:` + strings.Repeat("a", 64) + `"],"version_digest":"sha256:` + strings.Repeat("b", 64) + `"}`},
		{"artifact", NewScopedArtifactReferenceCodecV1, `{"agent_id":"@ref1","location":"秘密港","source_refs":["@ref2"],"version_digest":"@ref3"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := tc.build(KindCharacterObservation, input)
			if err != nil {
				t.Fatal(err)
			}
			if string(before.ModelView().Body) != tc.body {
				t.Fatalf("old model bytes changed: %s", before.ModelView().Body)
			}
			location, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, input)
			if err != nil {
				t.Fatal(err)
			}
			if location.Binding().Policy != ScopedLocationMetadataViewPolicyV1 || location.Binding().SourceDigest != before.Binding().SourceDigest {
				t.Fatal("new policy lost its canonical source binding")
			}
			after, err := tc.build(KindCharacterObservation, input)
			if err != nil {
				t.Fatal(err)
			}
			if string(before.ModelView().Body) != string(after.ModelView().Body) || before.Binding() != after.Binding() {
				t.Fatal("new location constructor changed a legacy view/binding")
			}
			again, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, input)
			if err != nil {
				t.Fatal(err)
			}
			if location.Binding() != again.Binding() || string(location.ModelView().Body) != string(again.ModelView().Body) {
				t.Fatal("same canonical packet did not replay the same location mapping")
			}
			if _, err := location.ExpandArguments(json.RawMessage(`{"location":"秘密港"}`), before.Binding()); err == nil {
				t.Fatal("legacy binding activated the new policy")
			}
		})
	}
}

func TestScopedLocationMetadataRequiresCurrentOriginHandle(t *testing.T) {
	codec, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, json.RawMessage(`{"location":"CURRENT_SECRET","self_experiences":[{"location":"HISTORICAL_SECRET"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Location        string
		SelfExperiences []struct{ Location string } `json:"self_experiences"`
	}
	if err := json.Unmarshal(codec.ModelView().Body, &view); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{}`,
		`{"location":""}`,
		`{"location":null}`,
		`{"location":"CURRENT_SECRET"}`,
		`{"location":"guessed name"}`,
		`{"location":"` + view.SelfExperiences[0].Location + `"}`,
	} {
		if err := codec.ValidateModelArguments(json.RawMessage(bad), codec.Binding()); err == nil {
			t.Errorf("accepted a non-current-origin argument: %s", bad)
		}
		if _, err := codec.ExpandArguments(json.RawMessage(bad), codec.Binding()); err == nil {
			t.Errorf("expanded a non-current-origin argument: %s", bad)
		}
	}
	valid := json.RawMessage(`{"location":"` + view.Location + `","communications":[{"text":"对方说这里叫CURRENT_SECRET"}]}`)
	if err := codec.ValidateModelArguments(valid, codec.Binding()); err != nil {
		t.Fatal(err)
	}
	expanded, err := codec.ExpandArguments(valid, codec.Binding())
	if err != nil || string(expanded) != `{"communications":[{"text":"对方说这里叫CURRENT_SECRET"}],"location":"CURRENT_SECRET"}` {
		t.Fatalf("current-origin expansion lost canonical location or legitimate statement: %s %v", expanded, err)
	}
}

func TestScopedLocationMetadataNumbersFirstOccurrencesWithoutNameSorting(t *testing.T) {
	codec, err := NewScopedLocationMetadataCodecV1(KindCharacterObservation, json.RawMessage(`{"location":"Z_SECRET","self_experiences":[{"location":"B_SECRET"},{"location":"Z_SECRET"},{"location":"Y_SECRET"}],"resource_views":[{"known_placement":{"location":"A_SECRET"}}],"artifact_views":[{"placement":{"location":"C_SECRET"}},{"placement":{"location":"B_SECRET"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Location        string
		SelfExperiences []struct{ Location string } `json:"self_experiences"`
		ResourceViews   []struct {
			KnownPlacement struct{ Location string } `json:"known_placement"`
		} `json:"resource_views"`
		ArtifactViews []struct{ Placement struct{ Location string } } `json:"artifact_views"`
	}
	if err := json.Unmarshal(codec.ModelView().Body, &view); err != nil {
		t.Fatal(err)
	}
	locations := []string{view.Location, view.SelfExperiences[0].Location, view.SelfExperiences[1].Location, view.SelfExperiences[2].Location, view.ResourceViews[0].KnownPlacement.Location, view.ArtifactViews[0].Placement.Location, view.ArtifactViews[1].Placement.Location}
	for i, suffix := range []string{"_1", "_2", "_1", "_3", "_4", "_5", "_2"} {
		if !strings.HasSuffix(locations[i], suffix) {
			t.Errorf("path occurrence %d got %q; expected ordinal %s from fixed path order", i, locations[i], suffix)
		}
	}
}

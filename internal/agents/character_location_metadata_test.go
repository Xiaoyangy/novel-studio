package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"os"
	"strings"
	"testing"
)

func TestLocationMetadataFreshProducerRetainsOldRecovery(t *testing.T) {
	old := characterActivationProtocolV3MemoryTextDigest()
	t.Logf("unchanged memory producer=%s", old)
	fresh := characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV3)
	if fresh == "" || fresh == old {
		t.Fatal("location metadata requires a distinct fresh producer")
	}
	for _, p := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(p)
		if domain.HasCharacterHostLocationMetadataPolicyV1(policies) != (p == fresh) {
			t.Fatal("location visibility was retrofitted into a frozen producer")
		}
		if characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != p {
			t.Fatal("stored source inventory changed recovery identity")
		}
	}
	if CharacterActivationProtocolWithProducer(domain.CharacterActivationCyclePolicyV3, old) != old {
		t.Fatal("previous producer disappeared")
	}
}

func TestLocationMetadataReadOnlyCurrentSourceOptInSimulation(t *testing.T) {
	path := os.Getenv("NOVEL_CHARACTERS_SOURCE_READONLY")
	if path == "" {
		t.Skip("explicit read-only current book source not supplied")
	}
	raw, err := os.ReadFile(path)
	selectionMust(t, err)
	requireSelected := os.Getenv("NOVEL_REQUIRE_LOCATION_SOURCE_OPTIN") == "1"
	t.Logf("read-only source sha256=%x; require actual source opt-in=%t; all state built in temp Store", sha256.Sum256(raw), requireSelected)
	t.Cleanup(func() {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, after) {
			t.Error("read-only source changed")
		}
	})
	var chars []domain.Character
	selectionMust(t, json.Unmarshal(raw, &chars))
	unknown := false
	var actual string
	for i := range chars {
		if chars[i].Name == "陈渡" {
			actual = chars[i].InitialState.Location
			if requireSelected {
				if known := chars[i].InitialState.LocationNameKnown; known == nil || *known {
					t.Fatal("actual source has not explicitly selected private location metadata")
				}
			} else {
				chars[i].InitialState.LocationNameKnown = &unknown
			}
		}
	}
	if actual == "" {
		t.Fatal("explicit source patient missing")
	}
	st := store.NewStore(t.TempDir())
	selectionMust(t, st.Init())
	selectionMust(t, st.Characters.Save(chars))
	selectionMust(t, st.EnsureCharacterAgentCanon(0))
	registry, err := st.CharacterAgents.LoadRegistry()
	selectionMust(t, err)
	state, err := domain.BuildWorldPhysicalStateFromInitialV2(chars, *registry)
	selectionMust(t, err)
	state, err = domain.PrepareCharacterSelfChronologyStateV1(state)
	selectionMust(t, err)
	const generation = "pg2_readonly_location_source"
	for _, chapter := range []int{1, 2} {
		for _, name := range []string{"陈渡", "何静澜"} {
			var character domain.Character
			for _, c := range chars {
				if c.Name == name {
					character = c
				}
			}
			record, ok := registry.Resolve(name)
			if !ok {
				t.Fatal("missing source owner")
			}
			profile := characterAgentProfile{Character: character, Record: record}
			selectionMust(t, ensureCharacterAgentMemory(st, generation, profile, chapter, "2026-09-22T00:00:00Z"))
			policies := append(characterActivationV3HostLocationPolicies(), domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterPassiveReceptionPolicyV2)
			stimulus := domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: generation, Chapter: chapter, PhysicalState: &state, Sources: policies, Digest: "sha256:" + strings.Repeat("1", 64)}
			observation, err := buildCharacterObservation(st, generation, chapter, profile, stimulus, domain.ProjectedPlanningContextV2{}, "2026-09-22T00:00:00Z")
			selectionMust(t, err)
			makeCodec := modelinput.NewScopedArtifactReferenceCodecV1
			if name == "陈渡" {
				makeCodec = modelinput.NewScopedLocationMetadataCodecV1
			}
			codec, err := makeCodec(modelinput.KindCharacterObservation, observation)
			selectionMust(t, err)
			wire, _, err := modelinput.EncodeCharacterMemoryModelViewV1(codec.ModelView())
			selectionMust(t, err)
			view, err := modelinput.DecodeCharacterMemoryModelViewV1(wire)
			selectionMust(t, err)
			var visible domain.CharacterObservationPacket
			selectionMust(t, json.Unmarshal(view.Body, &visible))
			if name == "陈渡" {
				if bytes.Contains(view.Body, []byte(actual)) || !strings.HasPrefix(visible.Location, "@loc_") {
					t.Fatalf("chapter%d patient still sees host location name", chapter)
				}
				args, _ := json.Marshal(map[string]string{"location": visible.Location})
				selectionMust(t, codec.ValidateModelArguments(args, codec.Binding()))
				expanded, err := codec.ExpandArguments(args, codec.Binding())
				selectionMust(t, err)
				var restored map[string]string
				selectionMust(t, json.Unmarshal(expanded, &restored))
				if restored["location"] != actual {
					t.Fatal("source patient's actual location did not restore")
				}
			} else {
				for _, secret := range []string{"陈渡", "陈砚", "周启明", "七年前旧港火灾", "旧衣旧票", "船厂借出登记", "伤者离厂前"} {
					if bytes.Contains(view.Body, []byte(secret)) {
						t.Errorf("chapter%d doctor contains unreceived association %q", chapter, secret)
					}
				}
				for _, known := range []string{"周六14:17接报", "14:43交急诊", "急性硬膜外血肿", "07:20", "08:40", "08:48", "右袖口灰布补片"} {
					if !bytes.Contains(view.Body, []byte(known)) {
						t.Errorf("doctor lost personally known %q", known)
					}
				}
				if visible.Location != character.InitialState.Location {
					t.Fatal("doctor known location was hidden")
				}
			}
		}
	}
}

func TestLocationMetadataActualDispatchCyclesAndRecovery(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	chars, err := st.Characters.Load()
	selectionMust(t, err)
	const actual = "仅Host知道的住院区域正式名称"
	unknown := false
	for i := range chars {
		if chars[i].Name == "甲" {
			chars[i].InitialState.Location, chars[i].InitialState.LocationNameKnown = actual, &unknown
		}
	}
	selectionMust(t, st.Characters.Save(chars))
	sourceBefore, err := json.Marshal(chars)
	selectionMust(t, err)
	model := &activationV3RuntimeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "location-metadata", model)}
	const generation = "pg2_location_metadata"
	proof, err := runCharacterActivationChapter(t.Context(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if len(proof.Cycles) != 3 {
		t.Fatal("actual multi-cycle kernel did not complete")
	}
	seen := 0
	for _, o := range model.seenActors {
		if o.Character == "甲" {
			seen++
			raw, _ := json.Marshal(o)
			if !strings.HasPrefix(o.Location, "@loc_") || bytes.Contains(raw, []byte(actual)) {
				t.Fatalf("actual actor input leaks canonical metadata: %s", raw)
			}
		} else if o.Location != "船上" {
			t.Fatal("unselected actor's known location changed")
		}
	}
	if seen < 2 {
		t.Fatal("probe did not observe cross-cycle owner input")
	}
	for _, cycle := range proof.Cycles {
		for _, p := range cycle.Evidence.Proposals {
			if p.Character == "甲" && p.Location != actual {
				t.Fatal("actual submit tool did not restore exact canonical origin")
			}
		}
		for _, o := range cycle.Evidence.Observations {
			if o.Character == "甲" && o.Location != actual {
				t.Fatal("model view rewrote canonical observation")
			}
			tampered := o
			if o.Character == "甲" {
				tampered.HostLocationMetadataPolicy = ""
			} else {
				tampered.HostLocationMetadataPolicy = domain.CharacterHostLocationMetadataPolicyV1
			}
			tampered, err = domain.FinalizeCharacterObservationPacket(tampered)
			selectionMust(t, err)
			if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(cycle.Evidence.Stimulus, tampered); err == nil {
				t.Fatal("re-signed observation changed its actual owner's static location policy")
			}
		}
	}
	selectionMust(t, domain.ValidateCharacterActivationChapterEvidence(*proof))
	resumed := &activationV3RuntimeModel{}
	models = &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "location-recovery", resumed)}
	_, err = runCharacterActivationChapter(t.Context(), cfg, store.NewStore(st.Dir()), models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if resumed.actorCalls+resumed.arbiterCalls != 0 {
		t.Fatal("reopening accepted location history dispatched a model")
	}
	after, err := st.Characters.Load()
	selectionMust(t, err)
	sourceAfter, _ := json.Marshal(after)
	if !bytes.Equal(sourceBefore, sourceAfter) {
		t.Fatal("projection changed author source")
	}
}

func TestLocationMetadataOldProducerRejectsOptInBeforeModel(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	chars, err := st.Characters.Load()
	selectionMust(t, err)
	unknown := false
	chars[0].InitialState.LocationNameKnown = &unknown
	selectionMust(t, st.Characters.Save(chars))
	old := characterActivationProtocolV3MemoryTextDigest()
	cfg.CharacterAgents.FrozenActivationProducer, boundary.FrozenActivationProducer = old, old
	model := &activationV3RuntimeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "legacy-location", model)}
	_, err = runCharacterActivationChapter(t.Context(), cfg, st, models, "pg2_old_location_must_reject", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err == nil || model.actorCalls+model.arbiterCalls != 0 {
		t.Fatalf("old producer silently reinterpreted explicit visibility: %v", err)
	}
}

func TestLocationMetadataRehearsalRetainsExplicitSourcePolicy(t *testing.T) {
	st, binding, cfg := rehearsalCapabilityFixture(t)
	chars, err := st.Characters.Load()
	selectionMust(t, err)
	unknown := false
	chars[0].InitialState.LocationNameKnown = &unknown
	selectionMust(t, st.Characters.Save(chars))
	input, err := BuildArcRehearsalInput(st, binding, cfg)
	selectionMust(t, err)
	for _, o := range input.CharacterObservations {
		if o.Character != chars[0].Name {
			continue
		}
		if o.HostLocationMetadataPolicy != domain.CharacterHostLocationMetadataPolicyV1 {
			t.Fatal("rehearsal actor view silently lost source-selected metadata policy")
		}
		if o.Location != chars[0].InitialState.Location {
			t.Fatal("author rehearsal changed canonical geography")
		}
	}
	cfg.CharacterAgents.FrozenActivationProducer = characterActivationProtocolV3MemoryTextDigest()
	if _, err := BuildArcRehearsalInput(st, binding, cfg); err == nil {
		t.Fatal("old rehearsal producer silently accepted new source visibility")
	}
}

func TestLocationMetadataLegacyRehearsalObservationBytes(t *testing.T) {
	st, binding, cfg := rehearsalCapabilityFixture(t)
	cfg.CharacterAgents.FrozenActivationProducer = characterActivationProtocolV3MemoryTextDigest()
	const legacy = "ae23982424f939d0488cc95b7967987df67eecb0806117af9f4fa26f41d3aec2"
	for _, explicitKnown := range []bool{false, true} {
		if explicitKnown {
			chars, err := st.Characters.Load()
			selectionMust(t, err)
			known := true
			for i := range chars {
				chars[i].InitialState.LocationNameKnown = &known
			}
			selectionMust(t, st.Characters.Save(chars))
		}
		input, err := BuildArcRehearsalInput(st, binding, cfg)
		selectionMust(t, err)
		raw, err := json.Marshal(input.CharacterObservations)
		selectionMust(t, err)
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != legacy {
			t.Fatal("missing/known source changed pre-policy rehearsal actor-view bytes")
		}
	}
}

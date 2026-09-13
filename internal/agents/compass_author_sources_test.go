package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func bindAgentTestCompass(t *testing.T, st *store.Store) domain.AuthorSourcesV1 {
	t.Helper()
	catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "original-author", Text: "AUTHOR_ONLY_CONTRACT：必须三章完结，不能新增重要角色，每章2200—2500字。"}}})
	selectionMust(t, err)
	selectionMust(t, st.SaveAuthorSources(catalog))
	selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "SOFT_ONLY_ENDING：也许三人会共同签认，但这不是用户的额外要求", AuthorContracts: &domain.CompassAuthorContractsV1{Policy: catalog.Policy, SourcesDigest: catalog.Digest, Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "original-author", Paragraph: 0}}}}))
	return catalog
}

func TestRehearsalBoundCompassUsesAuthorClausesWithoutLeakingThemToActors(t *testing.T) {
	st, old := arcRehearsalTestInput(t)
	bindAgentTestCompass(t, st)
	input, err := BuildArcRehearsalInput(st, old)
	selectionMust(t, err)
	joined := strings.Join(input.HardContracts, "\n")
	if !strings.Contains(joined, "AUTHOR_ONLY_CONTRACT") || strings.Contains(joined, "SOFT_ONLY_ENDING") || input.SourceFiles[store.AuthorSourcesPath] == "" || input.InputDigest == old.InputDigest {
		t.Fatal("bound rehearsal lost author authority, hardened soft direction, or failed to bind the new source")
	}
	observations, err := json.Marshal(input.CharacterObservations)
	selectionMust(t, err)
	if strings.Contains(string(observations), "AUTHOR_ONLY_CONTRACT") || strings.Contains(string(observations), "SOFT_ONLY_ENDING") {
		t.Fatal("author future intent leaked into independent character observations")
	}
	path := filepath.Join(st.Dir(), store.AuthorSourcesPath)
	raw, err := os.ReadFile(path)
	selectionMust(t, err)
	selectionMust(t, os.WriteFile(path, []byte(strings.Replace(string(raw), "不能新增", "可以新增", 1)), 0o644))
	if _, err := BuildArcRehearsalInput(st, old); err == nil {
		t.Fatal("source mutation became a newly authorized rehearsal contract")
	}
}

func TestCharacterStimulusBoundCompassKeepsSoftEndingAndRejectsSourceDrift(t *testing.T) {
	st, _, boundary := activationV3RuntimeFixture(t)
	bindAgentTestCompass(t, st)
	selectionMust(t, st.EnsureCharacterAgentCanon(0))
	build := func() (domain.WorldStimulusPacket, error) {
		return buildWorldStimulusDraft(st, "pg2_author_source_stimulus", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, "2026-09-14T00:00:00Z", domain.CharacterAgentDecisionProtocolV2Version)
	}
	packet, err := build()
	selectionMust(t, err)
	if hard := strings.Join(packet.HardContracts, "\n"); !strings.Contains(hard, "AUTHOR_ONLY_CONTRACT") || strings.Contains(hard, "SOFT_ONLY_ENDING") {
		t.Fatal("character stimulus promoted a model-designed ending or lost actual author clauses")
	}
	if !strings.Contains(strings.Join(packet.SoftGuidance, "\n"), "SOFT_ONLY_ENDING") {
		t.Fatal("soft direction was deleted instead of remaining replannable")
	}
	path := filepath.Join(st.Dir(), store.AuthorSourcesPath)
	raw, err := os.ReadFile(path)
	selectionMust(t, err)
	selectionMust(t, os.WriteFile(path, append(raw, []byte("garbage")...), 0o644))
	if _, err := build(); err == nil {
		t.Fatal("stimulus swallowed a compass/source validation failure")
	}
}

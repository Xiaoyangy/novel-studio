package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func chronologySourceTestFixture(t *testing.T) (*Store, domain.PlanningGenerationV2, domain.PlanningSourceSnapshotV2, domain.ProjectedChapterBundle) {
	t.Helper()
	st := NewStore(t.TempDir())
	verifiedStoreMust(t, st.Init())
	characters := []domain.Character{{Name: "甲", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "船上", CurrentGoal: "检查", Pressure: "时间有限", KnownFacts: []string{"本人已在船上"}}}}
	verifiedStoreMust(t, st.Characters.Save(characters))
	g, source, registry, _ := projectedStoreV2Fixture(t, 1, false)
	g.BaseCanonChapter, g.FirstProjectedChapter, g.LastProjectedChapter = 0, 1, 1
	registry.FirstChapter, registry.LastChapter = 1, 1
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	verifiedStoreMust(t, err)
	g.ObligationRegistryRoot = registry.RegistryRoot
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	verifiedStoreMust(t, err)
	source.BaseCanonChapter = 0
	_, source.FoundationSnapshotRoot, err = CaptureProjectAllFoundationSnapshot(st.Dir())
	verifiedStoreMust(t, err)
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.ProjectedV2().CreateBuildingGeneration(g, source, registry))
	reg := domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	reg, _, err = reg.UpsertCharacter("甲", nil, "core", 0, "")
	verifiedStoreMust(t, err)
	raw, err := domain.BuildWorldPhysicalStateFromInitialV2(characters, reg)
	verifiedStoreMust(t, err)
	prepared, err := domain.PrepareCharacterSelfChronologyStateV1(raw)
	verifiedStoreMust(t, err)
	bundle := domain.ProjectedChapterBundle{GenerationID: g.GenerationID, Chapter: 1, CharacterAgentEvidence: &domain.CharacterAgentEvidenceBundle{Stimulus: domain.WorldStimulusPacket{Sources: []string{domain.CharacterSelfChronologyPolicyV1}, PhysicalState: &prepared}}}
	return st, g, source, bundle
}

func TestChronologySourceCapsuleFreezesOriginalBytesWithoutReplacingSnapshot(t *testing.T) {
	st, g, source, bundle := chronologySourceTestFixture(t)
	p := st.ProjectedV2()
	base := projectedBuildingGenerationPath(g.GenerationID)
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, p.validateProjectedChronologySourceUnlocked(base, g, bundle, false))
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("read-only source verification captured a file")
	}
	verifiedStoreMust(t, p.withProjectedWriteLock(func() error { return p.validateProjectedChronologySourceUnlocked(base, g, bundle, true) }))
	raw, err := os.ReadFile(p.io.path(filepath.Join(base, projectedChronologySourceFile)))
	verifiedStoreMust(t, err)
	var capsule projectedChronologySource
	verifiedStoreMust(t, json.Unmarshal(raw, &capsule))
	if capsule.SourceSnapshotDigest != source.SnapshotDigest || len(capsule.Characters) == 0 {
		t.Fatal("capsule did not preserve actual frozen source bytes")
	}
	// A later accepted/canonical change must not invalidate the frozen source.
	verifiedStoreMust(t, st.Characters.Save([]domain.Character{{Name: "later canon"}}))
	verifiedStoreMust(t, p.validateProjectedChronologySourceUnlocked(base, g, bundle, false))
	verifiedStoreMust(t, p.withProjectedWriteLock(func() error { return p.validateProjectedChronologySourceUnlocked(base, g, bundle, true) }))
	again, err := os.ReadFile(p.io.path(filepath.Join(base, projectedChronologySourceFile)))
	verifiedStoreMust(t, err)
	if string(raw) != string(again) {
		t.Fatal("retry replaced frozen source")
	}
	sealed := projectedSealedGenerationPath(g.GenerationID)
	verifiedStoreMust(t, p.io.WriteJSON(filepath.Join(sealed, projectedSourceSnapshotFile), source))
	verifiedStoreMust(t, p.copyChronologySourceUnlocked(base, sealed))
	g.Status = domain.PlanningGenerationSealedV2
	verifiedStoreMust(t, p.validateProjectedChronologySourceUnlocked(sealed, g, bundle, false))
	copied, err := os.ReadFile(p.io.path(filepath.Join(sealed, projectedChronologySourceFile)))
	verifiedStoreMust(t, err)
	if string(raw) != string(copied) {
		t.Fatal("seal copy changed exact original capsule bytes")
	}
}

func TestChronologySourceCapsuleRejectsResignedOpeningAndFrozenInventoryTampering(t *testing.T) {
	for _, kind := range []string{"opening_source_root", "opening_location", "source_drift_before_capture", "capsule_original_bytes", "capsule_resigned_inventory", "missing_sealed_capsule"} {
		t.Run(kind, func(t *testing.T) {
			st, g, _, bundle := chronologySourceTestFixture(t)
			p := st.ProjectedV2()
			base := projectedBuildingGenerationPath(g.GenerationID)
			if kind == "source_drift_before_capture" {
				verifiedStoreMust(t, st.Characters.Save([]domain.Character{{Name: "other source"}}))
			} else if strings.HasPrefix(kind, "opening_") {
				state := bundle.CharacterAgentEvidence.Stimulus.PhysicalState
				if kind == "opening_location" {
					state.Actors[0].Location = "elsewhere"
				} else {
					b := state.Actors[0].SelfChronologyBaseline
					b.SourcePhysicalRoot = projectedStoreV2Digest("foreign")
					b.Digest = ""
					hash, err := domain.DeterministicPlanningHash(*b)
					verifiedStoreMust(t, err)
					b.Digest = "sha256:" + hash
				}
				verifiedStoreMust(t, domain.ValidateWorldPhysicalStateV2(*state))
			} else {
				verifiedStoreMust(t, p.validateProjectedChronologySourceUnlocked(base, g, bundle, true))
				var capsule projectedChronologySource
				verifiedStoreMust(t, p.readJSONUnlocked(filepath.Join(base, projectedChronologySourceFile), &capsule))
				if kind == "missing_sealed_capsule" {
					verifiedStoreMust(t, os.Remove(p.io.path(filepath.Join(base, projectedChronologySourceFile))))
					g.Status = domain.PlanningGenerationSealedV2
				} else {
					capsule.Characters = []byte(`[{"name":"forged original"}]`)
					if kind == "capsule_resigned_inventory" {
						capsule.Foundation.Artifacts["characters.json"] = characterMemoryPublicationSHA(capsule.Characters)
					}
					var err error
					capsule.Digest, err = projectedChronologySourceDigest(capsule)
					verifiedStoreMust(t, err)
					verifiedStoreMust(t, p.io.WriteJSON(filepath.Join(base, projectedChronologySourceFile), capsule))
				}
			}
			before, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if err := p.validateProjectedChronologySourceUnlocked(base, g, bundle, true); err == nil {
				t.Fatal("self-signed or changed source was accepted")
			}
			after, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("failed source check changed files")
			}
		})
	}
}

func chronologyAcceptedPhysicalBundle(t *testing.T, bundle domain.ProjectedChapterBundle) domain.ProjectedChapterBundle {
	t.Helper()
	attachStoreIndependentV1EvidenceForProtocolTest(t, &bundle)
	e := *bundle.CharacterAgentEvidence
	actor := domain.CharacterPhysicalStateV2{AgentID: e.Proposals[0].AgentID, Character: e.Proposals[0].Character, Location: e.Proposals[0].Location, Resources: []domain.CharacterResourceHoldingV2{}}
	state, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Actors: []domain.CharacterPhysicalStateV2{actor}})
	verifiedStoreMust(t, err)
	e.Stimulus.Version, e.Stimulus.PhysicalState = domain.WorldStimulusPacketV2Version, &state
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	verifiedStoreMust(t, err)
	o := e.Observations[0]
	o.Version, o.Location, o.StimulusDigest = domain.CharacterObservationV2Version, actor.Location, e.Stimulus.Digest
	o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(state, actor.AgentID)
	verifiedStoreMust(t, err)
	o, err = domain.FinalizeCharacterObservationPacket(o)
	verifiedStoreMust(t, err)
	p := e.Proposals[0]
	p.ObservationDigest = o.Digest
	p, err = domain.FinalizeCharacterDecisionProposal(p, o)
	verifiedStoreMust(t, err)
	e.Activation.Entries[0].ObservationDigest = o.Digest
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	verifiedStoreMust(t, err)
	r := e.Arbitrations[0]
	r.Version, r.StimulusDigest, r.ActivationDigest = domain.WorldArbitrationReceiptV2Version, e.Stimulus.Digest, e.Activation.Digest
	r.ProposalDigests = []string{p.Digest}
	r.Resolutions[0].ProposalDigest = p.Digest
	r.Resolutions[0].PostState = &actor
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, []domain.CharacterDecisionProposal{p}, 1)
	verifiedStoreMust(t, err)
	e.Proposals, e.Observations, e.Arbitrations = []domain.CharacterDecisionProposal{p}, []domain.CharacterObservationPacket{o}, []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	verifiedStoreMust(t, err)
	state, err = domain.ApplyArbitrationPhysicalStateV2(r, e.Stimulus, p)
	verifiedStoreMust(t, err)
	bundle.CharacterAgentEvidence = &e
	sim := &bundle.ChapterWorldSimulation
	sim.PhysicalState = &state
	sim.CharacterDecisions, err = r.CharacterDecisions(e.Proposals, state)
	verifiedStoreMust(t, err)
	sim.CharacterAgentProtocol = &domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolV2Version, RegistryRoot: e.Registry.RegistryRoot, StimulusDigest: e.Stimulus.Digest, ActivationDigest: e.Activation.Digest, ObservationDigests: []string{o.Digest}, ProposalDigests: []string{p.Digest}, ArbitrationRound: 1, ArbitrationDigest: r.Digest, MemoryRoots: e.MemoryRoots, ProtocolDigest: e.ProtocolDigest}
	encoded, err := domain.EncodeWorldPhysicalStateV2(state)
	verifiedStoreMust(t, err)
	bundle.ProjectedDelta.CharacterState = []domain.StateMutationV2{{StableID: "physical:world", Subject: "world", Field: domain.WorldPhysicalStateV2Field, Operation: "set", After: encoded, Cause: "actual arbitration"}}
	bundle.ProjectedDelta.Locations = []domain.StateMutationV2{{StableID: "physical:location", Subject: actor.Character, Field: "location", Operation: "set", After: actor.Location, Cause: "actual arbitration"}}
	bundle.ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(bundle.ProjectedPreStateRoot, bundle.ProjectedDelta)
	verifiedStoreMust(t, err)
	projectedStoreV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, domain.ValidateProjectedChapterBundle(bundle))
	return bundle
}

func TestChronologySourceCapsuleUsesRealAcceptedPredecessorAcrossGenerations(t *testing.T) {
	st := NewStore(t.TempDir())
	verifiedStoreMust(t, st.Init())
	verifiedStoreMust(t, st.Progress.Init("chronology-source-test", 5))
	p := st.ProjectedV2()
	g, source, registry, bundles := projectedStoreV2Fixture(t, 1, false)
	g.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	var err error
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	verifiedStoreMust(t, err)
	bundle := chronologyAcceptedPhysicalBundle(t, bundles[0])
	verifiedStoreMust(t, p.CreateBuildingGeneration(g, source, registry))
	verifiedStoreMust(t, p.SaveProjectedChapterBundle(bundle))
	_, err = p.SealGeneration(g.GenerationID)
	verifiedStoreMust(t, err)
	_, cursor, err := p.ActivateSealedGeneration(g.GenerationID, nil)
	verifiedStoreMust(t, err)
	promotion := projectedStoreV2Promotion(t, bundle)
	_, err = p.Promote(*cursor, promotion)
	verifiedStoreMust(t, err)
	cursor, err = p.LoadRealizationCursor()
	verifiedStoreMust(t, err)
	body := []byte("An actual test body binds the accepted predecessor.")
	rel := filepath.Join("chapters", "04.md")
	verifiedStoreMust(t, st.Progress.io.WriteFileUnlocked(rel, body))
	bodySHA := characterMemoryPublicationSHA(body)
	commit, err := st.Checkpoints.Append(domain.ChapterScope(4), "commit", rel, bodySHA)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.Progress.MarkChapterComplete(4, len(body), "choice", "main"))
	verifiedStoreMust(t, st.SaveChapterWorldSimulation(bundle.ChapterWorldSimulation))
	outcome := projectedStoreV2Outcome(t, bundle, promotion)
	outcome.ChapterBodySHA256, outcome.CommitCheckpointSeq = bodySHA, commit.Seq
	outcome.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
	verifiedStoreMust(t, err)
	_, err = p.AcceptOutcome(*cursor, outcome)
	verifiedStoreMust(t, err)
	if _, err := st.LoadAcceptedCharacterAgentBundle(4); err != nil {
		t.Fatal("predecessor did not really pass accepted Store verification", err)
	}
	next, nextSource, nextRegistry, _ := projectedStoreV2FixtureWithAttempt(t, 1, "chronology-successor")
	next.BaseCanonChapter, next.FirstProjectedChapter, next.LastProjectedChapter = 4, 5, 5
	next.BaseCanonRoot, next.BaseStateRoot = outcome.ActualCanonRoot, bundle.ProjectedPostStateRoot
	next.GenerationID, err = domain.DerivePlanningGenerationAttemptV2ID(next.BaseCanonRoot, next.StableOutlineRoot, next.PlanningDependencyRoot, next.RandomSeedContractRoot, next.AttemptID)
	verifiedStoreMust(t, err)
	nextRegistry.GenerationID, nextRegistry.FirstChapter, nextRegistry.LastChapter = next.GenerationID, 5, 5
	nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
	verifiedStoreMust(t, err)
	next.ObligationRegistryRoot = nextRegistry.RegistryRoot
	next.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(next)
	verifiedStoreMust(t, err)
	nextSource.GenerationID, nextSource.BaseCanonChapter, nextSource.BaseCanonRoot, nextSource.BaseStateRoot = next.GenerationID, 4, next.BaseCanonRoot, next.BaseStateRoot
	_, nextSource.FoundationSnapshotRoot, err = CaptureProjectAllFoundationSnapshot(st.Dir())
	verifiedStoreMust(t, err)
	nextSource.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(nextSource)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, p.CreateBuildingGeneration(next, nextSource, nextRegistry))
	prepared, err := domain.PrepareCharacterSelfChronologyStateV1(*bundle.ChapterWorldSimulation.PhysicalState)
	verifiedStoreMust(t, err)
	opening := domain.ProjectedChapterBundle{GenerationID: next.GenerationID, Chapter: 5, CharacterAgentEvidence: &domain.CharacterAgentEvidenceBundle{Stimulus: domain.WorldStimulusPacket{Sources: []string{domain.CharacterSelfChronologyPolicyV1}, PhysicalState: &prepared}}}
	base := projectedBuildingGenerationPath(next.GenerationID)
	verifiedStoreMust(t, p.withProjectedWriteLock(func() error { return p.validateProjectedChronologySourceUnlocked(base, next, opening, true) }))
	var capsule projectedChronologySource
	verifiedStoreMust(t, p.readJSONUnlocked(filepath.Join(base, projectedChronologySourceFile), &capsule))
	if capsule.Accepted == nil || capsule.Accepted.OutcomeDigest != outcome.ReceiptDigest || capsule.Accepted.BundleDigest != bundle.BundleDigest || len(capsule.Characters) > 0 {
		t.Fatal("new generation did not bind the real immutable accepted source")
	}
	// Freeze first, then remove the mutable lookup identity. Old immutable
	// acceptance remains independently verifiable on restart.
	verifiedStoreMust(t, os.Remove(st.Progress.io.path("meta/planning/v2/realization_cursor.json")))
	restored := NewStore(st.Dir()).ProjectedV2()
	verifiedStoreMust(t, restored.validateProjectedChronologySourceUnlocked(base, next, opening, false))
	bad := verifiedStoreCopy(opening)
	b := bad.CharacterAgentEvidence.Stimulus.PhysicalState.Actors[0].SelfChronologyBaseline
	b.SourcePhysicalRoot = projectedStoreV2Digest("alternate accepted origin")
	b.Digest = ""
	hash, err := domain.DeterministicPlanningHash(*b)
	verifiedStoreMust(t, err)
	b.Digest = "sha256:" + hash
	verifiedStoreMust(t, domain.ValidateWorldPhysicalStateV2(*bad.CharacterAgentEvidence.Stimulus.PhysicalState))
	if restored.validateProjectedChronologySourceUnlocked(base, next, bad, false) == nil {
		t.Fatal("resigned source root bypassed real accepted predecessor")
	}
}

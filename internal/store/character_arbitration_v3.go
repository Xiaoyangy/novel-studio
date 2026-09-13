package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// This is a frozen host selection, not a serialized verification capability.
// Every load reconstructs authority from the real prefix and frozen inputs.
type characterArbitrationAdmissionV3 struct {
	Version       string                                      `json:"version"`
	SessionDigest string                                      `json:"session_digest"`
	InputDigest   string                                      `json:"input_digest"`
	Protocol      string                                      `json:"protocol"`
	Continuations []domain.CharacterWorkContinuationReceiptV1 `json:"continuations,omitempty"`
}

const characterArbitrationAdmissionVersionV3 = "character-arbitration-admission.v3"

type CharacterArbitrationV3 struct {
	store     *Store
	prefix    domain.VerifiedCharacterActivationPrefix
	input     domain.CharacterActivationInputSet
	proofs    *CharacterAgentStore
	admission characterArbitrationAdmissionV3
}

// The admission has already been authenticated by Load/Prepare. Expose its
// frozen producer for the runtime's executable-protocol check before payment.
func (v *CharacterArbitrationV3) ProtocolDigest() string {
	if v == nil {
		return ""
	}
	return v.admission.Protocol
}

// Equal source hashes do not authorize writing a copied workspace. Runtime
// tools may use another Store instance only for the same canonical directory.
func (v *CharacterArbitrationV3) BelongsTo(st *Store) bool {
	if v == nil || v.store == nil || st == nil || st.CharacterAgents == nil || v.store.CharacterAgents == nil {
		return false
	}
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	left, err := canonical(v.store.CharacterAgents.io.dir)
	if err != nil {
		return false
	}
	right, err := canonical(st.CharacterAgents.io.dir)
	return err == nil && left == right
}

func (v *CharacterArbitrationV3) admissionPath() string {
	return filepath.Join(characterAgentChapterDir(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter), "round_sources_v3.json")
}

// Prepare freezes selection before any paid proposal is published. It does not
// require proposals or advance the session. Repeated preparation cannot change
// the selected owners; lost admission metadata cannot be rebuilt after payment.
func (s *Store) PrepareCharacterArbitrationV3(session domain.CharacterActivationSession, continuations []domain.CharacterWorkContinuationReceiptV1, protocol string) (*CharacterArbitrationV3, error) {
	var result *CharacterArbitrationV3
	err := s.withCharacterActivationWrite(func() error {
		v, err := s.newCharacterArbitrationV3(session.GenerationID, session.Chapter)
		if err != nil {
			return err
		}
		if v == nil || v.prefix.Session().Digest != session.Digest {
			return fmt.Errorf("v3 arbitration preparation has a stale session")
		}
		v.admission = characterArbitrationAdmissionV3{Version: characterArbitrationAdmissionVersionV3, SessionDigest: session.Digest, InputDigest: v.input.Digest, Protocol: protocol, Continuations: copyContinuationStoreValue(continuations)}
		sort.Slice(v.admission.Continuations, func(i, j int) bool {
			return v.admission.Continuations[i].AgentID < v.admission.Continuations[j].AgentID
		})
		if err := v.validateAdmission(); err != nil {
			return err
		}
		if err := v.validatePartialProofs(); err != nil {
			return err
		}
		var stored characterArbitrationAdmissionV3
		if err := v.proofs.readProof(v.admissionPath(), &stored); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			if err := v.requireNoPaidProofs(); err != nil {
				return err
			}
		}
		if err := v.proofs.writeProof(v.admissionPath(), v.admission); err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}

func (s *Store) LoadCharacterArbitrationV3(generation string, chapter int) (*CharacterArbitrationV3, error) {
	if _, err := characterActivationSessionDir(generation, chapter); err != nil {
		return nil, err
	}
	if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s.ProjectedV2(), func() (*CharacterArbitrationV3, error) {
		v, err := s.newCharacterArbitrationV3(generation, chapter)
		if err != nil || v == nil {
			return nil, err
		}
		if err := v.loadAdmission(); err != nil {
			if os.IsNotExist(err) {
				if err := v.validateProofInventory(); err != nil {
					return nil, err
				}
				if err := v.requireNoPaidProofs(); err != nil {
					return nil, err
				}
				return nil, nil
			}
			return nil, err
		}
		if err := v.validatePartialProofs(); err != nil {
			return nil, err
		}
		return v, nil
	})
}

// Caller holds the projected lock. Historical validation supplies its verified
// pre-cycle prefix directly instead of recursively loading the current cursor.
func (s *Store) newCharacterArbitrationV3(generation string, chapter int) (*CharacterArbitrationV3, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	prefix, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
	if err != nil || prefix == nil {
		return nil, err
	}
	return s.characterArbitrationV3ForPrefix(*prefix)
}

func (s *Store) characterArbitrationV3ForPrefix(prefix domain.VerifiedCharacterActivationPrefix) (*CharacterArbitrationV3, error) {
	proofs, err := s.CharacterAgents.ForActivationCycle(prefix.Session())
	if err != nil {
		return nil, err
	}
	input, err := proofs.LoadActivationInputs()
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("v3 arbitration requires its frozen input")
	}
	v := &CharacterArbitrationV3{store: s, prefix: prefix, input: *input, proofs: proofs}
	return v, nil
}

func (v *CharacterArbitrationV3) loadAdmission() error {
	if err := v.proofs.readProof(v.admissionPath(), &v.admission); err != nil {
		return err
	}
	return v.validateAdmission()
}

func (v *CharacterArbitrationV3) validateAdmission() error {
	a := v.admission
	if a.Version != characterArbitrationAdmissionVersionV3 || a.SessionDigest != v.prefix.Session().Digest || a.InputDigest != v.input.Digest || !strings.HasPrefix(a.Protocol, "sha256:") || len(a.Protocol) != 71 || strings.Trim(a.Protocol[7:], "0123456789abcdef") != "" {
		return fmt.Errorf("v3 admission differs from its source identity/protocol")
	}
	contains := func(values []string, value string) bool {
		for _, s := range values {
			if s == value {
				return true
			}
		}
		return false
	}
	policies := []string{domain.CharacterActivationCyclePolicyV3, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1}
	for _, policy := range policies {
		if !contains(v.input.Stimulus.Sources, policy) {
			return fmt.Errorf("v3 admission lacks explicit frozen policy")
		}
		for _, o := range v.input.Observations {
			if !contains(o.Sources, policy) {
				return fmt.Errorf("v3 observation lacks explicit frozen policy")
			}
		}
	}
	for _, old := range []string{domain.CharacterActivationCyclePolicy, domain.CharacterActivationCyclePolicyV2} {
		if contains(v.input.Stimulus.Sources, old) {
			return fmt.Errorf("v3 cannot reinterpret an old frozen policy")
		}
		for _, o := range v.input.Observations {
			if contains(o.Sources, old) {
				return fmt.Errorf("v3 cannot reinterpret an old observation policy")
			}
		}
	}
	if v.input.Stimulus.SelfEvaluationContext == nil {
		return fmt.Errorf("v3 admission lacks frozen self chronology context")
	}
	if err := domain.ValidateCharacterSelfEvaluationContextAgainstSessionV1(*v.input.Stimulus.SelfEvaluationContext, v.prefix.Session()); err != nil {
		return err
	}
	if err := v.prefix.ValidateCycleProtocol(domain.CharacterActivationCycleV3Version, a.Protocol); err != nil {
		return fmt.Errorf("v3 cannot upgrade old history or change execution protocol")
	}
	seen := map[string]bool{}
	for _, receipt := range a.Continuations {
		if seen[receipt.AgentID] {
			return fmt.Errorf("v3 admission repeats an owner")
		}
		seen[receipt.AgentID] = true
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(v.prefix, v.input, receipt.AgentID)
		if err != nil {
			return err
		}
		if !eligible.Eligible || eligible.Receipt == nil || !jsonValuesEqual(*eligible.Receipt, receipt) {
			return fmt.Errorf("v3 continuation admission is not its actual eligible source")
		}
	}
	return nil
}

func (v *CharacterArbitrationV3) Input() domain.CharacterActivationInputSet {
	return copyContinuationStoreValue(v.input)
}
func (v *CharacterArbitrationV3) Continuations() []domain.CharacterWorkContinuationReceiptV1 {
	return copyContinuationStoreValue(v.admission.Continuations)
}

// Refresh under the shared lock before using a captured view, including reads:
// a paid result cannot be published into a different collecting session.
func (v *CharacterArbitrationV3) refresh() (*CharacterArbitrationV3, error) {
	if v == nil || v.store == nil {
		return nil, fmt.Errorf("v3 arbitration requires a host-created view")
	}
	next, err := v.store.newCharacterArbitrationV3(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter)
	if err != nil {
		return nil, err
	}
	if next == nil || next.prefix.Session().Digest != v.prefix.Session().Digest {
		return nil, fmt.Errorf("v3 arbitration view has a stale session")
	}
	if err := next.loadAdmission(); err != nil {
		return nil, err
	}
	if !jsonValuesEqual(next.admission, v.admission) {
		return nil, fmt.Errorf("v3 arbitration admission changed")
	}
	if err := next.validatePartialProofs(); err != nil {
		return nil, err
	}
	return next, nil
}

func (v *CharacterArbitrationV3) Sources(round int) (domain.VerifiedCharacterArbitrationSourcesV1, error) {
	return withProjectedReadResult(v.store.ProjectedV2(), func() (domain.VerifiedCharacterArbitrationSourcesV1, error) {
		next, err := v.refresh()
		if err != nil {
			return domain.VerifiedCharacterArbitrationSourcesV1{}, err
		}
		return next.sources(round)
	})
}

func (v *CharacterArbitrationV3) sources(round int) (domain.VerifiedCharacterArbitrationSourcesV1, error) {
	var zero domain.VerifiedCharacterArbitrationSourcesV1
	if round != 1 && round != 2 {
		return zero, fmt.Errorf("v3 arbitration supports exactly R1 and bounded R2")
	}
	var proposals []domain.CharacterDecisionProposal
	for _, entry := range v.input.Activation.Entries {
		if entry.State != domain.CharacterAgentActive {
			continue
		}
		p, err := v.proofs.LoadProposal(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, 1, entry.AgentID)
		if err != nil {
			return zero, err
		}
		if p != nil {
			proposals = append(proposals, *p)
		}
	}
	scope, err := domain.ResolveCharacterArbitrationRoundV1(v.prefix, v.input, v.admission.Continuations, proposals)
	if err != nil || round == 1 {
		return scope, err
	}
	r1, err := v.loadRound(1)
	if err != nil {
		return zero, err
	}
	if r1 == nil {
		return zero, fmt.Errorf("v3 R2 requires its durable verified R1")
	}
	var observations []domain.CharacterObservationPacket
	proposals = nil
	for _, entry := range v.input.Registry.Entries {
		o, err := v.proofs.LoadObservation(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, 2, entry.AgentID)
		if err != nil {
			return zero, err
		}
		p, err := v.proofs.LoadProposal(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, 2, entry.AgentID)
		if err != nil {
			return zero, err
		}
		if o != nil {
			observations = append(observations, *o)
		}
		if p != nil {
			proposals = append(proposals, *p)
		}
	}
	return domain.ResolveCharacterArbitrationRevisionV1(*r1, observations, proposals)
}

func (v *CharacterArbitrationV3) loadRound(round int) (*domain.VerifiedCharacterArbitrationRoundV1, error) {
	if round != 1 && round != 2 {
		return nil, fmt.Errorf("invalid v3 arbitration round")
	}
	var receipt domain.WorldArbitrationReceipt
	if err := v.proofs.readProof(characterAgentArbitrationPath(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, round), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sources, err := v.sources(round)
	if err != nil {
		return nil, err
	}
	verified, err := domain.FinalizeCharacterArbitrationRoundV1(sources, receipt)
	if err != nil {
		return nil, err
	}
	if !jsonValuesEqual(verified.Receipt(), receipt) {
		return nil, fmt.Errorf("v3 arbitration differs from exact finalized source receipt")
	}
	return &verified, nil
}

func (v *CharacterArbitrationV3) LoadArbitration(round int) (*domain.WorldArbitrationReceipt, error) {
	return withProjectedReadResult(v.store.ProjectedV2(), func() (*domain.WorldArbitrationReceipt, error) {
		next, err := v.refresh()
		if err != nil {
			return nil, err
		}
		verified, err := next.loadRound(round)
		if err != nil || verified == nil {
			return nil, err
		}
		r := verified.Receipt()
		return &r, nil
	})
}

// A provisional/hard receipt is durably recorded here, with no cycle finalizer
// and no cursor, clock, physical, knowledge or memory publication.
func (v *CharacterArbitrationV3) SaveArbitration(receipt domain.WorldArbitrationReceipt) error {
	return v.store.withCharacterActivationWrite(func() error {
		next, err := v.refresh()
		if err != nil {
			return err
		}
		sources, err := next.sources(receipt.Round)
		if err != nil {
			return err
		}
		verified, err := domain.FinalizeCharacterArbitrationRoundV1(sources, receipt)
		if err != nil {
			return err
		}
		if !jsonValuesEqual(verified.Receipt(), receipt) {
			return fmt.Errorf("save v3 arbitration requires exact finalized receipt")
		}
		return next.proofs.writeProof(characterAgentArbitrationPath(receipt.GenerationID, receipt.Chapter, receipt.Round), receipt)
	})
}

func (v *CharacterArbitrationV3) FinalizeCycle(usage []domain.CharacterAgentUsage) (domain.CharacterActivationCycle, error) {
	return withProjectedReadResult(v.store.ProjectedV2(), func() (domain.CharacterActivationCycle, error) {
		next, err := v.refresh()
		if err != nil {
			return domain.CharacterActivationCycle{}, err
		}
		return next.finalizeCycle(usage)
	})
}

func (v *CharacterArbitrationV3) finalizeCycle(usage []domain.CharacterAgentUsage) (domain.CharacterActivationCycle, error) {
	session := v.prefix.Session()
	cycle := domain.CharacterActivationCycle{Version: domain.CharacterActivationCycleV3Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Index: len(session.CycleDigests) + 1, ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: v.input.Digest, WorkContinuations: v.Continuations()}
	if len(session.CycleDigests) > 0 {
		cycle.PreviousDigest = session.CycleDigests[len(session.CycleDigests)-1]
	}
	cycle.Evidence = domain.CharacterAgentEvidenceBundle{Version: domain.CharacterActivationRoundEvidenceV3Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Registry: v.input.Registry, Stimulus: v.input.Stimulus, Activation: v.input.Activation, ProtocolDigest: v.admission.Protocol, Usage: copyContinuationStoreValue(usage)}
	var last *domain.VerifiedCharacterArbitrationRoundV1
	for round := 1; round <= 2; round++ {
		verified, err := v.loadRound(round)
		if err != nil {
			return cycle, err
		}
		if verified == nil {
			break
		}
		last = verified
		cycle.Evidence.Arbitrations = append(cycle.Evidence.Arbitrations, verified.Receipt())
	}
	if last == nil {
		return cycle, fmt.Errorf("v3 cycle has no durable arbitration")
	}
	cycle.Evidence.Observations = last.Sources().Observations()
	cycle.Evidence.Proposals = last.Sources().SubmittedProposals()
	for _, o := range cycle.Evidence.Observations {
		if o.Round == 1 {
			cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, o.MemoryRoot)
		}
	}
	return domain.FinalizeCharacterActivationCycleV3(v.prefix, v.input, cycle)
}

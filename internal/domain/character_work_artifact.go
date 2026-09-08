package domain

import (
	"fmt"
	"strings"
)

const CharacterWorkArtifactPolicyV1 = "character-work-artifact:receipt.v1"

const (
	CharacterWorkArtifactLimitV1         = 64
	CharacterWorkArtifactClaimLimitV1    = 64
	CharacterWorkArtifactTextLimitV1     = 1000
	CharacterWorkArtifactRevisionLimitV1 = 64
)

type CharacterWorkMaterialInputV1 struct {
	ResourceID string  `json:"resource_id"`
	Amount     float64 `json:"amount"`
}

// A claim is what its author intends to write, not a certificate of truth.
// LineageRoots is Host-derived in the artifact, never accepted from an intent.
type CharacterWorkArtifactClaimV1 struct {
	ClaimID       string   `json:"claim_id"`
	Text          string   `json:"text"`
	EpistemicKind string   `json:"epistemic_kind"`
	SourceRefs    []string `json:"source_refs"`
	AttributedTo  string   `json:"attributed_to,omitempty"`
	LineageRoots  []string `json:"lineage_roots,omitempty"`
}

type CharacterWorkOutputRequestV1 struct {
	OutputKey             string                         `json:"output_key"`
	ResourceID            string                         `json:"resource_id,omitempty"`
	ExpectedVersionDigest string                         `json:"expected_version_digest,omitempty"`
	Label                 string                         `json:"label"`
	MaterialInputs        []CharacterWorkMaterialInputV1 `json:"material_inputs,omitempty"`
	Claims                []CharacterWorkArtifactClaimV1 `json:"claims"`
}

type CharacterWorkOutputResultV1 struct {
	OutputKey string   `json:"output_key"`
	Status    string   `json:"status"` // created / updated / blocked
	AtDay     float64  `json:"at_day"`
	ClaimIDs  []string `json:"claim_ids,omitempty"`
	Complete  bool     `json:"complete"`
}

type CharacterArtifactPlacementV1 struct {
	Kind             string `json:"kind"` // with_actor / stored
	Location         string `json:"location"`
	CustodianAgentID string `json:"custodian_agent_id"`
}

// There is only one physical/content directory: Resources[].Artifact.
// VersionDigest covers content, not movement or signatures; the enclosing
// physical root separately authenticates every field of this record.
type CharacterWorkArtifactV1 struct {
	Version               string                                `json:"version"`
	ResourceID            string                                `json:"resource_id"`
	CreatorAgentID        string                                `json:"creator_agent_id"`
	OriginGenerationID    string                                `json:"origin_generation_id"`
	OriginChapter         int                                   `json:"origin_chapter"`
	OriginCycle           int                                   `json:"origin_cycle"`
	OriginTaskID          string                                `json:"origin_task_id"`
	OutputKey             string                                `json:"output_key"`
	OriginProposalDigest  string                                `json:"origin_proposal_digest"`
	Revision              int                                   `json:"revision"`
	PreviousVersionDigest string                                `json:"previous_version_digest,omitempty"`
	VersionDigest         string                                `json:"version_digest"`
	Status                string                                `json:"status"` // draft / complete
	Claims                []CharacterWorkArtifactClaimV1        `json:"claims"`
	Materials             []CharacterWorkMaterialInputV1        `json:"materials"`
	Placement             CharacterArtifactPlacementV1          `json:"placement"`
	CreatedAtDay          float64                               `json:"created_at_day"`
	UpdatedAtDay          float64                               `json:"updated_at_day"`
	Signatures            []CharacterArtifactSignatureReceiptV1 `json:"signatures,omitempty"`
}

type CharacterArtifactReadVersionV1 struct {
	TaskID        string   `json:"task_id"`
	ResourceID    string   `json:"resource_id"`
	VersionDigest string   `json:"version_digest"`
	ClaimIDs      []string `json:"claim_ids"`
}

type CharacterArtifactReadResultV1 struct {
	ResourceID    string   `json:"resource_id"`
	VersionDigest string   `json:"version_digest"`
	ClaimIDs      []string `json:"claim_ids"`
	AtDay         float64  `json:"at_day"`
}

type CharacterArtifactSignIntentV1 struct {
	TaskID        string   `json:"task_id"`
	ResourceID    string   `json:"resource_id"`
	VersionDigest string   `json:"version_digest"`
	ClaimIDs      []string `json:"claim_ids"`
	Scope         string   `json:"scope"`
}

type CharacterArtifactAccessIntentV1 struct {
	ResourceID    string `json:"resource_id"`
	VersionDigest string `json:"version_digest"`
	ToCharacter   string `json:"to_character"`
	Access        string `json:"access"`
}

type CharacterArtifactSignatureResultV1 struct {
	ResourceID    string   `json:"resource_id"`
	VersionDigest string   `json:"version_digest"`
	ClaimIDs      []string `json:"claim_ids"`
	Scope         string   `json:"scope"`
	AtDay         float64  `json:"at_day"`
}

type CharacterArtifactSignatureReceiptV1 struct {
	Signer               string   `json:"signer"`
	GenerationID         string   `json:"generation_id"`
	ResourceID           string   `json:"resource_id"`
	VersionDigest        string   `json:"version_digest"`
	ClaimIDs             []string `json:"claim_ids"`
	Scope                string   `json:"scope"`
	AtDay                float64  `json:"at_day"`
	SignerAgentID        string   `json:"signer_agent_id"`
	SourceProposalDigest string   `json:"source_proposal_digest"`
	Chapter              int      `json:"chapter"`
	Cycle                int      `json:"cycle"`
	SignatureDigest      string   `json:"signature_digest"`
}

// Exact versions actually authored/read by this owner. Old known versions
// survive later edits without silently acquiring the revised text.
type CharacterArtifactKnowledgeV1 struct {
	Signatures           []CharacterArtifactSignatureReceiptV1 `json:"signatures,omitempty"`
	Placement            CharacterArtifactPlacementV1          `json:"placement"`
	GenerationID         string                                `json:"generation_id"`
	Status               string                                `json:"status"`
	Revision             int                                   `json:"revision"`
	ResourceID           string                                `json:"resource_id"`
	VersionDigest        string                                `json:"version_digest"`
	Kind                 string                                `json:"kind"` // authored / read
	Claims               []CharacterWorkArtifactClaimV1        `json:"claims"`
	AtDay                float64                               `json:"at_day"`
	Chapter              int                                   `json:"chapter"`
	Cycle                int                                   `json:"cycle"`
	SourceProposalDigest string                                `json:"source_proposal_digest"`
	KnowledgeDigest      string                                `json:"knowledge_digest"`
}

type CharacterArtifactClaimViewV1 struct {
	ID            string   `json:"id"`
	ClaimID       string   `json:"claim_id"`
	Text          string   `json:"text"`
	EpistemicKind string   `json:"epistemic_kind"`
	AttributedTo  string   `json:"attributed_to,omitempty"`
	LineageRoots  []string `json:"lineage_roots"`
}

type CharacterArtifactViewV1 struct {
	Signatures    []CharacterArtifactSignatureReceiptV1 `json:"signatures,omitempty"`
	Placement     *CharacterArtifactPlacementV1         `json:"placement,omitempty"`
	Status        string                                `json:"status,omitempty"`
	Revision      int                                   `json:"revision,omitempty"`
	ResourceID    string                                `json:"resource_id"`
	VersionDigest string                                `json:"version_digest"`
	KnowledgeKind string                                `json:"knowledge_kind"`
	Claims        []CharacterArtifactClaimViewV1        `json:"claims"`
	AtDay         float64                               `json:"at_day"`
	Chapter       int                                   `json:"chapter"`
	Cycle         int                                   `json:"cycle"`
}

func HasCharacterWorkArtifactPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterWorkArtifactPolicyV1)
}

func CharacterWorkArtifactResourceIDV1(generation, owner, task, key string) string {
	digest, _ := characterAgentDigest(struct{ Policy, Generation, Owner, Task, Key string }{CharacterWorkArtifactPolicyV1, generation, owner, task, key})
	return "res_" + strings.TrimPrefix(digest, "sha256:")
}

func ComputeCharacterWorkArtifactVersionDigestV1(a CharacterWorkArtifactV1) (string, error) {
	return characterAgentDigest(struct {
		Policy, ResourceID, CreatorAgentID, OriginGenerationID, OriginTaskID, OutputKey string
		OriginChapter, OriginCycle, Revision                                            int
		OriginProposalDigest, PreviousVersionDigest, Status                             string
		Claims                                                                          []CharacterWorkArtifactClaimV1
	}{CharacterWorkArtifactPolicyV1, a.ResourceID, a.CreatorAgentID, a.OriginGenerationID, a.OriginTaskID, a.OutputKey, a.OriginChapter, a.OriginCycle, a.Revision, a.OriginProposalDigest, a.PreviousVersionDigest, a.Status, a.Claims})
}

func artifactPolicySourcesV1(sources []string) error {
	if !HasCharacterWorkArtifactPolicyV1(sources) {
		return nil
	}
	for _, required := range []string{CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterSelfExperiencePolicyV2, CharacterSelfChronologyPolicyV1} {
		if !physicalContainsRefV2(sources, required) {
			return fmt.Errorf("work artifacts require explicit v3 verified-round/self chronology policies")
		}
	}
	return nil
}

package domain

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
)

const artifactShapeMaterialID = "res_0000000000000011"
const artifactShapeSourceID = "res_0000000000000012"
const artifactShapeOtherID = "res_0000000000000013"

func artifactShapeMustV1(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func artifactShapeCopyV1[T any](t *testing.T, value T) T {
	t.Helper()
	copy, err := cloneVerifiedActivationValue(value)
	artifactShapeMustV1(t, err)
	return copy
}

func artifactShapeFixtureV1(t *testing.T) (WorldPhysicalStateV2, int) {
	t.Helper()
	proposal := "sha256:" + strings.Repeat("a", 64)
	a := CharacterWorkArtifactV1{Version: CharacterWorkArtifactPolicyV1, CreatorAgentID: "ca_author", OriginGenerationID: "pg2_artifact", OriginChapter: 1, OriginCycle: 1, OriginTaskID: "write_record", OutputKey: "record", OriginProposalDigest: proposal, Revision: 1, Status: "draft", Materials: []CharacterWorkMaterialInputV1{{ResourceID: artifactShapeMaterialID, Amount: 1}}, Placement: CharacterArtifactPlacementV1{Kind: "with_actor", Location: "柜台", CustodianAgentID: "ca_author"}, CreatedAtDay: .1, UpdatedAtDay: .1,
		Claims: []CharacterWorkArtifactClaimV1{{ClaimID: "original_field", Text: "旧页记载六十升。", EpistemicKind: "document_statement", SourceRefs: []string{CharacterSourceRefV2("ca_author", "known_original")}, LineageRoots: []string{artifactLineageRootV1("resource", artifactShapeSourceID)}}, {ClaimID: "reported_use", Text: "另一角色说这笔油用于经营。", EpistemicKind: "attributed_statement", AttributedTo: "乙", SourceRefs: []string{CharacterSourceRefV2("ca_author", "received_report")}, LineageRoots: []string{artifactLineageRootV1("speaker", "ca_speaker")}}}}
	a.ResourceID = CharacterWorkArtifactResourceIDV1(a.OriginGenerationID, a.CreatorAgentID, a.OriginTaskID, a.OutputKey)
	var err error
	a.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(a)
	artifactShapeMustV1(t, err)
	amount := 10.0
	state := WorldPhysicalStateV2{Version: WorldPhysicalStateV2Version, Resources: []WorldResourceBalanceV2{{ResourceID: artifactShapeMaterialID, Name: "纸张", Unit: "张", ActualAmount: &amount}, {ResourceID: artifactShapeSourceID, Name: "原始资料"}, {ResourceID: artifactShapeOtherID, Name: "另一来源"}, {ResourceID: a.ResourceID, Name: "工作附件", Artifact: &a, ReadableFacts: artifactReadableFactsV1(a)}}, Actors: []CharacterPhysicalStateV2{{AgentID: "ca_author", Character: "甲", Location: "柜台", Resources: []CharacterResourceHoldingV2{{ResourceID: a.ResourceID, PerceivedName: "本人工作附件", Access: "shared", Perception: ResourcePerceptionV2{Kind: "unknown"}}}}, {AgentID: "ca_reader", Character: "乙", Location: "门口", Resources: []CharacterResourceHoldingV2{{ResourceID: a.ResourceID, PerceivedName: "工作附件", Access: "none", Perception: ResourcePerceptionV2{Kind: "unknown"}}}}}}
	artifactShapeMustV1(t, ValidateWorldPhysicalStateV2(state))
	return state, 3
}

func artifactShapeKnowledgeV1(t *testing.T, owner, kind string, a CharacterWorkArtifactV1, claims []CharacterWorkArtifactClaimV1, day float64) CharacterArtifactKnowledgeV1 {
	t.Helper()
	k := CharacterArtifactKnowledgeV1{Placement: a.Placement, GenerationID: "pg2_artifact", Status: a.Status, Revision: a.Revision, ResourceID: a.ResourceID, VersionDigest: a.VersionDigest, Kind: kind, Claims: artifactShapeCopyV1(t, claims), AtDay: day, Chapter: 1, Cycle: 2, SourceProposalDigest: "sha256:" + strings.Repeat("b", 64)}
	var err error
	k.KnowledgeDigest, err = ComputeCharacterArtifactKnowledgeDigestV1(owner, k)
	artifactShapeMustV1(t, err)
	return k
}

func TestArtifactShapeStableContentAndSignatureDigests(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	resource := state.Resources[index]
	a := resource.Artifact
	signature := CharacterArtifactSignatureReceiptV1{Signer: "乙", GenerationID: "pg2_sign", ResourceID: a.ResourceID, VersionDigest: a.VersionDigest, ClaimIDs: []string{a.Claims[0].ClaimID}, Scope: "仅确认本人看到的该条声明", AtDay: .11, SignerAgentID: "ca_reader", SourceProposalDigest: "sha256:" + strings.Repeat("c", 64), Chapter: 1, Cycle: 3}
	var err error
	signature.SignatureDigest, err = ComputeCharacterArtifactSignatureDigestV1(signature)
	artifactShapeMustV1(t, err)
	a.Signatures = []CharacterArtifactSignatureReceiptV1{signature}
	a.Placement = CharacterArtifactPlacementV1{Kind: "stored", Location: "固定夹", CustodianAgentID: "ca_author"}
	digest, err := ComputeCharacterWorkArtifactVersionDigestV1(*a)
	artifactShapeMustV1(t, err)
	if digest != a.VersionDigest {
		t.Fatal("location/signature changed content version")
	}
	artifactShapeMustV1(t, validateCharacterWorkArtifactV1(resource))
	a.PreviousVersionDigest, a.VersionDigest = a.VersionDigest, ""
	a.Revision = 2
	a.Status = "complete"
	a.UpdatedAtDay = .2
	a.Claims[0].Text = "本版新增说明。"
	a.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(*a)
	artifactShapeMustV1(t, err)
	resource.ReadableFacts = artifactReadableFactsV1(*a)
	artifactShapeMustV1(t, validateCharacterWorkArtifactV1(resource)) // Old signature stays bound to old version only.
	if a.Signatures[0].VersionDigest == a.VersionDigest {
		t.Fatal("old signature silently rebound to new content")
	}
	changed := a.Signatures[0]
	changed.Scope = "改变签认范围"
	newDigest, err := ComputeCharacterArtifactSignatureDigestV1(changed)
	artifactShapeMustV1(t, err)
	if newDigest == changed.SignatureDigest {
		t.Fatal("signature digest ignored scope")
	}
	for _, fact := range resource.ReadableFacts {
		if !strings.Contains(fact.Text, "派生文档声明") || !strings.Contains(fact.Text, "不自动等于世界真相") {
			t.Fatal("derived statement became certified truth")
		}
	}
}

func TestArtifactShapeRejectsForgedAndUnboundedState(t *testing.T) {
	for name, change := range map[string]func(*WorldResourceBalanceV2){
		"quantity": func(r *WorldResourceBalanceV2) { r.ActualAmount = physicalTestNumber(1) },
		"unit":     func(r *WorldResourceBalanceV2) { r.Unit = "张" },
		"identity": func(r *WorldResourceBalanceV2) { r.Artifact.OutputKey = "forged" },
		"lineage": func(r *WorldResourceBalanceV2) {
			r.Artifact.Claims[0].LineageRoots = []string{"raw private source details"}
		},
		"empty lineage": func(r *WorldResourceBalanceV2) { r.Artifact.Claims[0].LineageRoots = nil },
		"material":      func(r *WorldResourceBalanceV2) { r.Artifact.Materials[0].Amount = math.NaN() },
		"duplicate materials": func(r *WorldResourceBalanceV2) {
			r.Artifact.Materials = append(r.Artifact.Materials, r.Artifact.Materials[0])
		},
		"no materials":   func(r *WorldResourceBalanceV2) { r.Artifact.Materials = nil },
		"time":           func(r *WorldResourceBalanceV2) { r.Artifact.UpdatedAtDay = -1 },
		"placement":      func(r *WorldResourceBalanceV2) { r.Artifact.Placement.Location = "" },
		"claims":         func(r *WorldResourceBalanceV2) { r.Artifact.Claims = nil },
		"readable truth": func(r *WorldResourceBalanceV2) { r.ReadableFacts[0].Text = "世界真相已确认" },
		"new truth type": func(r *WorldResourceBalanceV2) { r.Artifact.Claims[0].EpistemicKind = "independent_evidence" },
		"attribution":    func(r *WorldResourceBalanceV2) { r.Artifact.Claims[1].AttributedTo = "" },
		"revision":       func(r *WorldResourceBalanceV2) { r.Artifact.Revision = 65 },
		"wrong prior":    func(r *WorldResourceBalanceV2) { r.Artifact.Revision = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			state, index := artifactShapeFixtureV1(t)
			r := state.Resources[index]
			change(&r)
			if err := validateCharacterWorkArtifactV1(r); err == nil {
				t.Fatal("invalid artifact accepted")
			}
		})
	}
}

func TestArtifactViewsMergeActualReadsAndKeepOwnerHistoryPrivate(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	first := artifactShapeKnowledgeV1(t, "ca_author", "authored", *a, a.Claims[:1], .11)
	second := artifactShapeKnowledgeV1(t, "ca_author", "read", *a, a.Claims[1:], .12)
	state.Actors[0].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{first, second}
	before, _ := json.Marshal(state)
	views, err := BuildCharacterArtifactViewsV1(state, "ca_author")
	artifactShapeMustV1(t, err)
	if len(views) != 1 || len(views[0].Claims) != 2 || views[0].KnowledgeKind != "authored" || views[0].AtDay != .12 {
		t.Fatal("partial reads were lost instead of unioned")
	}
	if !strings.Contains(views[0].Claims[0].Text, "派生文档声明") || views[0].Claims[0].ID != CharacterArtifactClaimFactIDV1("ca_author", a.ResourceID, a.VersionDigest, views[0].Claims[0].ClaimID) {
		t.Fatal("claim lost safe kind/owner/version binding")
	}
	views[0].Claims[0].LineageRoots[0] = "changed"
	views[0].Placement.Location = "changed"
	again, err := BuildCharacterArtifactViewsV1(state, "ca_author")
	artifactShapeMustV1(t, err)
	if again[0].Claims[0].LineageRoots[0] == "changed" || again[0].Placement.Location == "changed" {
		t.Fatal("view getter leaked mutable state")
	}
	foreign, err := BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(foreign) != 0 {
		t.Fatal("another owner's claims leaked")
	}
	if _, err := BuildCharacterArtifactViewsV1(state, "ca_unknown"); err == nil {
		t.Fatal("unknown owner accepted")
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("projection mutated source knowledge")
	}
	borrowed := state.Actors[0].ArtifactKnowledge[0]
	state.Actors[1].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{borrowed}
	if _, err := BuildCharacterArtifactViewsV1(state, "ca_reader"); err == nil {
		t.Fatal("borrowed owner knowledge digest authorized foreign content")
	}
}

func TestArtifactViewsDoNotObserveRemoteRevisionAndExposeOnlyLocalUnreadCapability(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	known := artifactShapeKnowledgeV1(t, "ca_reader", "read", *a, a.Claims[:1], .11)
	state.Actors[1].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{known}
	state.Actors[1].Resources[0].Access = "shared"
	oldDigest := a.VersionDigest
	a.PreviousVersionDigest = oldDigest
	a.Revision = 2
	a.Status = "complete"
	a.UpdatedAtDay = .2
	a.Claims[0].Text = "UNREAD_NEW_PRIVATE_CONTENT"
	a.Placement = CharacterArtifactPlacementV1{Kind: "stored", Location: "档案室", CustodianAgentID: "ca_author"}
	var err error
	a.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(*a)
	artifactShapeMustV1(t, err)
	state.Resources[index].ReadableFacts = artifactReadableFactsV1(*a)
	views, err := BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	raw, _ := json.Marshal(views)
	if len(views) != 1 || views[0].VersionDigest != oldDigest || views[0].Revision != 1 || views[0].Status != "draft" || views[0].Placement.Location != known.Placement.Location || strings.Contains(string(raw), "UNREAD_NEW_PRIVATE_CONTENT") || strings.Contains(string(raw), a.VersionDigest) {
		t.Fatal("remote update refreshed old private knowledge")
	}
	state.Actors[1].Location = "档案室"
	views, err = BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(views) != 2 {
		t.Fatal("local accessible updated artifact cannot be requested for reading")
	}
	for _, view := range views {
		if view.VersionDigest == a.VersionDigest {
			if view.KnowledgeKind != "unread" || view.Claims == nil || len(view.Claims) != 0 || view.Status != "" || view.Revision != 0 || view.AtDay != 0 || view.Chapter != 0 || view.Cycle != 0 || view.Placement != nil {
				t.Fatal("unread capability leaked world metadata/content")
			}
		}
	}
	state.Actors[1].Resources[0].Access = "none"
	views, err = BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(views) != 1 {
		t.Fatal("lost access still exposed current unread capability")
	}
	state.Actors[1].Resources[0].Access = "shared"
	state.Actors[1].Resources[0].Perception.Kind = "unaware"
	views, err = BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(views) != 1 {
		t.Fatal("unaware actor acquired current version")
	}
}

func TestArtifactIndependentSourcesRejectCopiedLineage(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	if _, err := ArtifactIndependentSourcesV1(state, []string{artifactShapeSourceID, a.ResourceID}); err == nil {
		t.Fatal("document plus its derived attachment treated as independent")
	}
	roots, err := ArtifactIndependentSourcesV1(state, []string{artifactShapeOtherID, a.ResourceID})
	artifactShapeMustV1(t, err)
	if len(roots) != 3 {
		t.Fatal("lineage roots lost")
	}
	for _, root := range roots {
		if !characterSourceDigestPatternV2.MatchString(root) {
			t.Fatal("raw lineage text leaked")
		}
	}
	copyArtifact := artifactShapeCopyV1(t, *a)
	copyArtifact.OutputKey = "another_attachment"
	copyArtifact.ResourceID = CharacterWorkArtifactResourceIDV1(copyArtifact.OriginGenerationID, copyArtifact.CreatorAgentID, copyArtifact.OriginTaskID, copyArtifact.OutputKey)
	copyArtifact.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(copyArtifact)
	artifactShapeMustV1(t, err)
	state.Resources = append(state.Resources, WorldResourceBalanceV2{ResourceID: copyArtifact.ResourceID, Name: "另一附件", Artifact: &copyArtifact, ReadableFacts: artifactReadableFactsV1(copyArtifact)})
	if _, err := ArtifactIndependentSourcesV1(state, []string{a.ResourceID, copyArtifact.ResourceID}); err == nil {
		t.Fatal("two copies of same original source treated as independent")
	}
	if _, err := ArtifactIndependentSourcesV1(state, []string{a.ResourceID, a.ResourceID}); err == nil {
		t.Fatal("same physical source counted twice")
	}
	if _, err := ArtifactIndependentSourcesV1(state, []string{"res_9999999999999999"}); err == nil {
		t.Fatal("unknown lineage source accepted")
	}
	if got, err := ArtifactIndependentSourcesV1(state, nil); err != nil || got != nil {
		t.Fatal("nil lineage selection changed")
	}
	if got, err := ArtifactIndependentSourcesV1(state, []string{}); err != nil || got == nil {
		t.Fatal("explicit empty lineage selection changed")
	}
}

func TestArtifactViewsConcurrentAndNilShapes(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	state.Actors[0].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{artifactShapeKnowledgeV1(t, "ca_author", "authored", *a, a.Claims, .11)}
	before, _ := json.Marshal(state)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			views, err := BuildCharacterArtifactViewsV1(state, "ca_author")
			if err != nil {
				t.Error(err)
				return
			}
			views[0].Claims[0].LineageRoots[0] = "private mutation"
			views[0].Placement.Location = "changed"
		}()
	}
	wg.Wait()
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("concurrent views mutated source")
	}
	if got := artifactReadableFactsV1(CharacterWorkArtifactV1{}); got != nil {
		t.Fatal("nil claims changed")
	}
	if got := artifactReadableFactsV1(CharacterWorkArtifactV1{Claims: []CharacterWorkArtifactClaimV1{}}); got == nil {
		t.Fatal("explicit empty claims changed")
	}
}

func TestArtifactViewsRememberOnlyActuallyKnownVersionBoundSignatures(t *testing.T) {
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	read := artifactShapeKnowledgeV1(t, "ca_reader", "read", *a, a.Claims[:1], .11)
	signed := artifactShapeKnowledgeV1(t, "ca_reader", "signed", *a, a.Claims[:1], .12)
	signature := CharacterArtifactSignatureReceiptV1{Signer: "乙", GenerationID: signed.GenerationID, ResourceID: a.ResourceID, VersionDigest: a.VersionDigest, ClaimIDs: []string{a.Claims[0].ClaimID}, Scope: "只确认这一条陈述", AtDay: signed.AtDay, SignerAgentID: "ca_reader", SourceProposalDigest: signed.SourceProposalDigest, Chapter: signed.Chapter, Cycle: signed.Cycle}
	var err error
	signature.SignatureDigest, err = ComputeCharacterArtifactSignatureDigestV1(signature)
	artifactShapeMustV1(t, err)
	a.Signatures = []CharacterArtifactSignatureReceiptV1{signature}
	signed.Signatures = []CharacterArtifactSignatureReceiptV1{signature}
	signed.KnowledgeDigest, err = ComputeCharacterArtifactKnowledgeDigestV1("ca_reader", signed)
	artifactShapeMustV1(t, err)
	state.Actors[1].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{signed, read}
	// A global signature is not automatically known to its artifact's creator.
	state.Actors[0].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{artifactShapeKnowledgeV1(t, "ca_author", "authored", *a, a.Claims, .1)}
	creator, err := BuildCharacterArtifactViewsV1(state, "ca_author")
	artifactShapeMustV1(t, err)
	if len(creator) != 1 || len(creator[0].Signatures) != 0 {
		t.Fatal("global signature leaked into creator's past knowledge")
	}
	views, err := BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(views) != 1 || views[0].KnowledgeKind != "read" || len(views[0].Signatures) != 1 || views[0].Signatures[0].Signer != "乙" {
		t.Fatal("own actual signature forgotten or read knowledge downgraded")
	}
	views[0].Signatures[0].ClaimIDs[0] = "changed"
	again, err := BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if again[0].Signatures[0].ClaimIDs[0] == "changed" {
		t.Fatal("signature view exposed mutable scope")
	}
	// Access is already none; loss of access does not erase actual signing.
	oldVersion := a.VersionDigest
	a.PreviousVersionDigest = oldVersion
	a.Revision = 2
	a.UpdatedAtDay = .3
	a.Status = "complete"
	a.Claims[0].Text = "新版不得自动获得旧签认"
	a.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(*a)
	artifactShapeMustV1(t, err)
	state.Resources[index].ReadableFacts = artifactReadableFactsV1(*a)
	views, err = BuildCharacterArtifactViewsV1(state, "ca_reader")
	artifactShapeMustV1(t, err)
	if len(views) != 1 || views[0].VersionDigest != oldVersion || views[0].Signatures[0].VersionDigest != oldVersion {
		t.Fatal("old signature migrated to unseen new version")
	}
	for _, kind := range []string{"scope", "version", "future", "unread_claim", "empty_signer"} {
		changed := artifactShapeCopyV1(t, state)
		k := &changed.Actors[1].ArtifactKnowledge[0]
		sig := &k.Signatures[0]
		switch kind {
		case "scope":
			sig.Scope = "forged"
		case "version":
			sig.VersionDigest = a.VersionDigest
		case "future":
			sig.AtDay = k.AtDay + 1
		case "unread_claim":
			sig.ClaimIDs = append(sig.ClaimIDs, a.Claims[1].ClaimID)
		case "empty_signer":
			sig.Signer = ""
		}
		if kind != "scope" {
			sig.SignatureDigest, err = ComputeCharacterArtifactSignatureDigestV1(*sig)
			artifactShapeMustV1(t, err)
		}
		k.KnowledgeDigest, err = ComputeCharacterArtifactKnowledgeDigestV1("ca_reader", *k)
		artifactShapeMustV1(t, err)
		if _, err := BuildCharacterArtifactViewsV1(changed, "ca_reader"); err == nil {
			t.Fatalf("invalid known signature accepted: %s", kind)
		}
	}
}

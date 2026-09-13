package domain

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func artifactSignReferenceFixtureV1(t *testing.T, kind string) (CharacterDecisionProposal, CharacterObservationPacket) {
	t.Helper()
	state, index := artifactShapeFixtureV1(t)
	a := state.Resources[index].Artifact
	a.Claims[0].Text = "OWNER_CLAIM_TEXT_MUST_NOT_APPEAR_IN_ERROR"
	a.Claims[1].Text = "PENDING_CLAIM_TEXT_MUST_NOT_APPEAR_IN_ERROR"
	a.Claims[1].EpistemicKind = "pending"
	a.Claims[1].AttributedTo = ""
	foreign := artifactShapeCopyV1(t, a.Claims[0])
	foreign.ClaimID = "foreign_only_claim"
	foreign.Text = "OTHER_OWNER_PRIVATE_TEXT"
	a.Claims = append(a.Claims, foreign)
	var err error
	a.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(*a)
	artifactShapeMustV1(t, err)
	state.Resources[index].ReadableFacts = artifactReadableFactsV1(*a)
	state.Actors[0].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{artifactShapeKnowledgeV1(t, "ca_author", kind, *a, a.Claims[:2], .11)}
	state.Actors[1].ArtifactKnowledge = []CharacterArtifactKnowledgeV1{artifactShapeKnowledgeV1(t, "ca_reader", "read", *a, a.Claims[2:], .11)}
	views, err := BuildCharacterArtifactViewsV1(state, "ca_author")
	artifactShapeMustV1(t, err)
	if len(views) != 1 || len(views[0].Claims) != 2 {
		t.Fatal("fixture lost exact owner known-claim view")
	}
	f := artifactFlowFixtureV1(t)
	o := f.observations[0]
	o.AgentID = "ca_author"
	o.ArtifactViews = views
	o.ResourceViews, err = BuildCharacterResourceViewsV2(state, "ca_author")
	artifactShapeMustV1(t, err)
	p := CharacterDecisionProposal{AgentID: o.AgentID, SelfTasks: []CharacterSelfTaskV2{{TaskID: "sign-own-scope", Kind: "work", Action: "仅签本人已知声明", ResourceIDs: []string{a.ResourceID}}}, ArtifactSigns: []CharacterArtifactSignIntentV1{{TaskID: "sign-own-scope", ResourceID: a.ResourceID, VersionDigest: a.VersionDigest, ClaimIDs: []string{a.Claims[0].ClaimID, a.Claims[1].ClaimID}, Scope: "只确认本人知悉这些陈述，包括待核项；不将陈述签成世界真相"}}}
	return p, o
}

func TestArtifactSignReferenceAcceptsKnownClaimIDsIncludingAuthoredPending(t *testing.T) {
	for _, kind := range []string{"authored", "read"} {
		t.Run(kind, func(t *testing.T) {
			p, o := artifactSignReferenceFixtureV1(t, kind)
			before, _ := json.Marshal(struct {
				P CharacterDecisionProposal
				O CharacterObservationPacket
			}{p, o})
			artifactShapeMustV1(t, ValidateCharacterArtifactIntentV1(p, o))
			after, _ := json.Marshal(struct {
				P CharacterDecisionProposal
				O CharacterObservationPacket
			}{p, o})
			if !bytes.Equal(before, after) {
				t.Fatal("signing-intent validation mutated source or executed a signature")
			}
		})
	}
}

func TestArtifactSignReferenceFactIDIsNotClaimIDAndDiagnosticsStayOwnerScoped(t *testing.T) {
	p, o := artifactSignReferenceFixtureV1(t, "authored")
	view := o.ArtifactViews[0]
	p.ArtifactSigns[0].ClaimIDs[1] = CharacterArtifactClaimFactIDV1(o.AgentID, view.ResourceID, view.VersionDigest, view.Claims[1].ClaimID)
	before, _ := json.Marshal(o)
	err := ValidateCharacterArtifactIntentV1(p, o)
	if err == nil {
		t.Fatal("owner/version fact ID was accepted as the artifact's ClaimID")
	}
	message := err.Error()
	for _, want := range []string{"cannot sign an unread artifact claim", "artifact_signs[0].claim_ids[1]", "submitted claim_id", "known declaration IDs", "no signing action occurred", "derived fact ID", view.Claims[0].ClaimID, view.Claims[1].ClaimID} {
		if !strings.Contains(message, want) {
			t.Fatalf("diagnostic omitted %q: %s", want, message)
		}
	}
	for _, hidden := range []string{"OWNER_CLAIM_TEXT_MUST_NOT_APPEAR_IN_ERROR", "PENDING_CLAIM_TEXT_MUST_NOT_APPEAR_IN_ERROR", "OTHER_OWNER_PRIVATE_TEXT", "foreign_only_claim"} {
		if strings.Contains(message, hidden) {
			t.Fatalf("diagnostic leaked %q", hidden)
		}
	}
	after, _ := json.Marshal(o)
	if !bytes.Equal(before, after) {
		t.Fatal("rejection changed owner observation/signature state")
	}
}

func TestArtifactSignReferenceKnownIDListIsSortedAndExplicitlyBounded(t *testing.T) {
	known := map[string]bool{}
	for _, id := range []string{"claim09", "claim03", "claim07", "claim00", "claim08", "claim02", "claim06", "claim05", "claim01", "claim04"} {
		known[id] = true
	}
	want := `["claim00", "claim01", "claim02", "claim03", "claim04", "claim05", "claim06", "claim07"] (omitted=2)`
	first := artifactSigningClaimReferenceErrorV1(1, 2, "wrong-reference", known).Error()
	if !strings.Contains(first, "artifact_signs[1].claim_ids[2]") || !strings.Contains(first, want) || strings.Contains(first, "claim08") || strings.Contains(first, "claim09") {
		t.Fatalf("known-claim inventory lacks exact sorted limit: %s", first)
	}
	for i := 0; i < 10; i++ {
		if got := artifactSigningClaimReferenceErrorV1(1, 2, "wrong-reference", known).Error(); got != first {
			t.Fatal("map iteration changed diagnostic ordering")
		}
	}
}

func TestArtifactSignReferenceWrongIdentityVersionOrMissingWorkStillReject(t *testing.T) {
	for _, kind := range []string{"wrong-id", "wrong-version", "missing-work"} {
		t.Run(kind, func(t *testing.T) {
			p, o := artifactSignReferenceFixtureV1(t, "read")
			switch kind {
			case "wrong-id":
				p.ArtifactSigns[0].ClaimIDs[1] = "unrelated-claim"
			case "wrong-version":
				p.ArtifactSigns[0].VersionDigest = "sha256:" + strings.Repeat("f", 64)
			case "missing-work":
				p.SelfTasks = nil
			}
			if err := ValidateCharacterArtifactIntentV1(p, o); err == nil {
				t.Fatal("invalid signing intent accepted")
			}
		})
	}
}

func TestArtifactSignReferenceUntrustedIDIsBoundedAndJSONQuoted(t *testing.T) {
	for _, id := range []string{"bad\nclaim\"\\id", "bad\x01\x1bclaim", strings.Repeat("UNTRUSTED_LONG_ID\n", 2000)} {
		t.Run(strconv.Itoa(len(id)), func(t *testing.T) {
			p, o := artifactSignReferenceFixtureV1(t, "authored")
			p.ArtifactSigns[0].ClaimIDs[1] = id
			err := ValidateCharacterArtifactIntentV1(p, o)
			if err == nil {
				t.Fatal("malformed unknown claim ID accepted")
			}
			message := err.Error()
			if strings.ContainsAny(message, "\n\r\t") || len(message) > 2048 {
				t.Fatalf("unbounded or unescaped signing diagnostic: bytes=%d", len(message))
			}
			if !strings.Contains(message, "artifact_signs[0].claim_ids[1]") {
				t.Fatal("malformed reference lost exact index")
			}
			if len(id) < 128 {
				quoted, _ := json.Marshal(id)
				if !strings.Contains(message, string(quoted)) {
					t.Fatalf("identifier is not JSON quoted: %s", message)
				}
			} else if strings.Contains(message, "UNTRUSTED_LONG_ID") {
				t.Fatal("long untrusted ID was echoed")
			}
		})
	}
}

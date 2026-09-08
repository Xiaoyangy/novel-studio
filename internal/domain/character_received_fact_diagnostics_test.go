package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func receivedDiagnosticFixtureV2(t *testing.T, count int) physicalProtocolFixture {
	t.Helper()
	f := newPhysicalProtocolFixture(t)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("message_%02d", i)
		if i == 0 {
			id = "request_handler_confirmation"
		}
		if i == 1 {
			id = "clarify_limited_correction"
		}
		f.proposals[0].Communications = append(f.proposals[0].Communications, CharacterCommunicationV2{ID: id, ToCharacter: "乙", Kind: "information", Text: "PRIVATE_TEXT_不得回显的原始通信内容", KnowledgeRefs: f.proposals[0].KnowledgeRefs})
	}
	rebindPhysicalTestProposals(t, &f)
	for _, communication := range f.proposals[0].Communications {
		fact := CharacterReceivedFactV2{Chapter: 1, SourceType: "communication", SourceID: communication.ID, FromAgentID: f.proposals[0].AgentID, SourceProposalDigest: f.proposals[0].Digest, Kind: communication.Kind, Text: communication.Text}
		fact.ID = CharacterReceivedFactIDV2(f.proposals[1].AgentID, fact)
		f.receipt.Resolutions[1].PostState.ReceivedFacts = append(f.receipt.Resolutions[1].PostState.ReceivedFacts, fact)
	}
	return f
}

func receivedDiagnosticTransitionV2(f physicalProtocolFixture) error {
	before := *f.stimulus.PhysicalState
	after, _ := cloneVerifiedActivationValue(before)
	proposals := map[string]CharacterDecisionProposal{}
	resolutions := map[string]CharacterDecisionResolution{}
	for _, p := range f.proposals {
		proposals[p.AgentID] = p
	}
	for _, r := range f.receipt.Resolutions {
		resolutions[r.AgentID] = r
		for i := range after.Actors {
			if after.Actors[i].AgentID == r.AgentID {
				after.Actors[i] = *r.PostState
			}
		}
	}
	return validateReceivedFactsTransitionV2(f.receipt, before, after, proposals, resolutions)
}

func TestReceivedFactDiagnosticsAggregateAndPreserveKernelGate(t *testing.T) {
	f := receivedDiagnosticFixtureV2(t, 2)
	f.receipt.Resolutions[0].CompletionState = "blocked"
	raw, _ := json.Marshal(f.receipt)
	_, err := finalizePhysicalFixture(f)
	if err == nil {
		t.Fatal("blocked sender delivered two communications")
	}
	message := err.Error()
	for _, part := range []string{"actor ca_b received fact ", "lacks an exact delivered communication or authorized document read", "request_handler_confirmation", "clarify_limited_correction", "code=sender_completion_blocked", "path=/resolutions/1/post_state/received_facts/", "related_path=/resolutions/0/completion_state"} {
		if !strings.Contains(message, part) {
			t.Fatalf("missing diagnostic %q: %s", part, message)
		}
	}
	if strings.Count(message, " code=") != 2 || strings.Contains(message, "PRIVATE_TEXT") {
		t.Fatal("not two private-safe issues")
	}
	after, _ := json.Marshal(f.receipt)
	if string(raw) != string(after) {
		t.Fatal("diagnosis altered attempted receipt")
	}
	// Removing one attempted claim leaves only the other actual failure.
	f.receipt.Resolutions[1].PostState.ReceivedFacts = f.receipt.Resolutions[1].PostState.ReceivedFacts[:1]
	_, err = finalizePhysicalFixture(f)
	if err == nil || strings.Count(err.Error(), " code=") != 1 || strings.Contains(err.Error(), "clarify_limited_correction") {
		t.Fatalf("stale/repeated diagnosis: %v", err)
	}
	// A separately valid executed-delivery fixture remains valid. This is not
	// advice to flip live statuses or invent an execution to satisfy validation.
	f = receivedDiagnosticFixtureV2(t, 2)
	valid, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	again, err := FinalizeWorldArbitrationReceipt(valid, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || again.Digest != valid.Digest {
		t.Fatalf("valid receipt changed: %v", err)
	}
	// Golden captured with the original early-return validator in an isolated
	// overlay, not by comparing two entrypoints that share this implementation.
	state, err := ApplyArbitrationPhysicalStateV2(valid, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	encodedReceipt, _ := json.Marshal(valid)
	encodedState, _ := json.Marshal(state)
	if fmt.Sprintf("%x", sha256.Sum256(encodedReceipt)) != "fd9f970b4f86fc18162097d817d1372b86e60a5390bb66ee8b285586d00301da" || fmt.Sprintf("%x", sha256.Sum256(encodedState)) != "add01b54a2eec7cdecb5a08d65ceb975fb58f22e94f37073650cab11c1ae6d12" || valid.Digest != "sha256:4eb44faedfdc1d66cdfa94b6c0cbfe158e1461d4162ba0f3552514be1f52d7bc" {
		t.Fatal("valid receipt/post-state bytes or digest changed from original kernel")
	}
}

func TestReceivedFactDiagnosticsPreciseFailuresAndConditionalReadBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*physicalProtocolFixture)
	}{
		{"outcome", "sender_outcome_blocked", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].Outcome = "blocked" }},
		{"completion", "sender_completion_blocked", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].CompletionState = "blocked" }},
		{"source identity", "sender_proposal_mismatch", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[1].PostState.ReceivedFacts[0].SourceProposalDigest = "sha256:other"
		}},
		{"source text", "source_tuple_mismatch", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[1].PostState.ReceivedFacts[0].Text = "PRIVATE_CHANGED_SECRET"
		}},
		{"chapter", "chapter_mismatch", func(f *physicalProtocolFixture) { f.receipt.Resolutions[1].PostState.ReceivedFacts[0].Chapter = 2 }},
		{"active arrival", "active_received_at_day_forbidden", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[1].PostState.ReceivedFacts[0].ReceivedAtDay = physicalTestNumber(0)
		}},
		{"conditional response", "condition_not_received", func(f *physicalProtocolFixture) {
			f.proposals[0].Communications[0].Kind = "conditional_response"
			f.proposals[0].Communications[0].ConditionKind = "request"
			f.proposals[0].Communications[0].ConditionFromCharacter = "乙"
			f.receipt.Resolutions[1].PostState.ReceivedFacts[0].Kind = "conditional_response"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := receivedDiagnosticFixtureV2(t, 1)
			test.change(&f)
			err := receivedDiagnosticTransitionV2(f)
			if err == nil || !strings.Contains(err.Error(), "code="+test.code) || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("wrong diagnostic: %v", err)
			}
		})
	}
	for _, requested := range []bool{false, true} {
		f := newPhysicalProtocolFixture(t)
		p := &f.proposals[1]
		if requested {
			p.ResourceReads = []ResourceReadRequestV2{{ResourceID: physicalPaperTestID}}
		}
		f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{{ID: "new", Chapter: 1, Kind: "document_statement", SourceType: "resource_read", ResourceID: physicalPaperTestID, SourceID: "printed", SourceProposalDigest: p.Digest, Text: "登记纸上标示九十升。"}}
		want := "read_not_requested"
		if requested {
			want = "no_read_access"
		}
		if err := receivedDiagnosticTransitionV2(f); err == nil || !strings.Contains(err.Error(), "code="+want) {
			t.Fatalf("unread document accepted/wrong cause: %v", err)
		}
	}
}

func TestReceivedFactDiagnosticsKeepHistoricalFactsAndBoundPrivateOutput(t *testing.T) {
	f := receivedDiagnosticFixtureV2(t, 1)
	old := f.receipt.Resolutions[1].PostState.ReceivedFacts[0]
	f.stimulus.PhysicalState.Actors[1].ReceivedFacts = []CharacterReceivedFactV2{old}
	f.receipt.Resolutions[1].PostState.ReceivedFacts[0].Text = "PRIVATE_REWRITE"
	if err := receivedDiagnosticTransitionV2(f); err == nil || !strings.HasPrefix(err.Error(), "received fact was rewritten;") {
		t.Fatalf("history rewrite: %v", err)
	}
	f.receipt.Resolutions[1].PostState.ReceivedFacts = nil
	if err := receivedDiagnosticTransitionV2(f); err == nil || !strings.HasPrefix(err.Error(), "actor ca_b dropped a previously received fact;") {
		t.Fatalf("history deletion: %v", err)
	}
	f = receivedDiagnosticFixtureV2(t, 40)
	f.receipt.Resolutions[0].CompletionState = "blocked"
	message := receivedDiagnosticTransitionV2(f).Error()
	if len(message) > 2048 || strings.Count(message, " code=") > 8 || !strings.Contains(message, "further received_facts issues omitted") || strings.Contains(message, "PRIVATE_") {
		t.Fatalf("unbounded/leaking diagnostic: bytes=%d", len(message))
	}
	// ID slots are not a loophole for echoing body/argument text.
	f = receivedDiagnosticFixtureV2(t, 1)
	f.receipt.Resolutions[1].PostState.ReceivedFacts[0].SourceID = strings.Repeat("秘密参数", 5000)
	message = receivedDiagnosticTransitionV2(f).Error()
	if len(message) > 2048 || !utf8.ValidString(message) || strings.Contains(message, "秘密参数") || strings.Contains(message, "PRIVATE_") {
		t.Fatal("private source-id content leaked")
	}
	collector := &receivedFactTransitionErrorsV2{}
	for range 12 {
		collector.add("legacy prefix", "/x", "bad", "id", "")
	}
	if strings.Count(collector.Error(), " code=") != 8 || !strings.Contains(collector.Error(), "omitted") {
		t.Fatal("issue count is not capped at eight")
	}
}

func TestReceivedFactDiagnosticsConcurrentReadDoesNotMutateSources(t *testing.T) {
	f := receivedDiagnosticFixtureV2(t, 2)
	f.receipt.Resolutions[0].CompletionState = "blocked"
	before, _ := json.Marshal(f.receipt)
	want := receivedDiagnosticTransitionV2(f).Error()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := receivedDiagnosticTransitionV2(f); got == nil || got.Error() != want {
				t.Error("concurrent diagnostic changed")
			}
		}()
	}
	wg.Wait()
	after, _ := json.Marshal(f.receipt)
	if string(before) != string(after) {
		t.Fatal("concurrent diagnosis mutated receipt")
	}
}

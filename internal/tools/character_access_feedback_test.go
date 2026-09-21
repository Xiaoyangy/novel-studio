package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func accessFeedbackToolFixture(t *testing.T, count int) (*ResolveChapterWorldTool, domain.CharacterActivationCycle, *store.CharacterAgentStore) {
	t.Helper()
	base := testutil.CharacterCycle(t, 1, "", nil, 0)
	state := *base.Evidence.Stimulus.PhysicalState
	state.Resources = append([]domain.WorldResourceBalanceV2(nil), state.Resources...)
	state.Actors = append([]domain.CharacterPhysicalStateV2(nil), state.Actors...)
	state.Actors[0].Resources = nil
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("res_%016x", i+1)
		if i > 0 {
			state.Resources = append(state.Resources, domain.WorldResourceBalanceV2{ResourceID: id, Name: "PRIVATE world name"})
		}
		state.Actors[0].Resources = append(state.Actors[0].Resources, domain.CharacterResourceHoldingV2{ResourceID: id, PerceivedName: "本人资源", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"known-fuel"}})
	}
	cycle := testutil.CharacterCycle(t, 1, "", &state, 0)
	e := cycle.Evidence
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewCharacterActivationSession(e.GenerationID, e.Chapter, cycle.ChapterContextDigest, *e.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error { return proofs.SaveRegistrySnapshot(e.GenerationID, e.Chapter, e.Registry) },
		func() error { return proofs.SaveStimulus(e.Stimulus) },
		func() error { return proofs.SaveObservation(e.Observations[0]) },
		func() error { return proofs.SaveActivation(e.Activation) },
		func() error { return proofs.SaveProposal(e.Proposals[0], e.Observations[0]) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	return tool, cycle, proofs
}

func TestWorldToolAccessGainFeedbackCombinesIndependentResourcesWithoutWriting(t *testing.T) {
	tool, cycle, proofs := accessFeedbackToolFixture(t, 2)
	valid := activationArbiterArgs(t, cycle)
	bad := cycle
	bad.Evidence.Arbitrations = append([]domain.WorldArbitrationReceipt(nil), cycle.Evidence.Arbitrations...)
	bad.Evidence.Arbitrations[0].Resolutions = append([]domain.CharacterDecisionResolution(nil), cycle.Evidence.Arbitrations[0].Resolutions...)
	post := *bad.Evidence.Arbitrations[0].Resolutions[0].PostState
	post.Resources = append([]domain.CharacterResourceHoldingV2(nil), post.Resources...)
	for i := range post.Resources {
		post.Resources[i].Access = "exclusive"
	}
	bad.Evidence.Arbitrations[0].Resolutions[0].PostState = &post
	before := arbitrationReferenceFiles(t, tool.store.Dir())
	_, err := tool.Execute(context.Background(), activationArbiterArgs(t, bad))
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("expected unchanged rejection classification: %v", err)
	}
	for _, text := range []string{"res_0000000000000001", "res_0000000000000002", "shared -> exclusive", "do not invent a delivery", "omit access from resource_updates"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("missing same-call access repair feedback %q: %v", text, err)
		}
	}
	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("diagnostic exposed private catalog content")
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, tool.store.Dir())) {
		t.Fatal("rejected arbitration changed stored source or output")
	}
	if _, err := tool.Execute(context.Background(), valid); err != nil {
		t.Fatalf("unchanged valid call rejected: %v", err)
	}
	e := cycle.Evidence
	stored, err := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1)
	if err != nil || stored == nil {
		t.Fatalf("valid receipt absent: %v", err)
	}
	expected := e.Arbitrations[0]
	expected.GeneratedAt = stored.GeneratedAt
	expected, err = domain.FinalizeWorldArbitrationReceipt(expected, e.Stimulus, e.Activation, e.Proposals, 1)
	if err != nil || expected.Digest != stored.Digest {
		t.Fatalf("valid receipt digest changed: %v", err)
	}
	if current, _ := json.Marshal(tool.proposals); string(current) != mustAccessJSON(t, e.Proposals) {
		t.Fatal("feedback mutated proposal")
	}
}

func TestWorldToolAccessGainFeedbackIsBoundedAndDeterministic(t *testing.T) {
	tool, cycle, _ := accessFeedbackToolFixture(t, 10)
	post := *cycle.Evidence.Arbitrations[0].Resolutions[0].PostState
	post.Resources = append([]domain.CharacterResourceHoldingV2(nil), post.Resources...)
	for i := range post.Resources {
		post.Resources[i].Access = "exclusive"
	}
	cycle.Evidence.Arbitrations[0].Resolutions[0].PostState = &post
	before := arbitrationReferenceFiles(t, tool.store.Dir())
	var previous string
	for attempt := 0; attempt < 3; attempt++ {
		for left, right := 0, len(post.Resources)-1; left < right; left, right = left+1, right-1 {
			post.Resources[left], post.Resources[right] = post.Resources[right], post.Resources[left]
		}
		_, err := tool.Execute(context.Background(), activationArbiterArgs(t, cycle))
		if !errors.Is(err, errs.ErrToolPrecondition) {
			t.Fatalf("invalid access admitted: %v", err)
		}
		text := err.Error()
		if strings.Count(text, "access gain lacks an actual delivery") != 8 || !strings.Contains(text, "2 additional resource access errors omitted") {
			t.Fatalf("feedback not bounded: %v", err)
		}
		if !strings.HasPrefix(text, "world arbitration rejected: actor "+tool.proposals[0].AgentID+" resource res_0000000000000001 access gain lacks an actual delivery") {
			t.Fatalf("original first error prefix changed: %v", err)
		}
		if previous != "" && previous != text {
			t.Fatalf("input resource order changed feedback: %s\nvs\n%s", previous, text)
		}
		previous = text
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, tool.store.Dir())) {
		t.Fatal("rejected retries changed immutable evidence")
	}
}

func TestWorldToolAccessGainFeedbackDoesNotReplaceFirstFailureWithDependentKnowledge(t *testing.T) {
	tool, cycle, _ := accessFeedbackToolFixture(t, 2)
	post := *cycle.Evidence.Arbitrations[0].Resolutions[0].PostState
	post.Resources = append([]domain.CharacterResourceHoldingV2(nil), post.Resources...)
	post.Resources[0].Access = "exclusive"
	post.Resources[1].PerceivedName = "PRIVATE unreceived new name"
	cycle.Evidence.Arbitrations[0].Resolutions[0].PostState = &post
	before := arbitrationReferenceFiles(t, tool.store.Dir())
	_, err := tool.Execute(context.Background(), activationArbiterArgs(t, cycle))
	if !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "res_0000000000000001 access gain lacks an actual delivery") {
		t.Fatalf("first access failure replaced: %v", err)
	}
	if strings.Contains(err.Error(), "name lacks") || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("unrelated knowledge validation leaked into access-only repair: %v", err)
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, tool.store.Dir())) {
		t.Fatal("rejected diagnostic wrote files")
	}
}

func mustAccessJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

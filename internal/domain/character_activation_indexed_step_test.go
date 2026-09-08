package domain_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestVerifiedIndexedStepMatchesDetachedHistoryAndRejectsUnverifiedSource(t *testing.T) {
	evidence := testutil.CharacterActivationChapter(t)
	cycle, input := evidence.Cycles[0], evidence.Inputs[0]
	opening := verifiedPrefixForCycle(t, cycle)
	_, prefix, err := domain.VerifyCharacterActivationStep(opening, input, cycle)
	continuationMust(t, err)
	step, exists := prefix.Step(0)
	if !exists || !reflect.DeepEqual(step, prefix.Steps()[0]) {
		t.Fatal("indexed getter differs from previously detached history")
	}
	got := step.Input()
	got.Observations[0].KnownFacts[0].Text = "corrupted caller copy"
	gotCycle := step.Cycle()
	gotCycle.Evidence.Proposals[0].Decision = "corrupted caller copy"
	again, ok := prefix.Step(0)
	if !ok || again.Input().Observations[0].KnownFacts[0].Text == "corrupted caller copy" || again.Cycle().Evidence.Proposals[0].Decision == "corrupted caller copy" || again.GlobalRoot() != cycle.Digest {
		t.Fatal("indexed getter exposes writable verified source")
	}
	for _, index := range []int{-1, 1, 1000} {
		if value, ok := prefix.Step(index); ok || value.GlobalRoot() != "" {
			t.Fatal("out-of-range index granted authority")
		}
	}
	var fake domain.VerifiedCharacterActivationPrefix
	continuationMust(t, json.Unmarshal([]byte(`{"verified":true,"steps":[{"verified":true}]}`), &fake))
	if value, ok := fake.Step(0); ok || value.GlobalRoot() != "" {
		t.Fatal("serialized badge yielded an indexed verified step")
	}
}

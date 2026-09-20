package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

// Compare the complete generated packet, including each owner's private
// history, original continuation proposals, timestamps and all source digests.
// The fixture uses verified fresh/continued cycles, not serialized authority.
func TestNextActivationInputIndexedStepMatchesFullHistoryCopy(t *testing.T) {
	prefix, step := verifiedInputOrigin(t)
	for count := 1; count <= 3; count++ {
		t.Run(fmt.Sprintf("cycles-%d", count), func(t *testing.T) {
			session := prefix.Session()
			all := prefix.Steps() // Original call-site behavior, retained as oracle.
			if len(all) != count || len(session.CycleDigests) != count {
				t.Fatal("fixture did not build the requested complete prefix")
			}
			oldLast := all[len(all)-1]
			want, err := buildNextCharacterActivationInputsFromStep(oldLast.Input(), oldLast, session)
			selectionMust(t, err)
			last, ok := prefix.Step(len(session.CycleDigests) - 1)
			if !ok {
				t.Fatal("indexed lookup lost a verified last step")
			}
			got, err := buildNextCharacterActivationInputsFromStep(last.Input(), last, session)
			selectionMust(t, err)
			wantJSON, err := json.Marshal(want)
			selectionMust(t, err)
			gotJSON, err := json.Marshal(got)
			selectionMust(t, err)
			if !bytes.Equal(wantJSON, gotJSON) {
				t.Fatal("indexed lookup changed the exact next activation input")
			}
		})
		if count < 3 {
			prefix, step = verifiedInputContinuation(t, prefix, step)
		}
	}
}

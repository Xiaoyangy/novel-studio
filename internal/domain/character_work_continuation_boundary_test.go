package domain_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// TEST ONLY global adapter: this fixture's full world has one continuation
// owner (and any supplied fresh peers), so its complete authenticated ledger
// is the entire test-global proof. It is never installed by production code.
func continuationBoundaries(t *testing.T, ledger domain.CharacterWorkContinuationLedgerV1) []domain.VerifiedCharacterWorkContinuationBoundaryV1 {
	t.Helper()
	var out []domain.VerifiedCharacterWorkContinuationBoundaryV1
	for i := range ledger.Entries {
		prefix := continuationCopy(ledger)
		prefix.Entries = prefix.Entries[:i+1]
		var err error
		prefix.Digest, err = domain.ComputeCharacterWorkContinuationLedgerV1Digest(prefix)
		continuationMust(t, err)
		raw, err := json.Marshal(map[string]any{"complete_test_global_ledger": prefix})
		continuationMust(t, err)
		verified, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
			return verifyContinuationTestGlobal(raw, out)
		})
		continuationMust(t, err)
		out = append(out, verified)
	}
	return out
}

func verifyContinuationTestGlobal(raw json.RawMessage, prior []domain.VerifiedCharacterWorkContinuationBoundaryV1) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
	var envelope struct {
		Ledger domain.CharacterWorkContinuationLedgerV1 `json:"complete_test_global_ledger"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return domain.CharacterWorkContinuationGlobalBindingV1{}, err
	}
	l := envelope.Ledger
	if len(l.Entries) == 0 {
		return domain.CharacterWorkContinuationGlobalBindingV1{}, fmt.Errorf("incomplete test global proof")
	}
	if err := domain.ValidateCharacterWorkContinuationLedgerV1(l, prior...); err != nil {
		return domain.CharacterWorkContinuationGlobalBindingV1{}, err
	}
	e := l.Entries[len(l.Entries)-1]
	root, err := domain.ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
	if err != nil {
		return domain.CharacterWorkContinuationGlobalBindingV1{}, err
	}
	return domain.CharacterWorkContinuationGlobalBindingV1{GlobalRoot: root, GenerationID: l.Origin.GenerationID, Chapter: l.Origin.Chapter, Cycle: e.Continuation.Cycle, InputSetDigest: e.Input.Digest, ArbitrationDigest: e.Arbitration.Digest, AfterPhysicalRoot: e.AfterPhysicalRoot, ContinuationEntryDigests: []string{e.EntryDigest}}, nil
}

func TestWorkContinuationRequiresVerifiedGlobalBoundaryNotEntryOrCallerRoot(t *testing.T) {
	l, in := continuationFixture(t, nil, nil)
	a, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	r := continuationArbitration(t, l, in, 1, "in_progress")
	l, err = domain.AppendCharacterWorkContinuationExecutionV1(l, in, *a.Receipt, r, nil)
	continuationMust(t, err)
	in = continuationNextInput(t, l)
	if _, err := domain.EvaluateCharacterWorkContinuationV1(l, in); err == nil {
		t.Fatal("production missing global verifier was silently bypassed")
	}
	if _, err := domain.EvaluateCharacterWorkContinuationV1(l, in, domain.VerifiedCharacterWorkContinuationBoundaryV1{}); err == nil {
		t.Fatal("caller-created verification badge was trusted")
	}
	bound := continuationBoundaries(t, l)
	if bound[0].GlobalRoot() == l.Entries[0].EntryDigest {
		t.Fatal("entry digest was presented as a global cycle root")
	}
	wrongParent := continuationCopy(in)
	wrongParent.Observations[0].CycleContext.PreviousCycleDigest = l.Entries[0].EntryDigest
	token, err := domain.CharacterActivationCycleSourceToken(in.Stimulus.GenerationID, 1, wrongParent.Observations[0].CycleContext.Index, l.Origin.ChapterContextDigest, l.Entries[0].EntryDigest)
	continuationMust(t, err)
	for i, source := range wrongParent.Stimulus.Sources {
		if strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
			wrongParent.Stimulus.Sources[i] = token
		}
	}
	wrongParent = continuationRefinalizeInput(t, wrongParent)
	if _, err := domain.EvaluateCharacterWorkContinuationV1(l, wrongParent, bound...); err == nil {
		t.Fatal("entry root replaced verified global parent")
	}
	result, err := domain.EvaluateCharacterWorkContinuationV1(l, in, bound...)
	continuationMust(t, err)
	if !result.Eligible {
		t.Fatal("complete global membership failed")
	}
	raw, err := json.Marshal(map[string]any{"complete_test_global_ledger": l})
	continuationMust(t, err)
	if _, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, nil); err == nil {
		t.Fatal("nil global verifier granted authority")
	}
	_, err = domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
		b, e := verifyContinuationTestGlobal(raw, nil)
		b.GlobalRoot = "sha256:" + strings.Repeat("f", 64)
		return b, e
	})
	if err == nil {
		t.Fatal("verifier root not derived from full canonical proof accepted")
	}
	_, err = domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
		b, e := verifyContinuationTestGlobal(raw, nil)
		b.AfterPhysicalState = l.Origin.Evidence.Stimulus.PhysicalState
		return b, e
	})
	if err == nil {
		t.Fatal("mixed-origin boundary accepted physical state different from its after root")
	}
	for _, field := range []string{"membership", "input", "arbitration", "after"} {
		t.Run(field, func(t *testing.T) {
			bad, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
				b, e := verifyContinuationTestGlobal(raw, nil)
				foreign := "sha256:" + strings.Repeat("e", 64)
				switch field {
				case "membership":
					b.ContinuationEntryDigests = []string{foreign}
				case "input":
					b.InputSetDigest = foreign
				case "arbitration":
					b.ArbitrationDigest = foreign
				case "after":
					b.AfterPhysicalRoot = foreign
				}
				return b, e
			})
			continuationMust(t, err)
			if _, err := domain.EvaluateCharacterWorkContinuationV1(l, in, bad); err == nil {
				t.Fatal("foreign global ownership roots accepted")
			}
		})
	}
}

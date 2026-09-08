package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// Returned by a HOST-SELECTED complete global-cycle verifier, never by a model.
type CharacterWorkContinuationGlobalBindingV1 struct {
	GlobalRoot                                           string
	GenerationID                                         string
	Chapter, Cycle                                       int
	InputSetDigest, ArbitrationDigest, AfterPhysicalRoot string
	ContinuationEntryDigests                             []string
	AfterPhysicalState                                   *WorldPhysicalStateV2 `json:"-"`
}

type CharacterWorkContinuationGlobalVerifierV1 func(json.RawMessage) (CharacterWorkContinuationGlobalBindingV1, error)

// Zero values and JSON cannot construct verified authority. Rehydration must
// rerun the real global verifier. This MVP installs NO production verifier.
type VerifiedCharacterWorkContinuationBoundaryV1 struct {
	binding  CharacterWorkContinuationGlobalBindingV1
	verified bool
}

func (b VerifiedCharacterWorkContinuationBoundaryV1) GlobalRoot() string {
	if !b.verified {
		return ""
	}
	return b.binding.GlobalRoot
}

// raw is the COMPLETE global verifier's digest payload, not a partial summary
// or self-referential digest envelope. Object key order/whitespace normalize;
// numbers retain exact precision. The trusted verifier checks full semantics.
func ComputeCharacterWorkContinuationGlobalProofRootV1(raw json.RawMessage) (string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value map[string]any
	if err := d.Decode(&value); err != nil || len(value) == 0 {
		return "", fmt.Errorf("global continuation proof must be a complete JSON object")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("global continuation proof has trailing data")
	}
	return characterAgentDigest(value)
}

func VerifyCharacterWorkContinuationBoundaryV1(raw json.RawMessage, verifier CharacterWorkContinuationGlobalVerifierV1) (VerifiedCharacterWorkContinuationBoundaryV1, error) {
	var out VerifiedCharacterWorkContinuationBoundaryV1
	if verifier == nil {
		return out, fmt.Errorf("complete global cycle verifier is not installed")
	}
	root, err := ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
	if err != nil {
		return out, err
	}
	bound, err := verifier(append(json.RawMessage(nil), raw...))
	if err != nil {
		return out, err
	}
	if bound.GlobalRoot != root || bound.GenerationID == "" || bound.Chapter <= 0 || bound.Cycle <= 0 || bound.Cycle > 64 || len(bound.ContinuationEntryDigests) == 0 {
		return out, fmt.Errorf("global verifier did not bind the complete proof identity/membership")
	}
	for _, digest := range []string{bound.GlobalRoot, bound.InputSetDigest, bound.ArbitrationDigest, bound.AfterPhysicalRoot} {
		if err := validatePlanningV2Digest("global continuation binding", digest); err != nil {
			return out, err
		}
	}
	seen := map[string]bool{}
	for _, digest := range bound.ContinuationEntryDigests {
		if err := validatePlanningV2Digest("continuation member", digest); err != nil {
			return out, err
		}
		if seen[digest] {
			return out, fmt.Errorf("global continuation member duplicated")
		}
		seen[digest] = true
	}
	if bound.AfterPhysicalState != nil {
		physicalRoot, err := CharacterPhysicalRootForCycle(*bound.AfterPhysicalState)
		if err != nil || physicalRoot != bound.AfterPhysicalRoot {
			return out, fmt.Errorf("global continuation after-state does not match its verified physical root")
		}
	}
	out.binding, out.verified = continuationCloneV1(bound), true
	if bound.AfterPhysicalState != nil {
		copy := continuationCloneV1(*bound.AfterPhysicalState)
		out.binding.AfterPhysicalState = &copy
	}
	return out, nil
}

func verifiedContinuationGlobalRootV1(entry CharacterWorkContinuationExecutionV1, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (string, error) {
	root := ""
	for _, verified := range boundaries {
		b := verified.binding
		if !verified.verified || !slices.Contains(b.ContinuationEntryDigests, entry.EntryDigest) {
			continue
		}
		if b.GenerationID != entry.Continuation.GenerationID || b.Chapter != entry.Continuation.Chapter || b.Cycle != entry.Continuation.Cycle || b.InputSetDigest != entry.Input.Digest || b.ArbitrationDigest != entry.Arbitration.Digest || b.AfterPhysicalRoot != entry.AfterPhysicalRoot {
			return "", fmt.Errorf("global continuation membership has foreign input/arbitration/physical roots")
		}
		if root != "" && root != b.GlobalRoot {
			return "", fmt.Errorf("ambiguous global commit for continuation entry")
		}
		root = b.GlobalRoot
	}
	if root == "" {
		return "", fmt.Errorf("continuation entry lacks a verified complete global commit boundary")
	}
	return root, nil
}

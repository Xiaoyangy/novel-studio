package domain_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestContinuationSameCallReuseMatchesIndependentBoundaryAndOriginalBytes(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	cycleBytes, _ := json.Marshal(cycle)
	inputBytes, _ := json.Marshal(input)
	payload := continuationCopy(cycle)
	payload.Digest = ""
	raw, err := json.Marshal(payload)
	continuationMust(t, err)
	// Independent public verification still fully checks externally supplied
	// JSON; the optimized step's same-call reuse must yield the same boundary.
	independent, err := domain.VerifyCharacterContinuationCycleBoundaryV2(prefix, input, raw)
	continuationMust(t, err)
	step, next, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	continuationMust(t, err)
	if independent.GlobalRoot() != step.GlobalRoot() || independent.GlobalRoot() != next.ContinuationBoundaries()[len(next.ContinuationBoundaries())-1].GlobalRoot() {
		t.Fatal("same-call reuse changed the canonical boundary")
	}
	got, _ := json.Marshal(step.Cycle())
	unchanged, _ := json.Marshal(cycle)
	afterInput, _ := json.Marshal(input)
	if string(got) != string(cycleBytes) || string(unchanged) != string(cycleBytes) || string(afterInput) != string(inputBytes) {
		t.Fatal("reuse changed original cycle, receipt, proposal or input bytes")
	}
	if next.Session().CurrentPhysicalRoot != cycle.AfterPhysicalRoot {
		t.Fatal("reused physical binding differs from actual result")
	}
	for _, member := range cycle.WorkContinuations {
		ledger, ok := next.ContinuationLedger(member.AgentID)
		if !ok {
			t.Fatal("lost owner ledger")
		}
		continuationMust(t, domain.ValidateCharacterWorkContinuationLedgerV1(ledger, next.ContinuationBoundaries()...))
	}
}

func TestContinuationSameCallReuseRejectsWrongBytesAndForgedSourceAuthority(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	for _, kind := range []string{"raw_cycle", "resigned_foreign_source", "wrong_input", "wrong_membership"} {
		t.Run(kind, func(t *testing.T) {
			bad := continuationCopy(cycle)
			supplied := continuationCopy(input)
			switch kind {
			case "raw_cycle":
				bad.Evidence.Arbitrations[0].Resolutions[0].StateAfter += " altered while retaining original digest"
			case "resigned_foreign_source":
				bad.WorkContinuations[0].OriginProposalDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				bad.WorkContinuations[0].Digest, err = domain.ComputeCharacterWorkContinuationReceiptV1Digest(bad.WorkContinuations[0])
				continuationMust(t, err)
				bad.Digest, err = domain.CharacterContinuationCycleRootV2(bad)
				continuationMust(t, err)
			case "wrong_input":
				supplied.Digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			case "wrong_membership":
				bad.ContinuationEntryDigests = bad.ContinuationEntryDigests[:1]
				bad.Digest, err = domain.CharacterContinuationCycleRootV2(bad)
				continuationMust(t, err)
			}
			if _, _, err := domain.VerifyCharacterActivationStep(prefix, supplied, bad); err == nil {
				t.Fatal("same-call path accepted forged incoming bytes/source")
			}
			bad.Digest = ""
			raw, _ := json.Marshal(bad)
			if _, err := domain.VerifyCharacterContinuationCycleBoundaryV2(prefix, supplied, raw); err == nil {
				t.Fatal("public external verifier lost full source validation")
			}
		})
	}
	var forged domain.VerifiedCharacterActivationPrefix
	continuationMust(t, json.Unmarshal([]byte(`{"verified":true,"session":{"digest":"claimed"}}`), &forged))
	if _, _, err := domain.VerifyCharacterActivationStep(forged, input, cycle); err == nil {
		t.Fatal("JSON minted verified source authority")
	}
}

func TestContinuationSameCallReuseIsDetachedAcrossConcurrentInvocations(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	original, _ := json.Marshal(cycle)
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			step, _, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
			if err != nil {
				failures <- err
				return
			}
			own := step.Cycle()
			own.Evidence.Arbitrations[0].Resolutions[0].StateAfter = "caller mutation"
			if step.Cycle().Digest != cycle.Digest || step.Cycle().Evidence.Arbitrations[0].Resolutions[0].StateAfter == "caller mutation" {
				failures <- fmt.Errorf("returned value aliases reused local authority")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		continuationMust(t, err)
	}
	after, _ := json.Marshal(cycle)
	if string(after) != string(original) {
		t.Fatal("concurrent reuse mutated source bytes")
	}
}

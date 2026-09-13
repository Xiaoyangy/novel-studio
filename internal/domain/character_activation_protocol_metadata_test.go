package domain_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func metadataV3Prefix(t *testing.T, cycles int) domain.VerifiedCharacterActivationPrefix {
	t.Helper()
	context, prefix, input := v3Fixture(t)
	for index := 0; index < cycles; index++ {
		cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 0))
		continuationMust(t, err)
		_, prefix, err = domain.VerifyCharacterActivationStep(prefix, input, cycle)
		continuationMust(t, err)
		if index+1 < cycles {
			prefix, _ = v3Assess(t, context, prefix, "continue")
			input = v3NextInput(t, prefix)
		}
	}
	return prefix
}

// The exact old admission algorithm is the equivalence/benchmark reference.
func clonedProtocolCheck(prefix domain.VerifiedCharacterActivationPrefix, version, protocol string) error {
	for _, step := range prefix.Steps() {
		if step.Cycle().Version != version || step.Cycle().Evidence.ProtocolDigest != protocol {
			return fmt.Errorf("mismatching old admission metadata")
		}
	}
	return nil
}

func TestVerifiedPrefixCycleProtocolRejectsZeroAndJSONForgedAuthority(t *testing.T) {
	protocol := "sha256:" + strings.Repeat("e", 64)
	for _, raw := range []string{`{}`, `{"verified":true}`, `{"verified":true,"steps":[{"verified":true,"cycle":{"version":"character-activation-cycle.v3","evidence":{"protocol_digest":"` + protocol + `"}}}]}`} {
		var prefix domain.VerifiedCharacterActivationPrefix
		continuationMust(t, json.Unmarshal([]byte(raw), &prefix))
		if prefix.ValidateCycleProtocol(domain.CharacterActivationCycleV3Version, protocol) == nil {
			t.Fatal("serialized metadata manufactured verified source authority")
		}
	}
	empty := metadataV3Prefix(t, 0)
	if err := empty.ValidateCycleProtocol(domain.CharacterActivationCycleV3Version, protocol); err != nil {
		t.Fatalf("actual verified empty prefix cannot admit its first cycle: %v", err)
	}
	raw, err := json.Marshal(empty)
	continuationMust(t, err)
	if string(raw) != "{}" {
		t.Fatal("metadata API added JSON-visible authority")
	}
}

func TestVerifiedPrefixCycleProtocolMatchesClonedCheckAndHasNoMutableAliases(t *testing.T) {
	prefix := metadataV3Prefix(t, 2)
	version, protocol := domain.CharacterActivationCycleV3Version, "sha256:"+strings.Repeat("e", 64)
	before, _ := json.Marshal(struct {
		Session domain.CharacterActivationSession
		Steps   []domain.CharacterActivationCycle
	}{prefix.Session(), []domain.CharacterActivationCycle{prefix.Steps()[0].Cycle(), prefix.Steps()[1].Cycle()}})
	for _, expected := range [][2]string{{version, protocol}, {domain.CharacterActivationCycleVersion, protocol}, {domain.CharacterActivationCycleV2Version, protocol}, {version, "sha256:" + strings.Repeat("f", 64)}, {"", protocol}, {version, ""}} {
		old, new := clonedProtocolCheck(prefix, expected[0], expected[1]), prefix.ValidateCycleProtocol(expected[0], expected[1])
		if (old == nil) != (new == nil) {
			t.Fatal("scalar validation differs from original cloned admission")
		}
	}
	// Every public path still yields detached values; the new API returns no
	// metadata container that could alias or overwrite the verified history.
	steps := prefix.Steps()
	copyCycle := steps[1].Cycle()
	copyCycle.Version = "corrupted"
	copyCycle.Evidence.ProtocolDigest = "sha256:" + strings.Repeat("f", 64)
	copyCycle.Evidence.Proposals[0].Decision = "caller mutation"
	steps[0] = domain.VerifiedCharacterActivationStep{}
	session := prefix.Session()
	session.CycleDigests[0] = "caller mutation"
	if err := prefix.ValidateCycleProtocol(version, protocol); err != nil {
		t.Fatal("detached caller mutation changed scalar validation")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 50; j++ {
				if prefix.ValidateCycleProtocol(version, protocol) != nil {
					t.Error("concurrent immutable scalar read failed")
				}
			}
		}()
	}
	workers.Wait()
	after, _ := json.Marshal(struct {
		Session domain.CharacterActivationSession
		Steps   []domain.CharacterActivationCycle
	}{prefix.Session(), []domain.CharacterActivationCycle{prefix.Steps()[0].Cycle(), prefix.Steps()[1].Cycle()}})
	if !bytes.Equal(before, after) {
		t.Fatal("metadata validation modified canonical source bytes/digests")
	}
}

func TestVerifiedPrefixCycleProtocolRejectsMixedVersionsAndProtocolChange(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	_, mixed, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	continuationMust(t, err)
	if origin.Version == cycle.Version {
		t.Fatal("fixture is not a real mixed-version prefix")
	}
	for _, version := range []string{origin.Version, cycle.Version, domain.CharacterActivationCycleV3Version} {
		if clonedProtocolCheck(mixed, version, origin.Evidence.ProtocolDigest) == nil || mixed.ValidateCycleProtocol(version, origin.Evidence.ProtocolDigest) == nil {
			t.Fatal("a latest/first-only metadata check accepted mixed cycle versions")
		}
	}
	// Mixed protocols cannot become verified source authority in the first
	// place. Preserve that real constructor rejection, not a fake verified bit.
	draft.Evidence.ProtocolDigest = "sha256:" + strings.Repeat("f", 64)
	if _, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft); err == nil {
		t.Fatal("protocol drift became a verified continuation")
	}
	if err := prefix.ValidateCycleProtocol(origin.Version, origin.Evidence.ProtocolDigest); err != nil {
		t.Fatal("rejected protocol drift mutated the original prefix")
	}
	_, v3, input3 := v3Fixture(t)
	first, err := domain.FinalizeCharacterActivationCycleV3(v3, input3, v3Draft(t, v3, input3, nil, 0))
	continuationMust(t, err)
	first.Evidence.ProtocolDigest = "sha256:" + strings.Repeat("f", 64) // No re-signing: metadata alone is not authority.
	if _, _, err := domain.VerifyCharacterActivationStep(v3, input3, first); err == nil {
		t.Fatal("new scalar API bypassed actual cycle digest/source validation")
	}
}

// Explicit opt-in keeps dynamic benchmark calibration out of ordinary tests
// and race CI. Use -benchtime=3x for a small fixed-count comparison.
func TestVerifiedPrefixCycleProtocolWorkComparison(t *testing.T) {
	if os.Getenv("NOVEL_STUDIO_PREFIX_PROTOCOL_BENCHMARK") != "1" {
		t.Skip("opt-in metadata-only work comparison")
	}
	prefix := metadataV3Prefix(t, 3)
	version, protocol := domain.CharacterActivationCycleV3Version, "sha256:"+strings.Repeat("e", 64)
	old := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := clonedProtocolCheck(prefix, version, protocol); err != nil {
				b.Fatal(err)
			}
		}
	})
	current := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := prefix.ValidateCycleProtocol(version, protocol); err != nil {
				b.Fatal(err)
			}
		}
	})
	t.Logf("old cloned metadata: %s %s", old.String(), old.MemString())
	t.Logf("immutable scalar check: %s %s", current.String(), current.MemString())
	if current.AllocsPerOp() != 0 {
		t.Fatal("scalar metadata success path unexpectedly allocates")
	}
}

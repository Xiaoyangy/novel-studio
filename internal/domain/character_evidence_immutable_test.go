package domain

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

func TestCharacterEvidenceFinalizationDoesNotMutateSharedSource(t *testing.T) {
	evidence := characterSourceEvidenceFixtureForTest(t, false)
	for left, right := 0, len(evidence.Registry.Entries)-1; left < right; left, right = left+1, right-1 {
		evidence.Registry.Entries[left], evidence.Registry.Entries[right] = evidence.Registry.Entries[right], evidence.Registry.Entries[left]
	}
	for left, right := 0, len(evidence.Observations)-1; left < right; left, right = left+1, right-1 {
		evidence.Observations[left], evidence.Observations[right] = evidence.Observations[right], evidence.Observations[left]
	}
	before, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := FinalizeCharacterAgentEvidenceBundle(evidence); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	after, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("validation mutated immutable caller-owned evidence")
	}
}

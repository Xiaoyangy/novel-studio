package store

import (
	"strings"
	"testing"
)

func TestArcWindowAggregateUniqueLookupIsReadOnlyAndRejectsAmbiguity(t *testing.T) {
	st, ids := arcWindowAggregateFixture(t)
	if receipt, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1(ids[1]); err != nil || receipt != nil {
		t.Fatalf("missing aggregate was not a read-only absence: %+v %v", receipt, err)
	}
	receipt, err := st.CompleteWindowedArcV1(ids, projectedStoreV2Time())
	if err != nil {
		t.Fatal(err)
	}
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(st.Dir()).LoadVerifiedWindowedArcCompletionForGenerationV1(ids[1])
	if err != nil || loaded == nil || loaded.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("unique original aggregate did not reload: %+v %v", loaded, err)
	}
	after, err := DirectoryContentRoot(st.Dir())
	if err != nil || after != before {
		t.Fatalf("aggregate lookup mutated stored sources: %v", err)
	}
	if _, err := st.CompleteWindowedArcV1(ids, "2026-07-18T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1(ids[1]); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("lookup chose a newer receipt instead of rejecting ambiguity: %v", err)
	}
	if _, err := st.LoadVerifiedWindowedArcCompletionForGenerationV1("../foreign"); err == nil {
		t.Fatal("unsafe recovery selector was accepted")
	}
}

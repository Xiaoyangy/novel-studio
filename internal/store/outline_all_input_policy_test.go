package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestOutlineAllInputPolicyRoundTripAndImmutability(t *testing.T) {
	for _, policy := range []string{"", domain.OutlineAllInputPolicyCompleteFoundationV1} {
		t.Run(policy, func(t *testing.T) {
			st := NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			receipt := storeOutlineAllReceiptForTest(t)
			receipt.InputPolicy = policy
			receipt, err := domain.SignOutlineAllExecutionReceipt(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
				t.Fatal(err)
			}
			loaded, err := st.LoadOutlineAllExecutionReceipt()
			if err != nil || loaded.InputPolicy != policy {
				t.Fatalf("roundtrip: %+v %v", loaded, err)
			}
			path := filepath.Join(st.Dir(), OutlineAllExecutionReceiptPath)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(before, []byte(`"input_policy"`)) != (policy != "") {
				t.Fatal("legacy empty input policy changed the wire")
			}
			opposite := domain.OutlineAllInputPolicyCompleteFoundationV1
			if policy != "" {
				opposite = ""
			}
			if _, err := st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error { current.InputPolicy = opposite; return nil }); err == nil {
				t.Fatal("CAS changed the frozen input policy")
			}
			changed := receipt
			changed.InputPolicy = opposite
			changed, err = domain.SignOutlineAllExecutionReceipt(changed)
			if err != nil {
				t.Fatal(err)
			}
			if changed.ReceiptDigest == receipt.ReceiptDigest {
				t.Fatal("input policy was not digest-bound")
			}
			if err := st.SaveOutlineAllExecutionReceipt(changed); err == nil {
				t.Fatal("Save overwrote an existing attempt's frozen input policy")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected policy mutation wrote receipt bytes")
			}
		})
	}
}

func TestOutlineAllInputPolicyRejectsCorrectlyHashedUnknownValue(t *testing.T) {
	receipt := storeOutlineAllReceiptForTest(t)
	receipt.InputPolicy = "unknown-input-policy"
	digest, err := domain.ComputeOutlineAllExecutionReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptDigest = digest
	if err := domain.ValidateOutlineAllExecutionReceipt(receipt); err == nil {
		t.Fatal("unknown correctly-hashed policy accepted")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip domain.OutlineAllExecutionReceipt
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateOutlineAllExecutionReceipt(roundtrip); err == nil {
		t.Fatal("unknown policy survived wire validation")
	}
}

package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOutlineAllContractPolicyLegacyWireAndDigestCompatibility(t *testing.T) {
	receipt := validOutlineAllReceiptForTest(t)
	now := time.Date(2026, 9, 20, 1, 2, 3, 456789000, time.UTC)
	receipt.LockAcquiredAt, receipt.LockExpiresAt = now, now.Add(time.Hour)
	receipt.StartedAt, receipt.UpdatedAt = now, now
	receipt, err := SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "contract_evidence_policy") {
		t.Fatal("legacy empty policy introduced a new wire field")
	}
	// Captured by running this fixed fixture against the actual 5faf5a9
	// outline_all_execution.go via go test -overlay, before the new field.
	const oldDigest = "sha256:5aaeb92f94adfea8c9cff92442bf0c599d906a8beb32651625c9bea60e23c43f"
	const oldWireSHA = "dfecd0cb3b5609a03d2d793ae040299e5d1fb24a69a4360693cf3a9c1aa558b6"
	if receipt.ReceiptDigest != oldDigest || fmt.Sprintf("%x", sha256.Sum256(raw)) != oldWireSHA {
		t.Fatalf("legacy wire/digest changed: digest=%s wire_sha=%x", receipt.ReceiptDigest, sha256.Sum256(raw))
	}
}

func TestOutlineAllContractPolicyReceiptRoundTripsAndBindsItsDigest(t *testing.T) {
	legacy := validOutlineAllReceiptForTest(t)
	receipt := legacy
	receipt.ContractEvidencePolicy = StoryContractEvidencePolicyNarrationV1
	receipt, err := SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ReceiptDigest == legacy.ReceiptDigest {
		t.Fatal("new contract evidence policy is absent from the receipt digest")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OutlineAllExecutionReceipt
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ContractEvidencePolicy != StoryContractEvidencePolicyNarrationV1 || ValidateOutlineAllExecutionReceipt(decoded) != nil {
		t.Fatal("new contract policy did not survive signed wire round trip")
	}
	decoded.ContractEvidencePolicy = ""
	if err := ValidateOutlineAllExecutionReceipt(decoded); err == nil {
		t.Fatal("removing the policy did not invalidate its original digest")
	}
}

func TestOutlineAllContractPolicyReceiptRejectsCorrectlyHashedUnknownPolicies(t *testing.T) {
	for _, policy := range []string{"future-policy", " ", StoryContractEvidencePolicyNarrationV1 + " "} {
		t.Run(policy, func(t *testing.T) {
			receipt := validOutlineAllReceiptForTest(t)
			receipt.ContractEvidencePolicy = policy
			var err error
			receipt.ReceiptDigest, err = ComputeOutlineAllExecutionReceiptDigest(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateOutlineAllExecutionReceipt(receipt); err == nil {
				t.Fatal("correctly hashed unknown policy was accepted")
			}
			if _, err := SignOutlineAllExecutionReceipt(receipt); err == nil {
				t.Fatal("signing authorized an unknown policy")
			}
		})
	}
}

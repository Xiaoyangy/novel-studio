package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func outlineAllAuthorReceipt(t *testing.T, withReferences bool) OutlineAllExecutionReceipt {
	t.Helper()
	receipt := validOutlineAllReceiptForTest(t)
	catalog := authorSourcesDomainFixture(t)
	compass := StoryCompass{EndingDirection: receipt.EndingDirection, EstimatedScale: receipt.EstimatedScale, AuthorContracts: &CompassAuthorContractsV1{Policy: catalog.Policy, SourcesDigest: catalog.Digest}}
	if withReferences {
		compass.AuthorContracts.Refs = []AuthorSourceParagraphRefV1{{SourceID: catalog.Sources[0].ID, Paragraph: 0}}
	}
	var err error
	compass, err = MaterializeCompassAuthorContractsV1(compass, catalog)
	if err != nil {
		t.Fatal(err)
	}
	receipt.AuthorContracts, receipt.NonNegotiables = compass.AuthorContracts, compass.NonNegotiables
	receipt.CompassDigest, err = ComputeStoryCompassDigest(compass)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestOutlineAllAuthorContractsReceiptPreservesEmptyAndNonemptyMode(t *testing.T) {
	for _, hasRefs := range []bool{false, true} {
		receipt := outlineAllAuthorReceipt(t, hasRefs)
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var loaded OutlineAllExecutionReceipt
		if err := json.Unmarshal(raw, &loaded); err != nil || !reflect.DeepEqual(loaded, receipt) {
			t.Fatalf("receipt dropped author mode/binding: %v", err)
		}
		if err := ValidateOutlineAllExecutionReceipt(loaded); err != nil {
			t.Fatal(err)
		}
		compass := StoryCompass{EndingDirection: loaded.EndingDirection, NonNegotiables: loaded.NonNegotiables, AuthorContracts: loaded.AuthorContracts}
		if got := CompassHardContractsV1(compass); !reflect.DeepEqual(got, loaded.NonNegotiables) {
			t.Fatal("receipt reconstruction promoted soft ending to hard")
		}
		registry := BuildStoryContractRegistry(compass)
		if len(registry) != len(receipt.NonNegotiables) {
			t.Fatal("receipt reconstruction changed author contract registry coverage")
		}
		for _, ref := range registry {
			if ref.Kind != StoryContractNonNegotiable {
				t.Fatal("new-mode receipt restored legacy ending contract")
			}
		}
	}
}

func TestOutlineAllAuthorContractsReceiptRejectsInvalidMarkersAndBindingDrift(t *testing.T) {
	for _, mode := range []string{"policy", "digest_shape", "duplicate_ref", "missing_nonneg", "extra_nonneg", "invalid_index", "unknown_source_key", "unsigned_binding_change"} {
		t.Run(mode, func(t *testing.T) {
			receipt := outlineAllAuthorReceipt(t, true)
			switch mode {
			case "policy":
				receipt.AuthorContracts.Policy = "model-contracts"
			case "digest_shape":
				receipt.AuthorContracts.SourcesDigest = "invented"
			case "duplicate_ref":
				receipt.AuthorContracts.Refs = append(receipt.AuthorContracts.Refs, receipt.AuthorContracts.Refs[0])
				receipt.NonNegotiables = append(receipt.NonNegotiables, receipt.NonNegotiables[0])
			case "missing_nonneg":
				receipt.NonNegotiables = nil
			case "extra_nonneg":
				receipt.NonNegotiables = append(receipt.NonNegotiables, "model-invented extra constraint")
			case "invalid_index":
				receipt.AuthorContracts.Refs[0].Paragraph = -1
			case "unknown_source_key":
				receipt.AuthorContracts.Refs[0].SourceID = "\x00source"
			case "unsigned_binding_change":
				receipt.AuthorContracts.SourcesDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if mode == "unsigned_binding_change" {
				if err := ValidateOutlineAllExecutionReceipt(receipt); err == nil {
					t.Fatal("old receipt digest authenticated changed author source binding")
				}
			} else if _, err := SignOutlineAllExecutionReceipt(receipt); err == nil {
				t.Fatalf("invalid %s author binding was signable", mode)
			}
		})
	}
}

func TestOutlineAllAuthorContractsLegacyNilWireAndMissingHardContractStayUnchanged(t *testing.T) {
	legacy := validOutlineAllReceiptForTest(t)
	before, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(`"author_contracts"`)) {
		t.Fatal("new optional mode changed legacy wire fields")
	}
	signed, err := SignOutlineAllExecutionReceipt(legacy)
	if err != nil || signed.ReceiptDigest != legacy.ReceiptDigest {
		t.Fatalf("empty marker changed old receipt digest: %v", err)
	}
	after, err := json.Marshal(signed)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy re-sign changed original bytes: %v", err)
	}
	legacy.NonNegotiables = nil
	if _, err := SignOutlineAllExecutionReceipt(legacy); err == nil {
		t.Fatal("new empty-source mode weakened the old required hard contract")
	}
}

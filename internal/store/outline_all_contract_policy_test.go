package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestOutlineAllContractEvidencePolicyCannotChangeDuringOrdinaryPersistence(t *testing.T) {
	for _, operation := range []string{"update", "save"} {
		for _, oldPolicy := range []string{"", domain.StoryContractEvidencePolicyNarrationV1} {
			name := operation + "/legacy-to-new"
			newPolicy := domain.StoryContractEvidencePolicyNarrationV1
			if oldPolicy != "" {
				name, newPolicy = operation+"/new-to-legacy", ""
			}
			t.Run(name, func(t *testing.T) {
				st := NewStore(t.TempDir())
				if err := st.Init(); err != nil {
					t.Fatal(err)
				}
				original := storeOutlineAllReceiptForTest(t)
				original.ContractEvidencePolicy = oldPolicy
				original, err := domain.SignOutlineAllExecutionReceipt(original)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.SaveOutlineAllExecutionReceipt(original); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(st.Dir(), OutlineAllExecutionReceiptPath)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				beforeRoot, err := DirectoryContentRoot(st.Dir())
				if err != nil {
					t.Fatal(err)
				}
				if operation == "update" {
					_, err = st.UpdateOutlineAllExecutionReceipt(original.ReceiptDigest, func(receipt *domain.OutlineAllExecutionReceipt) error {
						receipt.ContractEvidencePolicy = newPolicy
						return nil
					})
				} else {
					changed := original
					changed.ContractEvidencePolicy = newPolicy
					changed, err = domain.SignOutlineAllExecutionReceipt(changed)
					if err != nil {
						t.Fatal(err)
					}
					err = st.SaveOutlineAllExecutionReceipt(changed)
				}
				if err == nil {
					t.Fatalf("ordinary %s re-signed contract evidence policy from %q to %q", operation, oldPolicy, newPolicy)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatalf("rejected policy change modified the persisted receipt: %v", readErr)
				}
				if afterRoot, err := DirectoryContentRoot(st.Dir()); err != nil || beforeRoot != afterRoot {
					t.Fatalf("rejected policy change wrote another store artifact: %v", err)
				}
			})
		}
	}
}

func TestOutlineAllContractEvidencePolicySurvivesOrdinaryCheckpointUpdates(t *testing.T) {
	for _, policy := range []string{"", domain.StoryContractEvidencePolicyNarrationV1} {
		t.Run(policy, func(t *testing.T) {
			st := NewStore(t.TempDir())
			original := storeOutlineAllReceiptForTest(t)
			original.ContractEvidencePolicy = policy
			original, err := domain.SignOutlineAllExecutionReceipt(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveOutlineAllExecutionReceipt(original); err != nil {
				t.Fatal(err)
			}
			updated, err := st.UpdateOutlineAllExecutionReceipt(original.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
				current.PendingAction = nil
				current.CompletedActionCount++
				current.UpdatedAt = current.UpdatedAt.Add(time.Second)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if updated.ContractEvidencePolicy != policy || updated.ReceiptDigest == original.ReceiptDigest {
				t.Fatal("ordinary checkpoint changed policy or failed to re-sign")
			}
			if err := st.SaveOutlineAllExecutionReceipt(*updated); err != nil {
				t.Fatal(err)
			}
			loaded, err := NewStore(st.Dir()).LoadOutlineAllExecutionReceipt()
			if err != nil || loaded == nil || loaded.ContractEvidencePolicy != policy || loaded.ReceiptDigest != updated.ReceiptDigest {
				t.Fatalf("policy did not survive store reload: %+v %v", loaded, err)
			}
		})
	}
}

func TestOutlineAllContractEvidencePolicyUnknownSaveAndUpdateAreZeroWrite(t *testing.T) {
	st := NewStore(t.TempDir())
	original := storeOutlineAllReceiptForTest(t)
	if err := st.SaveOutlineAllExecutionReceipt(original); err != nil {
		t.Fatal(err)
	}
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.ContractEvidencePolicy = "future-policy"
	changed.ReceiptDigest, err = domain.ComputeOutlineAllExecutionReceiptDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(changed); err == nil {
		t.Fatal("Save accepted a correctly hashed unknown policy")
	}
	if _, err := st.UpdateOutlineAllExecutionReceipt(original.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.ContractEvidencePolicy = changed.ContractEvidencePolicy
		return nil
	}); err == nil {
		t.Fatal("Update accepted an unknown policy")
	}
	if after, err := DirectoryContentRoot(st.Dir()); err != nil || before != after {
		t.Fatalf("rejected unknown policy changed persisted state: %v", err)
	}
}

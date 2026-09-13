package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func outlineAllAuthorStoreReceipt(t *testing.T, withRefs bool) (*Store, domain.OutlineAllExecutionReceipt) {
	t.Helper()
	st, catalog := authorSourcesStoreFixture(t)
	receipt := storeOutlineAllReceiptForTest(t)
	compass := authorSourcesStoreCompass(catalog)
	if withRefs {
		compass = authorSourcesStoreCompass(catalog, 0)
	}
	compass.EstimatedScale = receipt.EstimatedScale
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	actual, err := st.Outline.LoadCompass()
	if err != nil {
		t.Fatal(err)
	}
	receipt.EndingDirection, receipt.NonNegotiables, receipt.AuthorContracts = actual.EndingDirection, actual.NonNegotiables, actual.AuthorContracts
	receipt.CompassDigest, err = domain.ComputeStoryCompassDigest(*actual)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return st, receipt
}

func TestOutlineAllAuthorContractsStoreRoundTripAndUpdatePreserveMode(t *testing.T) {
	for _, withRefs := range []bool{false, true} {
		st, receipt := outlineAllAuthorStoreReceipt(t, withRefs)
		if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
			t.Fatal(err)
		}
		loaded, err := NewStore(st.Dir()).LoadOutlineAllExecutionReceipt()
		if err != nil || loaded == nil || !reflect.DeepEqual(receipt, *loaded) {
			t.Fatalf("receipt lost actual author source mode: %+v %v", loaded, err)
		}
		updated, err := st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error { current.CompletedActionCount++; return nil })
		if err != nil || !reflect.DeepEqual(updated.AuthorContracts, receipt.AuthorContracts) {
			t.Fatalf("CAS checkpoint dropped author mode: %+v %v", updated, err)
		}
	}
}

func TestOutlineAllAuthorContractsStoreRejectsResignedSourceModeDriftWithZeroWrites(t *testing.T) {
	for _, mode := range []string{"catalog_digest", "paragraph", "nonneg_text", "nil_mode", "compass_digest", "missing_catalog"} {
		t.Run(mode, func(t *testing.T) {
			st, original := outlineAllAuthorStoreReceipt(t, true)
			if err := st.SaveOutlineAllExecutionReceipt(original); err != nil {
				t.Fatal(err)
			}
			changed := original
			binding := *original.AuthorContracts
			binding.Refs = append([]domain.AuthorSourceParagraphRefV1(nil), binding.Refs...)
			changed.AuthorContracts = &binding
			changed.NonNegotiables = append([]string(nil), original.NonNegotiables...)
			switch mode {
			case "catalog_digest":
				changed.AuthorContracts.SourcesDigest = "sha256:" + strings.Repeat("f", 64)
			case "paragraph":
				changed.AuthorContracts.Refs[0].Paragraph = 1
			case "nonneg_text":
				changed.NonNegotiables[0] = "模型替换原文并删去否定。"
			case "nil_mode":
				changed.AuthorContracts = nil
			case "compass_digest":
				changed.CompassDigest = "sha256:" + strings.Repeat("f", 64)
			case "missing_catalog":
				if err := os.Remove(filepath.Join(st.Dir(), AuthorSourcesPath)); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			changed, err = domain.SignOutlineAllExecutionReceipt(changed)
			if err != nil {
				t.Fatalf("fixture must be internally signed but source-invalid: %v", err)
			}
			before, err := DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveOutlineAllExecutionReceipt(changed); err == nil {
				t.Fatal("self-signed receipt replaced actual source authority")
			}
			if _, err := st.UpdateOutlineAllExecutionReceipt(original.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error { *current = changed; return nil }); err == nil {
				t.Fatal("CAS update bypassed source mode checks")
			}
			after, err := DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatalf("rejected source drift changed receipt files: %v", err)
			}
			if err := newIO(st.Dir()).WriteJSON(OutlineAllExecutionReceiptPath, changed); err != nil {
				t.Fatal(err)
			}
			bad, err := os.ReadFile(filepath.Join(st.Dir(), OutlineAllExecutionReceiptPath))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(st.Dir()).LoadOutlineAllExecutionReceipt(); err == nil {
				t.Fatal("reload trusted resigned receipt source drift")
			}
			again, err := os.ReadFile(filepath.Join(st.Dir(), OutlineAllExecutionReceiptPath))
			if err != nil || !bytes.Equal(bad, again) {
				t.Fatalf("reload repaired modified receipt: %v", err)
			}
		})
	}
}

func TestOutlineAllAuthorContractsMissingCatalogCannotDowngradeReceiptToLegacy(t *testing.T) {
	for _, withRefs := range []bool{false, true} {
		st, original := outlineAllAuthorStoreReceipt(t, withRefs)
		if err := st.SaveOutlineAllExecutionReceipt(original); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), AuthorSourcesPath)); err != nil {
			t.Fatal(err)
		}
		legacy := original
		legacy.AuthorContracts = nil
		if len(legacy.NonNegotiables) == 0 {
			legacy.NonNegotiables = []string{"model-invented legacy hard constraint"}
		}
		var err error
		legacy, err = domain.SignOutlineAllExecutionReceipt(legacy)
		if err != nil {
			t.Fatalf("fixture must be a valid legacy-shaped receipt: %v", err)
		}
		before, err := DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		if err := NewStore(st.Dir()).SaveOutlineAllExecutionReceipt(legacy); err == nil {
			t.Fatal("missing catalog let signed legacy receipt replace an existing bound mode")
		}
		if _, err := st.UpdateOutlineAllExecutionReceipt(original.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error { *current = legacy; return nil }); err == nil {
			t.Fatal("CAS replaced bound receipt after catalog loss")
		}
		after, err := DirectoryContentRoot(st.Dir())
		if err != nil || before != after {
			t.Fatalf("catalog-loss receipt refusal changed directory: %v", err)
		}
		// Simulate an independently re-signed legacy receipt already on disk;
		// current compass binding must still prevent all three public paths.
		if err := newIO(st.Dir()).WriteJSON(OutlineAllExecutionReceiptPath, legacy); err != nil {
			t.Fatal(err)
		}
		before, err = DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(st.Dir()).LoadOutlineAllExecutionReceipt(); err == nil {
			t.Fatal("reload treated lost catalog as proof of legacy mode")
		}
		if err := st.SaveOutlineAllExecutionReceipt(legacy); err == nil {
			t.Fatal("legacy-shaped persisted receipt bypassed bound compass mode")
		}
		mutated := false
		if _, err := st.UpdateOutlineAllExecutionReceipt(legacy.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
			mutated = true
			current.CompletedActionCount++
			return nil
		}); err == nil || mutated {
			t.Fatal("legacy-shaped receipt reached mutation before checking bound compass")
		}
		after, err = DirectoryContentRoot(st.Dir())
		if err != nil || before != after {
			t.Fatalf("legacy-shaped receipt rejection repaired or rewrote files: %v", err)
		}
	}
}

package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func authorSourcesStoreFixture(t *testing.T) (*Store, domain.AuthorSourcesV1) {
	t.Helper()
	st := NewStore(t.TempDir())
	catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "project:人物视角.md", Text: "  主角必须保持11岁，不得变成18岁。\r\n原段第二行必须保留。  \r\n\r\n第100章以前不得结局。\n\n必须先完成工作才算完成。"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAuthorSources(catalog); err != nil {
		t.Fatal(err)
	}
	return st, catalog
}

func authorSourcesStoreCompass(catalog domain.AuthorSourcesV1, paragraphs ...int) domain.StoryCompass {
	compass := domain.StoryCompass{EndingDirection: "模型建议的柔性终局方向", AuthorContracts: &domain.CompassAuthorContractsV1{Policy: catalog.Policy, SourcesDigest: catalog.Digest}}
	for _, paragraph := range paragraphs {
		compass.AuthorContracts.Refs = append(compass.AuthorContracts.Refs, domain.AuthorSourceParagraphRefV1{SourceID: catalog.Sources[0].ID, Paragraph: paragraph})
	}
	return compass
}

func TestAuthorSourcesStoreImmutableRoundTripAndExactHashes(t *testing.T) {
	st, catalog := authorSourcesStoreFixture(t)
	before, err := os.ReadFile(filepath.Join(st.Dir(), AuthorSourcesPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).SaveAuthorSources(catalog); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadAuthorSources()
	if err != nil || loaded == nil || !reflect.DeepEqual(*loaded, catalog) {
		t.Fatalf("original author bytes/hash lost: %+v %v", loaded, err)
	}
	different, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Sources: []domain.AuthorSourceV1{{ID: "different", Text: "另一份作者输入"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAuthorSources(different); err == nil {
		t.Fatal("immutable catalog was replaced")
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), AuthorSourcesPath))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("immutable/idempotent save rewrote bytes: %v", err)
	}
	var changed domain.AuthorSourcesV1
	if err := json.Unmarshal(before, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Sources[0].Text = "主角可以直接18岁。"
	if err := newIO(st.Dir()).WriteJSON(AuthorSourcesPath, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadAuthorSources(); err == nil {
		t.Fatal("catalog text drift retained stale authority")
	}
	if err := st.SaveAuthorSources(catalog); err == nil {
		t.Fatal("catalog save repaired corrupted immutable input")
	}
}

func TestAuthorSourcesCompassPreparationIsReadOnlyAndSaveMaterializes(t *testing.T) {
	st, catalog := authorSourcesStoreFixture(t)
	compass := authorSourcesStoreCompass(catalog, 0, 1)
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := st.Outline.PrepareCompass(compass)
	if err != nil {
		t.Fatal(err)
	}
	after, err := DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatalf("PrepareCompass wrote state: %v", err)
	}
	want := domain.AuthorSourceParagraphsV1(catalog.Sources[0].Text)[:2]
	if !reflect.DeepEqual(prepared.NonNegotiables, want) || compass.NonNegotiables != nil {
		t.Fatal("host preparation changed author paragraphs or input")
	}
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(st.Dir()).Outline.LoadCompass()
	if err != nil || loaded == nil || !reflect.DeepEqual(loaded.NonNegotiables, want) || !reflect.DeepEqual(loaded.AuthorContracts, prepared.AuthorContracts) {
		t.Fatalf("stored compass lost source binding: %+v %v", loaded, err)
	}
}

func TestAuthorSourcesCompassRejectsDowngradeTamperingAndOmittedContractsWithoutWrites(t *testing.T) {
	for _, mode := range []string{"nil_binding", "substring", "wrong_source", "missing_nonneg", "changed_nonneg", "nonstring_nonneg", "missing_catalog"} {
		t.Run(mode, func(t *testing.T) {
			st, catalog := authorSourcesStoreFixture(t)
			compass := authorSourcesStoreCompass(catalog, 0)
			if err := st.Outline.SaveCompass(compass); err != nil {
				t.Fatal(err)
			}
			if mode == "nil_binding" || mode == "substring" || mode == "wrong_source" {
				switch mode {
				case "nil_binding":
					compass.AuthorContracts = nil
				case "substring":
					compass.NonNegotiables = []string{"主角必须保持11岁"}
				case "wrong_source":
					compass.AuthorContracts.Refs[0].SourceID = "model-generated"
				}
				before, err := DirectoryContentRoot(st.Dir())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := st.Outline.PrepareCompass(compass); err == nil {
					t.Fatal("invalid reference reached a materialized preflight")
				}
				if err := st.Outline.SaveCompass(compass); err == nil {
					t.Fatal("invalid bound compass was saved")
				}
				after, err := DirectoryContentRoot(st.Dir())
				if err != nil || before != after {
					t.Fatalf("invalid source reference changed files: %v", err)
				}
				return
			}
			if mode == "missing_catalog" {
				if err := os.Remove(filepath.Join(st.Dir(), AuthorSourcesPath)); err != nil {
					t.Fatal(err)
				}
			} else {
				raw, err := os.ReadFile(filepath.Join(st.Dir(), "meta/compass.json"))
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]any
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "missing_nonneg":
					delete(decoded, "non_negotiables")
				case "changed_nonneg":
					decoded["non_negotiables"] = []string{"主角可以变成18岁。"}
				case "nonstring_nonneg":
					decoded["non_negotiables"] = append(decoded["non_negotiables"].([]any), 42)
				}
				if err := newIO(st.Dir()).WriteJSON("meta/compass.json", decoded); err != nil {
					t.Fatal(err)
				}
			}
			before, err := DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.Outline.LoadCompass(); err == nil {
				t.Fatal("load silently repaired or downgraded altered compass")
			}
			after, err := DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatalf("load repaired corruption on disk: %v", err)
			}
		})
	}
}

func TestAuthorSourcesCompassLegacyMigrationAndReferencePreservation(t *testing.T) {
	st := NewStore(t.TempDir())
	legacy := domain.StoryCompass{EndingDirection: "旧模型推断终局", NonNegotiables: []string{"旧模型自行扩大约束"}}
	if err := st.Outline.SaveCompass(legacy); err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(legacy, "", "  ")
	raw, err := os.ReadFile(filepath.Join(st.Dir(), "meta/compass.json"))
	if err != nil || !bytes.Equal(want, raw) {
		t.Fatalf("legacy compass byte behavior changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), authorSourcesLockPath)); !os.IsNotExist(err) {
		t.Fatal("legacy compass added author-source lock metadata")
	}
	_, catalog := authorSourcesStoreFixture(t)
	if err := st.SaveAuthorSources(catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Outline.LoadCompass(); err == nil {
		t.Fatal("catalog presence allowed old nil binding to downgrade policy")
	}
	bound := authorSourcesStoreCompass(catalog, 0)
	if err := st.Outline.SaveCompass(bound); err != nil {
		t.Fatalf("formal migration of legacy generated constraints failed: %v", err)
	}
	extended := authorSourcesStoreCompass(catalog, 1, 0)
	if err := st.Outline.SaveCompass(extended); err != nil {
		t.Fatalf("retaining old reference set with additions failed: %v", err)
	}
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Outline.PrepareCompass(authorSourcesStoreCompass(catalog, 0)); err == nil {
		t.Fatal("preflight permitted removal of existing author reference")
	}
	if err := st.Outline.SaveCompass(authorSourcesStoreCompass(catalog, 2)); err == nil {
		t.Fatal("save replaced prior reference identities")
	}
	after, err := DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatalf("reference deletion rejection wrote files: %v", err)
	}
}

func TestAuthorSourcesCompassMissingCatalogAndEmptySelection(t *testing.T) {
	_, catalog := authorSourcesStoreFixture(t)
	st := NewStore(t.TempDir())
	bound := authorSourcesStoreCompass(catalog, 0)
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Outline.PrepareCompass(bound); err == nil {
		t.Fatal("bound compass without catalog passed preflight")
	}
	if err := st.Outline.SaveCompass(bound); err == nil {
		t.Fatal("bound compass without catalog saved")
	}
	after, err := DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatalf("missing catalog refusal created metadata: %v", err)
	}
	if err := st.SaveAuthorSources(catalog); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(authorSourcesStoreCompass(catalog)); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Outline.LoadCompass()
	if err != nil || loaded == nil || len(loaded.NonNegotiables) != 0 {
		t.Fatalf("empty author selection was forced to invent hard contracts: %+v %v", loaded, err)
	}
}

func TestAuthorSourcesStoreConcurrentPublicationAndCompassCASPreserveReferences(t *testing.T) {
	st, catalog := authorSourcesStoreFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- NewStore(st.Dir()).SaveAuthorSources(catalog) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Outline.SaveCompass(authorSourcesStoreCompass(catalog, 0)); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 2)
	for _, paragraph := range []int{1, 2} {
		wg.Add(1)
		go func(paragraph int) {
			defer wg.Done()
			errs <- NewStore(st.Dir()).Outline.SaveCompass(authorSourcesStoreCompass(catalog, 0, paragraph))
		}(paragraph)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent reference additions lost first durable extension; success=%d", success)
	}
	loaded, err := st.Outline.LoadCompass()
	if err != nil || loaded == nil || len(loaded.AuthorContracts.Refs) != 2 || loaded.AuthorContracts.Refs[0].Paragraph != 0 {
		t.Fatalf("concurrent save replaced prior source reference: %+v %v", loaded, err)
	}
}

func TestAuthorSourcesMissingCatalogCannotDowngradeExistingBoundCompass(t *testing.T) {
	for _, emptyRefs := range []bool{false, true} {
		st, catalog := authorSourcesStoreFixture(t)
		bound := authorSourcesStoreCompass(catalog, 0)
		if emptyRefs {
			bound = authorSourcesStoreCompass(catalog)
		}
		if err := st.Outline.SaveCompass(bound); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), AuthorSourcesPath)); err != nil {
			t.Fatal(err)
		}
		// The simulated catalog loss is setup, not an operation under test.
		before, err := DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		legacyPayload := domain.StoryCompass{EndingDirection: "模型新的无来源方向", NonNegotiables: []string{"模型凭空的新硬合同"}}
		if _, err := NewStore(st.Dir()).Outline.PrepareCompass(legacyPayload); err == nil {
			t.Fatal("preflight treated missing catalog as permission to remove existing source binding")
		}
		if err := NewStore(st.Dir()).Outline.SaveCompass(legacyPayload); err == nil {
			t.Fatal("save downgraded existing source-bound compass after catalog loss")
		}
		after, err := DirectoryContentRoot(st.Dir())
		if err != nil || before != after {
			t.Fatalf("missing-catalog downgrade refusal changed directory: %v", err)
		}
	}
}

func TestAuthorSourcesLegacyFallbackRejectsUnparseableExistingCompassWithoutWrites(t *testing.T) {
	for _, raw := range []string{`{"ending_direction":`, `null`, `{}`, `{"ending_direction":"old","author_contracts":{"refs":[{"source_id":"author"}]}}`} {
		st := NewStore(t.TempDir())
		if err := newIO(st.Dir()).WriteFileUnlocked("meta/compass.json", []byte(raw)); err != nil {
			t.Fatal(err)
		}
		before, err := DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		incoming := domain.StoryCompass{EndingDirection: "replacement"}
		if _, err := st.Outline.PrepareCompass(incoming); err == nil {
			t.Fatalf("unparseable prior compass established legacy mode: %s", raw)
		}
		if err := st.Outline.SaveCompass(incoming); err == nil {
			t.Fatalf("legacy fast path overwrote unparseable prior compass: %s", raw)
		}
		after, err := DirectoryContentRoot(st.Dir())
		if err != nil || before != after {
			t.Fatalf("unparseable fallback refusal changed directory: %v", err)
		}
	}
}

func TestAuthorSourcesGenuineLegacyOverwriteRetainsOriginalBytesAndNoMarkerFiles(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "old direction"}); err != nil {
		t.Fatal(err)
	}
	incoming := domain.StoryCompass{EndingDirection: "updated direction", NonNegotiables: []string{"legacy hard"}, LastUpdated: 5}
	before, err := DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := st.Outline.PrepareCompass(incoming)
	if err != nil || !reflect.DeepEqual(prepared, incoming) {
		t.Fatalf("valid legacy preflight changed behavior: %+v %v", prepared, err)
	}
	after, err := DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatalf("legacy preflight wrote files: %v", err)
	}
	if err := st.Outline.SaveCompass(incoming); err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(incoming, "", "  ")
	actual, err := os.ReadFile(filepath.Join(st.Dir(), "meta/compass.json"))
	if err != nil || !bytes.Equal(want, actual) {
		t.Fatalf("legacy overwrite changed original JSON bytes: %v", err)
	}
	for _, path := range []string{AuthorSourcesPath, authorSourcesLockPath} {
		if _, err := os.Stat(filepath.Join(st.Dir(), path)); !os.IsNotExist(err) {
			t.Fatalf("legacy overwrite created author mode metadata: %s", path)
		}
	}
}

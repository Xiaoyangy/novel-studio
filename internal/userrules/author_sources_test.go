package userrules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestAuthorSourcesBuildPreservesVerifiedHostCatalogNotNormalizerEnvelope(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "corrupt"}[corrupt], func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			const original = "不强制离泊；恰好3章，每章2200—2500字。"
			catalog, err := BuildAuthorSourceCatalog(original, rules.LoadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveAuthorSources(catalog); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				catalog.Sources[0].Text = "伪造的新作者文字"
				raw, _ := json.Marshal(catalog)
				if err := os.WriteFile(filepath.Join(st.Dir(), store.AuthorSourcesPath), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewService(st, nil, rules.LoadOptions{}).Build(t.Context(), "[创作指令]\n"+original)
			if corrupt {
				if err == nil {
					t.Fatal("corrupt Host catalog accepted")
				}
				after, loadErr := store.DirectoryContentRoot(st.Dir())
				if loadErr != nil || before != after {
					t.Fatal("corrupt source rejection wrote normalized rules")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := st.LoadAuthorSources()
			if err != nil || got == nil || !reflect.DeepEqual(*got, catalog) {
				t.Fatalf("normalizer label replaced Host source: %v", err)
			}
		})
	}
}

func TestAuthorSourcesFreshBuildPreservesExactAuthorAndFileSources(t *testing.T) {
	st := store.NewStore(t.TempDir())
	ruleDir := t.TempDir()
	const ruleText = "不把比喻作为事实。\r\n\r\n允许角色合理拒绝，不为预定情节合作。\n"
	if err := os.WriteFile(filepath.Join(ruleDir, "style.md"), []byte(ruleText), 0600); err != nil {
		t.Fatal(err)
	}
	opts := rules.LoadOptions{ProjectRulesDir: ruleDir}
	const author = "  全书恰好3章，每章2200—2500字。\n\n不强制离泊，不要求第四名角色。\n"
	svc := NewService(st, nil, opts)
	if _, err := svc.Build(t.Context(), author); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadAuthorSources()
	if err != nil || got == nil {
		t.Fatalf("missing author catalog: %v", err)
	}
	want, err := BuildAuthorSourceCatalog(author, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) || len(got.Sources) != 2 {
		t.Fatal("source was normalized, omitted or fabricated")
	}
	byID := map[string]string{}
	for _, source := range got.Sources {
		byID[source.ID] = source.Text
	}
	if byID["startup_prompt"] != author || byID["project:style.md"] != ruleText {
		t.Fatalf("original negation/numbers/bytes changed: %#v", byID)
	}
	ordinary, err := json.Marshal(rules.RawFileSources(opts))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ordinary), "OriginalText") || strings.Contains(string(ordinary), "original_text") {
		t.Fatal("Host-only source bytes escaped into ordinary source/model serialization")
	}
	if _, err := svc.GetOrBuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.LoadAuthorSources()
	if err != nil || !reflect.DeepEqual(got, reloaded) {
		t.Fatalf("cached normalization altered source: %v", err)
	}
}

func TestAuthorSourcesLegacyNormalizationDoesNotUpgrade(t *testing.T) {
	for _, variant := range []string{"lazy", "existing_snapshot", "existing_compass"} {
		t.Run(variant, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			var prior []byte
			if variant == "existing_snapshot" {
				if err := st.UserRules.Save(&rules.Snapshot{Version: rules.SnapshotVersion, Status: rules.StatusReady, Preferences: "旧作者合同"}); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "existing_compass" {
				if err := st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "旧结局", NonNegotiables: []string{"旧合同"}}); err != nil {
					t.Fatal(err)
				}
				var err error
				prior, err = os.ReadFile(filepath.Join(st.Dir(), "meta/compass.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			svc := NewService(st, nil, rules.LoadOptions{})
			var err error
			if variant == "lazy" {
				_, err = svc.GetOrBuild(t.Context())
			} else {
				_, err = svc.Build(t.Context(), "新规则归一化仍不重签旧compass")
			}
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := st.LoadAuthorSources()
			if err != nil || catalog != nil {
				t.Fatalf("legacy path silently upgraded: %v", err)
			}
			if prior != nil {
				after, err := os.ReadFile(filepath.Join(st.Dir(), "meta/compass.json"))
				if err != nil || string(prior) != string(after) {
					t.Fatal("legacy compass changed")
				}
			}
		})
	}
}

package tools

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
	"github.com/chenhongyang/novel-studio/internal/userrules"
)

func TestCompassAuthorSourcesOrdinaryFreshSchemaProvidesExecutableCoordinates(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const author = "恰好3章，每章2200—2500字。\r\n\r\n不强制离泊，不增加第四名重要角色。\n"
	if _, err := userrules.NewService(st, nil, rules.LoadOptions{}).Build(t.Context(), author); err != nil {
		t.Fatal(err)
	}
	const transient = "HOST_PHASE_ONLY_禁止writer且本次只修资料"
	if err := st.Outline.SavePremise(transient); err != nil {
		t.Fatal(err)
	}
	tool := NewSaveFoundationTool(st)
	content := tool.Schema()["properties"].(map[string]any)["content"].(map[string]any)["description"].(string)
	_, raw, found := strings.Cut(content, authorCompassCatalogLabel)
	if !found || strings.Contains(raw, transient) {
		t.Fatal("ordinary author tool lacks source coordinates or copied generated workflow")
	}
	var view authorCompassCatalogView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	if view.Policy != domain.AuthorSourcesPolicyV1 || view.SourcesDigest == "" || len(view.Sources) != 1 || view.Sources[0].SourceID != "startup_prompt" {
		t.Fatal("wrong author source catalog view")
	}
	want := domain.AuthorSourceParagraphsV1(author)
	if len(view.Sources[0].Paragraphs) != len(want) {
		t.Fatal("author paragraphs were omitted")
	}
	// Build the only writable binding solely from the actual tool schema.
	// A normal Architect has no CLI catalog injection or filesystem reader.
	binding := domain.CompassAuthorContractsV1{Policy: view.Policy, SourcesDigest: view.SourcesDigest}
	for i, p := range view.Sources[0].Paragraphs {
		if p.Paragraph != i || p.Text != want[i] {
			t.Fatal("source text/indices changed")
		}
		binding.Refs = append(binding.Refs, domain.AuthorSourceParagraphRefV1{SourceID: view.Sources[0].SourceID, Paragraph: p.Paragraph})
	}
	args, _ := json.Marshal(map[string]any{"type": "update_compass", "content": domain.StoryCompass{EndingDirection: "暂定软方向", AuthorContracts: &binding}})
	if _, err := tool.Execute(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	got, err := store.NewStore(st.Dir()).Outline.LoadCompass()
	if err != nil || got == nil || !reflect.DeepEqual(got.NonNegotiables, want) {
		t.Fatalf("schema-only binding did not persist original constraints: %v", err)
	}
}

func TestCompassAuthorSourcesCorruptCatalogGivesNoCoordinatesAndRejectsWrite(t *testing.T) {
	st, catalog, compass := authorCompassToolFixture(t)
	catalog.Sources[0].Text = "CORRUPTED_UNVERIFIED_AUTHOR_TEXT"
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), store.AuthorSourcesPath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSaveFoundationTool(st)
	schemaJSON, err := json.Marshal(tool.Schema())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schemaJSON), catalog.Digest) || strings.Contains(string(schemaJSON), catalog.Sources[0].Text) || strings.Contains(string(schemaJSON), "sources_digest") {
		t.Fatal("invalid author source was advertised as verified")
	}
	args, _ := json.Marshal(map[string]any{"type": "update_compass", "scale": "long", "content": compass})
	if _, err := tool.Execute(t.Context(), args); err == nil {
		t.Fatal("corrupt catalog accepted")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("corrupt schema/write path changed data")
	}
}

func TestCompassAuthorSourcesLostCatalogCannotDowngradeThroughTool(t *testing.T) {
	st, _, compass := authorCompassToolFixture(t)
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(st.Dir(), store.AuthorSourcesPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Outline.LoadCompass(); err == nil {
		t.Fatal("lost catalog unexpectedly readable")
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	compass.AuthorContracts = nil
	compass.NonNegotiables = []string{"无来源的新条款"}
	raw, err := json.Marshal(map[string]any{"type": "update_compass", "scale": "long", "content": compass})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(t.Context(), raw); err == nil {
		t.Fatal("lost catalog was downgraded to legacy")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("downgrade rejection changed original compass, tier, checkpoint or retrieval")
	}
	if _, err := store.NewStore(st.Dir()).Outline.LoadCompass(); err == nil {
		t.Fatal("failed tool repaired/erased the missing-source marker")
	}
}

func authorCompassToolFixture(t *testing.T) (*store.Store, domain.AuthorSourcesV1, domain.StoryCompass) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// Prime the existing process-lock file: it is operational guard setup,
	// not a change made by the source precondition being measured below.
	if _, err := st.Runtime.LoadPipelineExecution(); err != nil {
		t.Fatal(err)
	}
	catalog, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Policy: domain.AuthorSourcesPolicyV1, Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: "恰好3章，每章2200—2500字；不增加第四名重要角色。\n\n不强制离泊。角色自主选择，第三章解除错误归责。"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAuthorSources(catalog); err != nil {
		t.Fatal(err)
	}
	c := domain.StoryCompass{EndingDirection: "模型选定的柔性终局方向", AuthorContracts: &domain.CompassAuthorContractsV1{Policy: domain.AuthorSourcesPolicyV1, SourcesDigest: catalog.Digest, Refs: []domain.AuthorSourceParagraphRefV1{{SourceID: "startup_prompt", Paragraph: 0}, {SourceID: "startup_prompt", Paragraph: 1}}}}
	return st, catalog, c
}

func TestCompassAuthorSourcesToolMaterializesCompleteOriginalContracts(t *testing.T) {
	st, catalog, c := authorCompassToolFixture(t)
	raw, err := json.Marshal(map[string]any{"type": "update_compass", "scale": "short", "content": c})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	got, err := st.Outline.LoadCompass()
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	want := domain.AuthorSourceParagraphsV1(catalog.Sources[0].Text)
	if !reflect.DeepEqual(got.NonNegotiables, want) || !reflect.DeepEqual(domain.CompassHardContractsV1(*got), want) {
		t.Fatal("Host changed/omitted original denial, range or role/ending requirements")
	}
	if strings.Contains(strings.Join(domain.CompassHardContractsV1(*got), "\n"), c.EndingDirection) {
		t.Fatal("soft ending became author contract")
	}
	checkpoints, err := st.Checkpoints.AllStrict()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("saved %d checkpoints", len(checkpoints))
	}
}

func TestCompassAuthorSourcesToolRejectsInventedOrDowngradedBeforeAnyWrite(t *testing.T) {
	for _, kind := range []string{"host_phase", "negation", "numeric_range", "unknown_source", "outside_paragraph", "wrong_digest", "nil_binding", "delete_prior"} {
		t.Run(kind, func(t *testing.T) {
			st, _, c := authorCompassToolFixture(t)
			if kind == "delete_prior" {
				if err := st.Outline.SaveCompass(c); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "host_phase":
				c.NonNegotiables = []string{"用户原话：本次只完成foundation，禁止writer/drafter/editor。"}
			case "negation":
				c.NonNegotiables = []string{"强制离泊。角色自主选择，第三章解除错误归责。"}
			case "numeric_range":
				c.NonNegotiables = []string{"恰好3章，每章1000—1500字；不增加第四名重要角色。"}
			case "unknown_source":
				c.AuthorContracts.Refs[0].SourceID = "host_workflow"
			case "outside_paragraph":
				c.AuthorContracts.Refs[0].Paragraph = 100
			case "wrong_digest":
				c.AuthorContracts.SourcesDigest = "sha256:" + strings.Repeat("0", 64)
			case "nil_binding":
				c.AuthorContracts = nil
			case "delete_prior":
				c.AuthorContracts.Refs = c.AuthorContracts.Refs[:1]
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{"type": "update_compass", "scale": "long", "content": c})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewSaveFoundationTool(st).Execute(t.Context(), raw); err == nil {
				t.Fatal("invalid source contract accepted")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("rejected source contract changed tier/checkpoint/foundation")
			}
		})
	}
}

func TestCompassAuthorSourcesSchemaOptInAndEmptyReferences(t *testing.T) {
	legacy := store.NewStore(t.TempDir())
	if err := legacy.Init(); err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(NewSaveFoundationTool(legacy).Schema())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal((&SaveFoundationTool{}).Schema())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || strings.Contains(string(a), "author-sources.v1") {
		t.Fatal("legacy schema upgraded")
	}
	st, _, c := authorCompassToolFixture(t)
	c.AuthorContracts.Refs = nil
	raw, _ := json.Marshal(map[string]any{"type": "update_compass", "content": c})
	if _, err := NewSaveFoundationTool(st).Execute(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	got, err := st.Outline.LoadCompass()
	if err != nil || got == nil || len(got.NonNegotiables) != 0 {
		t.Fatalf("invented nonempty requirement: %v", err)
	}
	newSchema, _ := json.Marshal(NewSaveFoundationTool(st).Schema())
	if !strings.Contains(string(newSchema), "author-sources.v1") {
		t.Fatal("new source contract unavailable in schema")
	}
}

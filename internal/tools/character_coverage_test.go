package tools

import (
	"slices"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRequiredDossierCharacterNamesCoversWholeSimulationCast(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	chars := []domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
		{Name: "叶南栀", Role: "主角团配角", Tier: "important"},
		{Name: "周曼", Role: "配角", Tier: "important"},
	}
	if err := st.Characters.Save(chars); err != nil {
		t.Fatal(err)
	}
	for _, c := range chars {
		if err := st.SaveCharacterDossier(domain.CharacterDossier{Character: c.Name, Role: c.Role, Tier: c.Tier}); err != nil {
			t.Fatal(err)
		}
	}

	names := requiredDossierCharacterNames(st, 1)
	for _, want := range []string{"林澈", "沈知遥", "叶南栀", "周曼"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected %s in required names: %v", want, names)
		}
	}
}

func TestChapterOutlineCharacterNamesInfersParentsFromOutline(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
		{Name: "林建国", Role: "主角父亲", Tier: "important"},
		{Name: "周曼", Role: "主角母亲", Tier: "important"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "失业饭桌",
		CoreEvent: "林澈返乡饭桌被亲戚阴阳失业，父母嘴硬护不住面子。",
	}}); err != nil {
		t.Fatal(err)
	}

	names := chapterOutlineCharacterNames(st, 1)
	for _, want := range []string{"林澈", "林建国", "周曼"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected %s in required names: %v", want, names)
		}
	}
	if slices.Contains(names, "沈知遥") {
		t.Fatalf("female lead should stay dormant until the outline authorizes her: %v", names)
	}
}

func TestChapterOutlineCharacterNamesIncludesOutlineMention(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "叶南栀", Role: "主角团配角", Tier: "important"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "饭桌",
		CoreEvent: "林澈接到叶南栀电话，决定回青山县。",
	}}); err != nil {
		t.Fatal(err)
	}

	names := chapterOutlineCharacterNames(st, 1)
	if !slices.Contains(names, "叶南栀") {
		t.Fatalf("outline-mentioned character should be required: %v", names)
	}
}

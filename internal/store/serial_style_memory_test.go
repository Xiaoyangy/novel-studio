package store

import (
	"slices"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

func TestCanonicalSerialStyleMemoryStopwordsIncludesStructuredWorldNames(t *testing.T) {
	s := newTestStore(t)
	if err := s.Characters.Save([]domain.Character{{Name: "沈岚", Aliases: []string{"阿岚"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cast.Save([]domain.CastEntry{{Name: "陆九渊", Aliases: []string{"陆掌柜"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveBookWorld(domain.BookWorld{
		Name:   "雾海洲",
		Places: []domain.WorldPlace{{Name: "青云山", Description: "不应纳入的地点描述"}},
		Factions: []domain.WorldFaction{{
			Name: "巡夜司", Aliases: []string{"夜巡"}, Goal: "不应纳入的势力目标",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorldCodex(domain.WorldCodex{
		NovelName:    "雾海长明",
		AbilityTiers: []domain.CodexAbilityTier{{Name: "照烛境", Aliases: []string{"烛境"}, Magnitude: "不应纳入的量级描述", Samples: []string{"不应纳入的样本"}}},
		SkillDomains: []domain.CodexDomainEntry{{Name: "观星术", Description: "不应纳入的技能描述", Constraints: []string{"不应纳入的技能规则"}}},
		Races:        []domain.CodexRace{{Name: "羽民", Description: "不应纳入的种族描述"}},
		WeaponCategories: []domain.CodexGradedCategory{{
			Name: "符刃", Grades: []string{"玄铁级", "星银级"}, Description: "不应纳入的武器描述",
		}},
		EquipmentCategories: []domain.CodexGradedCategory{{
			Name: "灵甲", Grades: []string{"织雾级"}, Constraints: []string{"不应纳入的装备规则"},
		}},
		Sections: []domain.CodexSection{{Title: "不应纳入的章节标题", Content: "不应纳入的法典正文", Rules: []string{"不应纳入的法典规则"}}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.CanonicalSerialStyleMemoryStopwords()
	if err != nil {
		t.Fatal(err)
	}
	want := stylestat.CanonicalStopwords([]string{
		"沈岚", "阿岚", "陆九渊", "陆掌柜",
		"雾海洲", "青云山", "巡夜司", "夜巡",
		"雾海长明", "照烛境", "烛境", "观星术", "羽民",
		"符刃", "玄铁级", "星银级", "灵甲", "织雾级",
	})
	if !slices.Equal(got, want) {
		t.Fatalf("canonical stopwords:\n got %#v\nwant %#v", got, want)
	}
}

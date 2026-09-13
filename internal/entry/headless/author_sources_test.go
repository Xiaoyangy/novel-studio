package headless

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/userrules"
)

func TestPrepareUserRulesCapturesOnlyOriginalAuthorSources(t *testing.T) {
	st := store.NewStore(t.TempDir())
	service := userrules.NewService(st, nil, rules.LoadOptions{})
	const author = "全书3章，每章2200—2500字。\n\n不强制离泊，不增加第四名重要角色。"
	const temporary = "本次只完成foundation，禁止writer；修复时不得覆盖其他文件。"
	calls := 0
	prepare := func(source string) error { calls++; _, err := service.Build(t.Context(), source); return err }
	if err := prepareUserRules(Options{Prompt: author + "\n" + temporary, UserRulesPrompt: author}, author+"\n"+temporary, prepare); err != nil {
		t.Fatal(err)
	}
	catalog, err := st.LoadAuthorSources()
	if err != nil || catalog == nil {
		t.Fatalf("no host source: %v", err)
	}
	if calls != 1 || len(catalog.Sources) != 1 || catalog.Sources[0].Text != author {
		t.Fatal("workflow/repair note became author source")
	}
	if err := prepareUserRules(Options{PreserveUserRules: true, UserRulesPrompt: temporary}, temporary, prepare); err != nil {
		t.Fatal(err)
	}
	again, err := st.LoadAuthorSources()
	if err != nil || again.Digest != catalog.Digest || calls != 1 {
		t.Fatalf("repair replaced author source: %v", err)
	}
}

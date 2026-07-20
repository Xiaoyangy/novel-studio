package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProductionGoSourcesContainNoRetiredStoryFixtures prevents emergency
// fixes for one manuscript from silently becoming engine policy again. Test
// fixtures may still use concrete stories; non-test Go code must load those
// facts from the active project's assets and frozen contracts.
func TestProductionGoSourcesContainNoRetiredStoryFixtures(t *testing.T) {
	t.Parallel()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contamination guard source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	retiredFixtures := []string{
		"零点四十分", "我在直播间给凶手点外卖", "她的第二算法", "只许把钱花在青山县",
		"林澈", "沈知遥", "贺骁", "周曼", "林建国", "马玉芬", "梁广财",
		"梁渡", "夏岚", "傅行简", "程棠", "乔安", "邱梅", "陆敏", "韩璐",
		"陈思予", "周临", "陈砚青", "许牧", "叶南栀", "罗湘", "孟嘉仪", "周蕴", "许闻溪",
		"江烬", "江禾", "温梨", "周行舟", "冥府黑卡", "阴阳公寓", "镇厄局",
		"红伞医院", "白骨财神", "冥雾", "夜租", "收租鬼", "人格资产",
		"青山县专项经营额度", "桥点工作室", "溪流助手",
		"澄光系统", "本地新增交付", "小额改善额度", "1704快开", "七单",
	}

	for _, sourceRoot := range []string{filepath.Join(repoRoot, "cmd"), filepath.Join(repoRoot, "internal")} {
		err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, fixture := range retiredFixtures {
				if strings.Contains(string(raw), fixture) {
					rel, _ := filepath.Rel(repoRoot, path)
					t.Errorf("production source %s contains retired story fixture %q", rel, fixture)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

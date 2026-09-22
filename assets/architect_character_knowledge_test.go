package assets

import (
	"strings"
	"testing"
)

func TestArchitectLongSeparatesActualKnowledgeFromAuthorExclusions(t *testing.T) {
	prompt := Load("default").Prompts.ArchitectLong
	for _, want := range []string{"全句可见性", "否定句", "身份尚未核实", "已知姓名但不知道去向", "作者态"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("character source guidance omits %q", want)
		}
	}
	if strings.Contains(prompt, "跨卷弧线也揉进这里讲完") {
		t.Fatal("character description still asks for future arc material")
	}
}

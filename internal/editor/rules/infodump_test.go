package rules

import "testing"

func TestDialogueInfoDumpFlags(t *testing.T) {
	// 信息倾倒：一口气罗列客户清单+房号+背景 → 命中
	dump := `他说：“我这边有几户客户，205的周阿姨，她儿子在外地，309那个开小面馆的，还有一户带孩子的，他们都收到单了，钱凑出来了。”`
	if len(dialogueInfoDumpFlags(dump)) == 0 {
		t.Fatal("信息倾倒式对白应被命中")
	}
	// 正常断续对白：短、被打断、无罗列 → 不命中
	normal := `“别在这里报全名。”他说。
她怔了一下：“你嫌少？”
“收了钱才要负责。”`
	if len(dialogueInfoDumpFlags(normal)) != 0 {
		t.Fatalf("正常对白不应命中: %+v", dialogueInfoDumpFlags(normal))
	}
}

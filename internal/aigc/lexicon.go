package aigc

import (
	_ "embed"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// Task 059：slop 词表语料化。内置词表从硬编码迁出为 embed JSON（保留原 var cliches
// 字面量为最终兜底），支持项目级 meta/slop_lexicon.json 覆盖合并。
// 加载顺序：项目覆盖 > embed 数据 > 内置字面量。词表版本随 Report.LexiconVersion
// 落进 ai_gate JSON，保证历史报告可解释。语料驱动的再生成走
// quality/audit/scripts/build_slop_lexicon.py（EQ-Bench 60/25/15 权重结构）。

//go:embed slop_lexicon.json
var builtinLexiconJSON []byte

// SlopLexicon slop 词表数据结构（与 build_slop_lexicon.py 输出对齐）。
type SlopLexicon struct {
	Version string              `json:"version"`
	Source  string              `json:"source,omitempty"`
	BuiltAt string              `json:"built_at,omitempty"`
	Groups  map[string][]string `json:"groups"`
	Weights map[string]float64  `json:"weights,omitempty"`
}

var (
	lexiconMu     sync.RWMutex
	activeLexicon SlopLexicon
	loadedProject string // 已加载覆盖的项目目录（幂等）
)

func init() {
	var lex SlopLexicon
	if err := json.Unmarshal(builtinLexiconJSON, &lex); err == nil && len(lex.Groups) > 0 {
		activeLexicon = lex
		cliches = lex.Groups
		return
	}
	// embed 损坏时兜底：内置字面量继续生效。
	activeLexicon = SlopLexicon{Version: "builtin-literal", Groups: cliches}
}

// LoadProjectLexicon 合并项目级覆盖（<dir>/meta/slop_lexicon.json）：同名组替换、
// 新组追加，版本取覆盖文件的 version。无覆盖文件为 noop；幂等（同目录只加载一次）。
func LoadProjectLexicon(dir string) {
	if dir == "" {
		return
	}
	lexiconMu.Lock()
	defer lexiconMu.Unlock()
	if loadedProject == dir {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "meta", "slop_lexicon.json"))
	if err != nil {
		loadedProject = dir
		return
	}
	var override SlopLexicon
	if err := json.Unmarshal(data, &override); err != nil || len(override.Groups) == 0 {
		loadedProject = dir
		return
	}
	merged := make(map[string][]string, len(activeLexicon.Groups)+len(override.Groups))
	maps.Copy(merged, activeLexicon.Groups)
	maps.Copy(merged, override.Groups)
	activeLexicon.Groups = merged
	if override.Version != "" {
		activeLexicon.Version = override.Version + "+project"
	}
	cliches = merged
	loadedProject = dir
}

// LexiconVersion 当前生效词表版本。
func LexiconVersion() string {
	lexiconMu.RLock()
	defer lexiconMu.RUnlock()
	return activeLexicon.Version
}

// LexiconDigest 供写作/返工上下文注入的规避清单：组名 → 样例词（每组截前 n 个）。
// Writer 生成时与审核不通过修改时都要对照规避——命中会在提交时被机械检测标红。
func LexiconDigest(perGroup int) map[string][]string {
	lexiconMu.RLock()
	defer lexiconMu.RUnlock()
	out := make(map[string][]string, len(activeLexicon.Groups))
	for k, v := range activeLexicon.Groups {
		if len(v) > perGroup {
			out[k] = append([]string(nil), v[:perGroup]...)
		} else {
			out[k] = append([]string(nil), v...)
		}
	}
	return out
}

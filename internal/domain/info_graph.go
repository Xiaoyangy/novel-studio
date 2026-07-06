package domain

import (
	"fmt"
	"sort"
)

// InfoNode 信息差图节点：角色 / 读者 / 叙述者各自"知道什么、误信什么、此刻不该知道什么"。
// 角色侧事实的单一来源是 CharacterKnowledgeLedger；本图是派生快照 + reader/narrator 增量，
// 不做第二事实源（双写必然漂移）。
type InfoNode struct {
	ID             string   `json:"id"`   // 角色名 / "reader" / "narrator"
	Type           string   `json:"type"` // character / reader / narrator
	Knows          []string `json:"knows,omitempty"`
	Believes       []string `json:"believes,omitempty"` // 含 misbelief
	MustNotKnowYet []string `json:"must_not_know_yet,omitempty"`
}

// InfoEdge 两个节点之间的信任与隐瞒关系。
type InfoEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Trust  float64 `json:"trust,omitempty"` // [0,1]
	Hiding bool    `json:"hiding,omitempty"`
}

// InfoGraph 某章时点的信息差快照。落盘 meta/info_graph.json。
type InfoGraph struct {
	Chapter int        `json:"chapter"`
	Nodes   []InfoNode `json:"nodes"`
	Edges   []InfoEdge `json:"edges,omitempty"`
}

// Validate 校验节点 ID 唯一且 edge 引用存在。
func (g InfoGraph) Validate() error {
	seen := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("info_graph 节点缺少 id")
		}
		if _, dup := seen[n.ID]; dup {
			return fmt.Errorf("info_graph 节点 id 重复: %s", n.ID)
		}
		seen[n.ID] = struct{}{}
	}
	for _, e := range g.Edges {
		if _, ok := seen[e.From]; !ok {
			return fmt.Errorf("info_graph edge.from 引用不存在的节点: %s", e.From)
		}
		if _, ok := seen[e.To]; !ok {
			return fmt.Errorf("info_graph edge.to 引用不存在的节点: %s", e.To)
		}
	}
	return nil
}

// BuildInfoGraphFromLedgers 从各角色 KnowledgeLedger 派生角色节点（纯函数，按名字排序保证稳定输出）。
// reader / narrator 节点与 edges 由调用方按需补充。
func BuildInfoGraphFromLedgers(chapter int, ledgers map[string]CharacterKnowledgeLedger) InfoGraph {
	names := make([]string, 0, len(ledgers))
	for name := range ledgers {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	g := InfoGraph{Chapter: chapter}
	for _, name := range names {
		l := ledgers[name]
		g.Nodes = append(g.Nodes, InfoNode{
			ID:             name,
			Type:           "character",
			Knows:          l.KnownFacts,
			Believes:       l.FalseBeliefs,
			MustNotKnowYet: l.ForbiddenKnowledge,
		})
	}
	return g
}

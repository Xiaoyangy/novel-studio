package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var secondAlgorithmContaminationTerms = []string{
	"江烬", "江禾", "蒋牧", "温梨", "周行舟", "白骨财神",
	"鬼城", "阴阳公寓", "阴司", "阴司银行", "冥钞", "夜租",
	"首夜租", "收租鬼", "黑卡", "欠费单", "旧账字段", "1704",
}

var secondAlgorithmDeprecatedEngineTerms = []string{
	"数据合规", "算法审计", "上市审计", "审计演示", "审计顾问", "审计盒",
	"匿名样本", "M17", "封存回执", "字段来源", "现场口径", "补现场口径",
	"权限链", "变量链路", "测试样本口径", "原始读取链", "药量", "数据室",
	"证据链", "日志窗口", "原始包", "合规邮件", "待签纪要", "会议纪要确认",
	"版本差异", "核验原始", "纪要", "日志", "版本不对", "核验",
}

func validateProjectContaminationFree(s *store.Store, label string, payload any) error {
	return validateProjectContamination(s, label, payload, false)
}

func validateProjectContaminationFinal(s *store.Store, label string, payload any) error {
	return validateProjectContamination(s, label, payload, true)
}

func validateProjectContamination(s *store.Store, label string, payload any, requireAnchor bool) error {
	active, hasRequiredAnchor := secondAlgorithmContaminationPolicy(s)
	if !active {
		return nil
	}
	text := payloadText(payload)
	if hits := secondAlgorithmCrossProjectHits(text); len(hits) > 0 {
		return fmt.Errorf("%s 命中跨项目污染词：%s。当前项目是《她的第二算法》，请只使用许闻溪/澄光生活/溪流助手/岗位合并/桥点等本书事实，鬼城案例只能作为抽象写法参考，不能进入计划或正文: %w",
			label, strings.Join(hits, "、"), errs.ErrToolPrecondition)
	}
	if hits := secondAlgorithmDeprecatedEngineHits(text); len(hits) > 0 {
		return fmt.Errorf("%s 命中旧版硬核取证引擎词：%s。重启版《她的第二算法》只能写 AI 改变职业后的女性职场成长，不能滑回技术取证、正式核查或原始材料追查: %w",
			label, strings.Join(hits, "、"), errs.ErrToolPrecondition)
	}
	if requireAnchor && hasRequiredAnchor && !strings.Contains(text, "许闻溪") {
		return fmt.Errorf("%s 缺少《她的第二算法》主角锚点“许闻溪”；请回到本书人物和第一章大纲重写，不能用“主角”或其他项目人物泛化替代: %w",
			label, errs.ErrToolPrecondition)
	}
	return nil
}

func containsSecondAlgorithmContaminationTerm(text string) bool {
	return len(secondAlgorithmCrossProjectHits(text)) > 0 || len(secondAlgorithmDeprecatedEngineHits(text)) > 0
}

// sanitizeProjectDiagnosticForPlan keeps a negative rewrite diagnosis useful
// without reintroducing forbidden cross-project nouns into the executable
// plan. Only the active Second Algorithm restart policy needs this redaction;
// the full review brief remains unchanged as the audit source.
func sanitizeProjectDiagnosticForPlan(s *store.Store, text string) string {
	active, _ := secondAlgorithmContaminationPolicy(s)
	if !active || strings.TrimSpace(text) == "" {
		return strings.TrimSpace(text)
	}
	for _, term := range secondAlgorithmContaminationTerms {
		text = strings.ReplaceAll(text, term, "[跨项目旧设定]")
	}
	for _, term := range secondAlgorithmDeprecatedEngineTerms {
		text = strings.ReplaceAll(text, term, "[旧版取证引擎元素]")
	}
	return strings.TrimSpace(text)
}

func secondAlgorithmCrossProjectHits(text string) []string {
	return orderedTermHits(text, secondAlgorithmContaminationTerms)
}

func secondAlgorithmDeprecatedEngineHits(text string) []string {
	return orderedTermHits(text, secondAlgorithmDeprecatedEngineTerms)
}

func countSecondAlgorithmCrossProjectHits(text string) map[string]int {
	return countTermHits(text, secondAlgorithmContaminationTerms)
}

func countSecondAlgorithmDeprecatedEngineHits(text string) map[string]int {
	return countTermHits(text, secondAlgorithmDeprecatedEngineTerms)
}

func SecondAlgorithmProjectContaminationViolations(s *store.Store, text string) []rules.Violation {
	active, _ := secondAlgorithmContaminationPolicy(s)
	if !active {
		return nil
	}
	var out []rules.Violation
	for _, term := range sortedHitTerms(countSecondAlgorithmCrossProjectHits(text)) {
		out = append(out, rules.Violation{
			Rule:     "project_contamination",
			Target:   term,
			Actual:   strings.Count(text, term),
			Severity: rules.SeverityError,
		})
	}
	for _, term := range sortedHitTerms(countSecondAlgorithmDeprecatedEngineHits(text)) {
		out = append(out, rules.Violation{
			Rule:     "deprecated_story_engine",
			Target:   term,
			Actual:   strings.Count(text, term),
			Severity: rules.SeverityError,
		})
	}
	return out
}

func orderedTermHits(text string, terms []string) []string {
	var hits []string
	for _, term := range terms {
		if strings.Contains(text, term) {
			hits = append(hits, term)
		}
	}
	return hits
}

func countTermHits(text string, terms []string) map[string]int {
	out := map[string]int{}
	for _, term := range terms {
		if term == "" {
			continue
		}
		if n := strings.Count(text, term); n > 0 {
			out[term] = n
		}
	}
	return out
}

func sortedHitTerms(counts map[string]int) []string {
	hits := make([]string, 0, len(counts))
	for term := range counts {
		hits = append(hits, term)
	}
	sort.Strings(hits)
	return hits
}

func secondAlgorithmContaminationPolicy(s *store.Store) (active bool, requiredAnchor bool) {
	chars, err := s.Characters.Load()
	if err == nil {
		for _, c := range chars {
			switch strings.TrimSpace(c.Name) {
			case "江烬":
				return false, false
			case "许闻溪":
				return true, true
			}
		}
	}
	premise, _ := s.Outline.LoadPremise()
	if strings.Contains(premise, "她的第二算法") || strings.Contains(premise, "许闻溪") {
		return true, true
	}
	return false, false
}

func payloadText(payload any) string {
	switch v := payload.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

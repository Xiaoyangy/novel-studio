package domain

import "sort"

const CharacterRevisionFeedbackPolicyV1 = "character-revision-feedback:owner-constraints.v1"

// The new policy exposes only closed host constraint categories. Arbiter text,
// conflict IDs, peer identities and unexecuted intentions never cross into O2.
// The original receipt remains intact. Historical unmarked sources retain
// their exact original feedback binding.
func CharacterArbitrationFeedbackForOwnerV1(receipt WorldArbitrationReceipt, owner string, policies []string) []string {
	safe := physicalContainsRefV2(policies, CharacterRevisionFeedbackPolicyV1)
	var result []string
	seen := map[string]bool{}
	for _, conflict := range receipt.Conflicts {
		if conflict.Resolved {
			continue
		}
		affected := false
		for _, id := range conflict.AffectedAgentIDs {
			if id == owner {
				affected = true
			}
		}
		if !affected {
			continue
		}
		text := conflict.Kind + "：" + conflict.Feedback
		if safe {
			switch conflict.Kind {
			case "time":
				text = "时间约束：请仅据本人观察中的当前时刻和已知期限，重选本人的动作顺序、所需时段及条件；本轮尚未执行。"
			case "resource":
				text = "资源约束：请仅据本人已知资源、访问权与数量感知重选动作；未知数量不当真值，意图或承诺不当已经交付；本轮尚未执行。"
			case "location", "space":
				text = "位置约束：请仅据本人实际位置和已知路径重选动作；目的地不等于已抵达，中途搬运或会面不视为完成；本轮尚未执行。"
			case "knowledge", "information":
				text = "知识约束：请仅引用本人观察中已有的事实；他人报告不当独立核实，未读材料内容不可提前使用；本轮尚未执行。"
			default:
				text = "执行约束：请仅据本人原观察重选可执行的动作、顺序和必要条件；不假定别人已行动或未来成功；本轮尚未执行。"
			}
		}
		if !safe || !seen[text] {
			result = append(result, text)
			seen[text] = true
		}
	}
	if safe {
		sort.Strings(result)
	}
	return result
}

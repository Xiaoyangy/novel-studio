package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// refreshRewriteBriefFromReview makes the latest rewrite/polish review the
// planning source of truth. Without this hand-off a stale "accepted" brief can
// survive a new user steer and send Writer back into the already rejected plan.
func refreshRewriteBriefFromReview(s *store.Store, review domain.ReviewEntry, finalVerdict string) (string, error) {
	if s == nil || review.Scope != "chapter" || review.Chapter <= 0 ||
		(finalVerdict != "rewrite" && finalVerdict != "polish") {
		return "", nil
	}

	existing, err := s.Drafts.LoadRewriteBrief(review.Chapter)
	if err != nil {
		return "", err
	}
	preserveFacts := rewriteBriefPreserveFacts(existing)
	body, err := s.Drafts.LoadChapterText(review.Chapter)
	if err != nil {
		return "", err
	}
	pendingSteer := ""
	if meta, loadErr := s.RunMeta.Load(); loadErr != nil {
		return "", loadErr
	} else if meta != nil {
		pendingSteer = strings.TrimSpace(meta.PendingSteer)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# ch%02d rewrite brief\n\n", review.Chapter)
	b.WriteString("## 当前结论\n\n")
	fmt.Fprintf(&b, "- 最新结构化评审：`%s`。\n", finalVerdict)
	if strings.TrimSpace(body) != "" {
		fmt.Fprintf(&b, "- 待返工正文 SHA-256：`%s`。\n", reviewreport.BodySHA256(body))
	}
	if summary := strings.TrimSpace(review.Summary); summary != "" {
		fmt.Fprintf(&b, "- 本轮结论：%s\n", summary)
	}
	b.WriteString("- 本文件由 save_review 根据最新评审原子刷新；旧版 accept/polish 结论不再参与本轮规划。\n")

	if pendingSteer != "" {
		b.WriteString("\n## 用户本轮要求\n\n")
		fmt.Fprintf(&b, "- %s\n", pendingSteer)
	}

	b.WriteString("\n## 保留事实\n\n")
	writeRewriteBriefItems(&b, preserveFacts)

	b.WriteString("\n## 合同漏项\n\n")
	writeRewriteBriefItems(&b, review.ContractMisses)
	if notes := strings.TrimSpace(review.ContractNotes); notes != "" {
		fmt.Fprintf(&b, "- 说明：%s\n", notes)
	}

	b.WriteString("\n## 必须修正\n\n")
	if len(review.Issues) == 0 {
		b.WriteString("- 按最新评审总结完成目标范围内的返工。\n")
	}
	for _, issue := range review.Issues {
		fmt.Fprintf(&b, "- [%s/%s] %s\n", issue.Type, issue.Severity, strings.TrimSpace(issue.Description))
		if evidence := strings.TrimSpace(issue.Evidence); evidence != "" {
			fmt.Fprintf(&b, "  - 证据：%s\n", evidence)
		}
		if suggestion := strings.TrimSpace(issue.Suggestion); suggestion != "" {
			fmt.Fprintf(&b, "  - 修法：%s\n", suggestion)
		}
	}

	b.WriteString("\n## 验收条件\n\n")
	b.WriteString("- 逐条满足用户本轮要求、合同漏项和 error/critical issue；warning 只在不损伤正文时修正。\n")
	b.WriteString("- 保留事实的事件顺序、金额、地点、角色、结果和章末后果不得漂移。\n")
	b.WriteString("- 必须重新完成世界模拟、POV plan、正文渲染、机械检查与 Editor 审核；不得复用旧 plan 冒充新返工。\n")

	path := fmt.Sprintf("reviews/%02d_rewrite_brief.md", review.Chapter)
	if err := s.Drafts.SaveRewriteBrief(review.Chapter, b.String()); err != nil {
		return "", err
	}
	// The steer is now durably represented by the versioned brief. Leaving the
	// ephemeral copy pending would dispatch Editor again after every restart.
	if pendingSteer != "" {
		if err := s.RunMeta.ClearPendingSteer(); err != nil {
			return "", err
		}
	}
	return path, nil
}

func writeRewriteBriefItems(b *strings.Builder, items []string) {
	wrote := false
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			fmt.Fprintf(b, "- %s\n", item)
			wrote = true
		}
	}
	if !wrote {
		b.WriteString("- 无额外条目。\n")
	}
}

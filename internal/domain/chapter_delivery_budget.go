package domain

import "fmt"

const ChapterDeliveryBudgetPolicyV1 = "chapter-detail-to-accept-wall.v1"

// ChapterDeliveryBudgetV1 is an opt-in host execution limit, not story time,
// a model instruction, or permission to accept an incomplete chapter.
type ChapterDeliveryBudgetV1 struct {
	Policy       string `json:"policy"`
	LimitSeconds int    `json:"limit_seconds"`
}

func ValidateChapterDeliveryBudgetV1(budget *ChapterDeliveryBudgetV1) error {
	if budget == nil {
		return nil
	}
	if budget.Policy != ChapterDeliveryBudgetPolicyV1 || budget.LimitSeconds <= 0 || budget.LimitSeconds > 86400 {
		return fmt.Errorf("chapter delivery budget requires its explicit policy and 1..86400 seconds")
	}
	return nil
}

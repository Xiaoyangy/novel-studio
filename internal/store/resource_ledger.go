package store

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// ResourceLedgerStore 管理已入账资源与待确认提案。
type ResourceLedgerStore struct{ io *IO }

func NewResourceLedgerStore(io *IO) *ResourceLedgerStore { return &ResourceLedgerStore{io: io} }

const resourceLedgerPath = "meta/resource_ledger.json"

func (s *ResourceLedgerStore) Load() (*domain.ResourceLedger, error) {
	var ledger domain.ResourceLedger
	if err := s.io.ReadJSON(resourceLedgerPath, &ledger); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ledger, nil
}

func (s *ResourceLedgerStore) Save(ledger domain.ResourceLedger) error {
	if ledger.Version == 0 {
		ledger.Version = 1
	}
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked(resourceLedgerPath, ledger); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/resource_ledger.md", renderResourceLedger(ledger))
	})
}

// MergeClaims 分离写入已确认资源与待确认提案。booked 不会被 pending 覆盖成事实。
func (s *ResourceLedgerStore) MergeClaims(chapter int, booked, pending []domain.ResourceClaim) error {
	if len(booked) == 0 && len(pending) == 0 {
		return nil
	}
	return s.io.WithWriteLock(func() error {
		var ledger domain.ResourceLedger
		if err := s.io.ReadJSONUnlocked(resourceLedgerPath, &ledger); err != nil && !os.IsNotExist(err) {
			return err
		}
		if ledger.Version == 0 {
			ledger.Version = 1
		}
		now := time.Now().Format(time.RFC3339)
		for _, claim := range booked {
			claim.Status = "booked"
			claim.Chapter = chapter
			claim.UpdatedAt = now
			ledger.Claims = upsertResourceClaim(ledger.Claims, claim)
		}
		for _, claim := range pending {
			if claim.Status == "" || claim.Status == "booked" {
				claim.Status = "pending"
			}
			claim.Chapter = chapter
			claim.UpdatedAt = now
			ledger.Claims = upsertResourceClaim(ledger.Claims, claim)
		}
		if err := s.io.WriteJSONUnlocked(resourceLedgerPath, ledger); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/resource_ledger.md", renderResourceLedger(ledger))
	})
}

// ReplaceChapterClaims removes resource claims produced by one chapter, then
// writes the replacement booked/pending set. Rewrite commits use this to keep
// resource facts aligned with the rewritten chapter.
func (s *ResourceLedgerStore) ReplaceChapterClaims(chapter int, booked, pending []domain.ResourceClaim) error {
	return s.io.WithWriteLock(func() error {
		var ledger domain.ResourceLedger
		if err := s.io.ReadJSONUnlocked(resourceLedgerPath, &ledger); err != nil && !os.IsNotExist(err) {
			return err
		}
		if ledger.Version == 0 {
			ledger.Version = 1
		}
		filtered := ledger.Claims[:0]
		for _, claim := range ledger.Claims {
			if claim.Chapter != chapter {
				filtered = append(filtered, claim)
			}
		}
		ledger.Claims = filtered
		now := time.Now().Format(time.RFC3339)
		for _, claim := range booked {
			claim.Status = "booked"
			claim.Chapter = chapter
			claim.UpdatedAt = now
			ledger.Claims = upsertResourceClaim(ledger.Claims, claim)
		}
		for _, claim := range pending {
			if claim.Status == "" || claim.Status == "booked" {
				claim.Status = "pending"
			}
			claim.Chapter = chapter
			claim.UpdatedAt = now
			ledger.Claims = upsertResourceClaim(ledger.Claims, claim)
		}
		if err := s.io.WriteJSONUnlocked(resourceLedgerPath, ledger); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/resource_ledger.md", renderResourceLedger(ledger))
	})
}

func (s *ResourceLedgerStore) AuditForParticipants(participants []string) (domain.ResourceAudit, error) {
	ledger, err := s.Load()
	if err != nil || ledger == nil {
		return domain.ResourceAudit{}, err
	}
	set := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	audit := domain.ResourceAudit{Participants: participants}
	for _, claim := range ledger.Claims {
		if len(set) > 0 && !resourceClaimTouches(claim, set) {
			continue
		}
		switch claim.Status {
		case "pending":
			audit.Pending = append(audit.Pending, claim)
		case "rejected":
			continue
		default:
			audit.Booked = append(audit.Booked, claim)
		}
	}
	if len(audit.Pending) > 0 {
		audit.Warnings = append(audit.Warnings, "pending 中的资源只是待确认提案，正文不得写成已经拥有、已入账或已兑现")
	}
	return audit, nil
}

func (s *ResourceLedgerStore) AuditTextForPendingFacts(text string) ([]string, error) {
	ledger, err := s.Load()
	if err != nil || ledger == nil {
		return nil, err
	}
	var warnings []string
	for _, claim := range ledger.Claims {
		if claim.Status != "pending" || claim.Name == "" || !strings.Contains(text, claim.Name) {
			continue
		}
		if looksLikeConfirmedResource(text, claim.Name) {
			warnings = append(warnings, fmt.Sprintf("待确认资源 %q 在正文中疑似被写成既成事实", claim.Name))
		}
	}
	return warnings, nil
}

func upsertResourceClaim(claims []domain.ResourceClaim, next domain.ResourceClaim) []domain.ResourceClaim {
	key := resourceClaimKey(next)
	for i := range claims {
		if resourceClaimKey(claims[i]) == key {
			claims[i] = mergeResourceClaim(claims[i], next)
			return claims
		}
	}
	return append(claims, next)
}

func mergeResourceClaim(old, next domain.ResourceClaim) domain.ResourceClaim {
	if next.Name != "" {
		old.Name = next.Name
	}
	if next.Owner != "" {
		old.Owner = next.Owner
	}
	if next.Kind != "" {
		old.Kind = next.Kind
	}
	if next.Status != "" {
		old.Status = next.Status
	}
	if next.Risk != "" {
		old.Risk = next.Risk
	}
	if next.Evidence != "" {
		old.Evidence = next.Evidence
	}
	if next.Chapter > 0 {
		old.Chapter = next.Chapter
	}
	if len(next.Participants) > 0 {
		old.Participants = appendUniqueString(old.Participants, next.Participants...)
	}
	if next.UpdatedAt != "" {
		old.UpdatedAt = next.UpdatedAt
	}
	return old
}

func resourceClaimKey(c domain.ResourceClaim) string {
	if c.ID != "" {
		return c.ID
	}
	return c.Owner + "|" + c.Kind + "|" + c.Name
}

func resourceClaimTouches(c domain.ResourceClaim, set map[string]struct{}) bool {
	if _, ok := set[c.Owner]; ok {
		return true
	}
	for _, p := range c.Participants {
		if _, ok := set[p]; ok {
			return true
		}
	}
	return false
}

var confirmedResourceWords = []string{"已经", "已", "拿到", "拥有", "归入", "入账", "到账", "确认", "兑现", "成为", "到手"}

func looksLikeConfirmedResource(text, name string) bool {
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。！？!?\n；;", r)
	}) {
		if !strings.Contains(sentence, name) {
			continue
		}
		for _, word := range confirmedResourceWords {
			if strings.Contains(sentence, word) {
				return true
			}
		}
	}
	return false
}

func renderResourceLedger(ledger domain.ResourceLedger) string {
	var b strings.Builder
	b.WriteString("# 资源账本\n\n")
	for _, status := range []string{"booked", "pending", "spent"} {
		var rows []domain.ResourceClaim
		for _, claim := range ledger.Claims {
			if claim.Status == status {
				rows = append(rows, claim)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", status)
		slices.SortFunc(rows, func(a, b domain.ResourceClaim) int {
			return strings.Compare(a.Name, b.Name)
		})
		for _, claim := range rows {
			fmt.Fprintf(&b, "- **%s**", claim.Name)
			if claim.Owner != "" {
				fmt.Fprintf(&b, "（%s）", claim.Owner)
			}
			if claim.Kind != "" {
				fmt.Fprintf(&b, " [%s]", claim.Kind)
			}
			if claim.Evidence != "" {
				fmt.Fprintf(&b, "：%s", claim.Evidence)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

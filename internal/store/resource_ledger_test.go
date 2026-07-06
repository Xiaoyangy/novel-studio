package store

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestResourceLedgerSeparatesBookedAndPending(t *testing.T) {
	s := newTestStore(t)
	err := s.ResourceLedger.MergeClaims(3,
		[]domain.ResourceClaim{{ID: "cash", Name: "鬼币一百枚", Owner: "江烬", Participants: []string{"江烬"}}},
		[]domain.ResourceClaim{{ID: "shop", Name: "便利店经营权", Owner: "江烬", Risk: "契约未签", Participants: []string{"江烬", "温梨"}}},
	)
	if err != nil {
		t.Fatalf("MergeClaims: %v", err)
	}

	audit, err := s.ResourceLedger.AuditForParticipants([]string{"江烬"})
	if err != nil {
		t.Fatalf("AuditForParticipants: %v", err)
	}
	if len(audit.Booked) != 1 || audit.Booked[0].Name != "鬼币一百枚" {
		t.Fatalf("booked audit wrong: %+v", audit.Booked)
	}
	if len(audit.Pending) != 1 || audit.Pending[0].Name != "便利店经营权" {
		t.Fatalf("pending audit wrong: %+v", audit.Pending)
	}
	if len(audit.Warnings) == 0 {
		t.Fatal("expected pending warning")
	}
}

func TestResourceLedgerFlagsPendingAsFact(t *testing.T) {
	s := newTestStore(t)
	if err := s.ResourceLedger.MergeClaims(3, nil,
		[]domain.ResourceClaim{{ID: "shop", Name: "便利店经营权", Owner: "江烬"}},
	); err != nil {
		t.Fatalf("MergeClaims: %v", err)
	}
	warnings, err := s.ResourceLedger.AuditTextForPendingFacts("江烬已经拿到便利店经营权，账本也归他管。")
	if err != nil {
		t.Fatalf("AuditTextForPendingFacts: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "便利店经营权") {
		t.Fatalf("expected pending warning, got %v", warnings)
	}
}

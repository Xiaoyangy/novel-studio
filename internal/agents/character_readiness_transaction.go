package agents

import (
	"context"
	"fmt"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The commit captures only the result of this tool invocation, never reusable
// source authority. Store reauthenticates the source chain after the model.
type characterReadinessCommit struct {
	store    *store.Store
	expected string
	mu       sync.Mutex
	audit    *domain.CharacterReadinessReviewAudit
	session  *domain.CharacterActivationSession
}

func (c *characterReadinessCommit) Save(audit domain.CharacterReadinessReviewAudit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, err := c.store.CommitVerifiedCharacterReadinessReview(c.expected, audit)
	if err != nil {
		return err
	}
	c.audit, c.session = &audit, session
	return nil
}

func (c *characterReadinessCommit) Result() (*domain.CharacterReadinessReviewAudit, *domain.CharacterActivationSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.audit, c.session
}

// One verified read before the model and one verified commit afterwards. The
// legacy runner remains available for historical standalone callers; production
// verified cycles use this path so the controller does not replay them again.
func runVerifiedCharacterChapterReadiness(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, session domain.CharacterActivationSession, beforeDispatch ...func(context.Context, string) error) (domain.CharacterChapterReadiness, *domain.CharacterActivationSession, error) {
	var empty domain.CharacterChapterReadiness
	if err := ctx.Err(); err != nil {
		return empty, nil, err
	}
	if st == nil || models == nil || session.Phase != "assessing" {
		return empty, nil, fmt.Errorf("transactional readiness requires its pending verified session")
	}
	snapshot, err := models.SnapshotForRole("writer")
	if err != nil {
		return empty, nil, err
	}
	thinking, _ := ResolveThinkingForModel(snapshot.Model, roleThinking(cfg, "writer"))
	protocol, err := characterReadinessReviewProtocol(snapshot, thinking, true)
	if err != nil {
		return empty, nil, err
	}
	input, cached, err := st.PrepareVerifiedCharacterReadinessReview(session, protocol)
	if err != nil {
		return empty, nil, err
	}
	commit := &characterReadinessCommit{store: st, expected: session.Digest}
	if cached != nil {
		// A paid audit saved before a crash still completes mechanically.
		if err := commit.Save(*cached); err != nil {
			return empty, nil, err
		}
		_, applied := commit.Result()
		return cached.Receipt, applied, nil
	}
	for _, before := range beforeDispatch {
		if before != nil {
			if err := before(ctx, "chapter_readiness"); err != nil {
				return empty, nil, err
			}
		}
	}
	inputDigest, err := domain.CharacterReadinessReviewInputDigest(input)
	if err != nil {
		return empty, nil, err
	}
	readiness, err := runCharacterReadinessInput(ctx, cfg, st, snapshot, thinking, protocol, inputDigest, input, true, commit)
	if err != nil {
		return empty, nil, err
	}
	audit, applied := commit.Result()
	if audit == nil || applied == nil || audit.Receipt.Digest != readiness.Digest {
		return empty, nil, fmt.Errorf("readiness lacks its exact committed result")
	}
	return readiness, applied, nil
}

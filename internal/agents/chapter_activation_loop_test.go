package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationLoopFixture(t *testing.T, max int) (*store.Store, domain.CharacterActivationSession, ChapterActivationDriver, *int, *int) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	first := testutil.CharacterCycle(t, 1, "", nil, 0)
	baseline, err := domain.NewCharacterActivationSession(first.GenerationID, first.Chapter, first.ChapterContextDigest, *first.Evidence.Stimulus.PhysicalState, 0, max)
	if err != nil {
		t.Fatal(err)
	}
	cycles, assessments := 0, 0
	driver := ChapterActivationDriver{ExecuteCycle: func(_ context.Context, current domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
		cycles++
		if len(current.CycleDigests) == 0 {
			return first, nil
		}
		previous, err := st.LoadCharacterActivationCycle(current.GenerationID, current.Chapter, len(current.CycleDigests))
		if err != nil {
			return domain.CharacterActivationCycle{}, err
		}
		final := previous.Evidence.Arbitrations[len(previous.Evidence.Arbitrations)-1]
		after, err := domain.ApplyArbitrationPhysicalStateV2(final, previous.Evidence.Stimulus, domain.LatestCharacterCycleProposals(previous.Evidence)...)
		if err != nil {
			return domain.CharacterActivationCycle{}, err
		}
		return testutil.CharacterCycle(t, len(current.CycleDigests)+1, previous.Digest, &after, current.CurrentDay), nil
	}, AssessCycle: func(_ context.Context, current domain.CharacterActivationSession, cycle domain.CharacterActivationCycle) (domain.CharacterChapterReadiness, error) {
		assessments++
		decision := "continue"
		if len(current.CycleDigests) >= 3 {
			decision = "ready_for_plan"
		}
		return testutil.CycleReadiness(t, cycle, decision), nil
	}}
	return st, baseline, driver, &cycles, &assessments
}

func TestChapterActivationLoopRunsSeveralCyclesWithoutCreatingFakeChapters(t *testing.T) {
	st, baseline, driver, cycles, assessments := activationLoopFixture(t, 4)
	result, err := RunChapterActivationLoop(context.Background(), st, baseline, driver)
	if err != nil {
		t.Fatal(err)
	}
	if *cycles != 3 || *assessments != 3 || result.Chapter != 1 || result.Phase != "ready" {
		t.Fatalf("unexpected run: %d/%d %+v", *cycles, *assessments, result)
	}
	if _, err := RunChapterActivationLoop(context.Background(), store.NewStore(st.Dir()), baseline, driver); err != nil {
		t.Fatal(err)
	}
	if *cycles != 3 || *assessments != 3 {
		t.Fatal("completed session was re-billed")
	}
}

func TestChapterActivationLoopResumesOnlyMissingAssessmentAfterCancel(t *testing.T) {
	st, baseline, driver, cycles, assessments := activationLoopFixture(t, 4)
	ctx, cancel := context.WithCancel(context.Background())
	original := driver.ExecuteCycle
	driver.ExecuteCycle = func(ctx context.Context, s domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
		cycle, err := original(ctx, s)
		cancel()
		return cycle, err
	}
	result, err := RunChapterActivationLoop(ctx, st, baseline, driver)
	if !errors.Is(err, context.Canceled) || result == nil || result.Phase != "assessing" || *cycles != 1 || *assessments != 0 {
		t.Fatalf("paid result/cancel boundary failed: %v %+v %d/%d", err, result, *cycles, *assessments)
	}
	driver.ExecuteCycle = original
	if _, err := RunChapterActivationLoop(context.Background(), st, baseline, driver); err != nil {
		t.Fatal(err)
	}
	if *cycles != 3 || *assessments != 3 {
		t.Fatalf("resume repeated a paid cycle: %d/%d", *cycles, *assessments)
	}
}

func TestChapterActivationLoopLimitsAndBudgetNeverMeanCompletion(t *testing.T) {
	st, baseline, driver, cycles, _ := activationLoopFixture(t, 2)
	result, err := RunChapterActivationLoop(context.Background(), st, baseline, driver)
	var limit *ChapterActivationLimitError
	if !errors.As(err, &limit) || *cycles != 2 || result.Phase != "collecting" {
		t.Fatalf("cycle limit hidden as ready: %v %+v", err, result)
	}
	other, otherBaseline, blocked, calls, _ := activationLoopFixture(t, 4)
	budgetErr := errors.New("configured budget exceeded")
	blocked.BeforeDispatch = func(context.Context, string) error { return budgetErr }
	if _, err := RunChapterActivationLoop(context.Background(), other, otherBaseline, blocked); !errors.Is(err, budgetErr) || *calls != 0 {
		t.Fatal("budget was checked after model dispatch")
	}
}

func TestChapterActivationLoopConcurrentRunnerDoesNotDuplicateDispatch(t *testing.T) {
	st, baseline, driver, cycles, assessments := activationLoopFixture(t, 4)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := driver.ExecuteCycle
	driver.ExecuteCycle = func(ctx context.Context, s domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
		cycle, err := original(ctx, s)
		close(started)
		<-release
		cancel()
		return cycle, err
	}
	go func() { _, err := RunChapterActivationLoop(ctx, st, baseline, driver); done <- err }()
	<-started
	if _, err := RunChapterActivationLoop(context.Background(), store.NewStore(st.Dir()), baseline, driver); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("concurrent runner reached dispatch: %v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if *cycles != 1 || *assessments != 0 {
		t.Fatalf("duplicate paid work: %d/%d", *cycles, *assessments)
	}
}

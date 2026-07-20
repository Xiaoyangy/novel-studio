package main

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

type recordingPipelineWatchdog struct {
	mu sync.Mutex

	startErr    error
	stopErr     error
	progressErr error
	starts      int
	stops       int
	events      []string
	bodyEvents  []recordingPipelineWatchdogBodyEvent
}

type recordingPipelineWatchdogBodyEvent struct {
	EventCode string
	BodySHA   string
}

type recordingPipelineWatchdogPauser struct {
	*recordingPipelineWatchdog

	mu        sync.Mutex
	pauseErr  error
	resumeErr error
	pauses    int
	resumes   int
}

func (w *recordingPipelineWatchdogPauser) Pause() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pauses++
	return w.pauseErr
}

func (w *recordingPipelineWatchdogPauser) Resume() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.resumes++
	return w.resumeErr
}

func (w *recordingPipelineWatchdogPauser) pauseSnapshot() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pauses, w.resumes
}

func (w *recordingPipelineWatchdog) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.starts++
	return w.startErr
}

func (w *recordingPipelineWatchdog) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stops++
	return w.stopErr
}

func (w *recordingPipelineWatchdog) Progress(eventCode string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, eventCode)
	return w.progressErr
}

func (w *recordingPipelineWatchdog) ProgressBody(eventCode, bodySHA string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bodyEvents = append(w.bodyEvents, recordingPipelineWatchdogBodyEvent{
		EventCode: eventCode,
		BodySHA:   bodySHA,
	})
	return w.progressErr
}

func (w *recordingPipelineWatchdog) snapshot() (int, int, []string, []recordingPipelineWatchdogBodyEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.starts,
		w.stops,
		append([]string(nil), w.events...),
		append([]recordingPipelineWatchdogBodyEvent(nil), w.bodyEvents...)
}

func TestWithPipelineStageWatchdogLifecycle(t *testing.T) {
	t.Run("success stops and unbinds", func(t *testing.T) {
		handle := &recordingPipelineWatchdog{}
		factory := func(pipelineWatchdogConfig) (pipelineWatchdogHandle, error) {
			return handle, nil
		}
		bodySHA := "sha256:" + strings.Repeat("a", 64)
		err := withPipelineStageWatchdogUsing(
			pipelineWatchdogConfig{Stage: "render"},
			factory,
			func() error {
				return pipelineWatchdogProgressBody(
					pipelineWatchdogEventRenderBodyCommitted,
					bodySHA,
				)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		// The bridge must be cleared before the stage returns; a late event is a
		// no-op and cannot race a stopped watchdog.
		if err := pipelineWatchdogProgress("late_event"); err != nil {
			t.Fatal(err)
		}
		starts, stops, events, bodyEvents := handle.snapshot()
		if starts != 1 || stops != 1 || len(events) != 0 || len(bodyEvents) != 1 ||
			bodyEvents[0].EventCode != pipelineWatchdogEventRenderBodyCommitted ||
			bodyEvents[0].BodySHA != bodySHA {
			t.Fatalf(
				"unexpected lifecycle: starts=%d stops=%d events=%v body_events=%v",
				starts,
				stops,
				events,
				bodyEvents,
			)
		}
	})

	t.Run("work and stop errors are both retained", func(t *testing.T) {
		primaryErr := errors.New("stage failed")
		stopErr := errors.New("watchdog stop failed")
		handle := &recordingPipelineWatchdog{stopErr: stopErr}
		err := withPipelineStageWatchdogUsing(
			pipelineWatchdogConfig{Stage: "render"},
			func(pipelineWatchdogConfig) (pipelineWatchdogHandle, error) {
				return handle, nil
			},
			func() error { return primaryErr },
		)
		if !errors.Is(err, primaryErr) || !errors.Is(err, stopErr) {
			t.Fatalf("primary or stop error was lost: %v", err)
		}
		starts, stops, _, _ := handle.snapshot()
		if starts != 1 || stops != 1 {
			t.Fatalf("unexpected lifecycle: starts=%d stops=%d", starts, stops)
		}
	})

	t.Run("start error still stops and does not dispatch work", func(t *testing.T) {
		startErr := errors.New("watchdog start failed")
		stopErr := errors.New("watchdog cleanup failed")
		handle := &recordingPipelineWatchdog{startErr: startErr, stopErr: stopErr}
		workCalled := false
		err := withPipelineStageWatchdogUsing(
			pipelineWatchdogConfig{Stage: "render"},
			func(pipelineWatchdogConfig) (pipelineWatchdogHandle, error) {
				return handle, nil
			},
			func() error {
				workCalled = true
				return nil
			},
		)
		if !errors.Is(err, startErr) || !errors.Is(err, stopErr) {
			t.Fatalf("start or cleanup error was lost: %v", err)
		}
		starts, stops, _, _ := handle.snapshot()
		if workCalled || starts != 1 || stops != 1 {
			t.Fatalf("unexpected start failure lifecycle: work=%v starts=%d stops=%d", workCalled, starts, stops)
		}
	})
}

func TestWithPipelineWatchdogPausedPreservesWorkAndResumeErrors(t *testing.T) {
	workErr := errors.New("directory publish failed")
	resumeErr := errors.New("watchdog resume failed")
	handle := &recordingPipelineWatchdogPauser{
		recordingPipelineWatchdog: &recordingPipelineWatchdog{},
		resumeErr:                 resumeErr,
	}
	release, err := bindCurrentPipelineWatchdog(handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	err = withPipelineWatchdogPaused(func() error { return workErr })
	if !errors.Is(err, workErr) || !errors.Is(err, resumeErr) {
		t.Fatalf("work or resume error was lost: %v", err)
	}
	pauses, resumes := handle.pauseSnapshot()
	if pauses != 1 || resumes != 1 {
		t.Fatalf("pause lifecycle pauses=%d resumes=%d, want 1/1", pauses, resumes)
	}
}

func TestPipelineWatchdogBridgeConcurrentProgress(t *testing.T) {
	handle := &recordingPipelineWatchdog{}
	release, err := bindCurrentPipelineWatchdog(handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	const goroutines = 32
	const eventsPerGoroutine = 16
	bodySHA := "sha256:" + strings.Repeat("b", 64)
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				if progressErr := pipelineWatchdogProgressBody(
					pipelineWatchdogEventRenderBodyCommitted,
					bodySHA,
				); progressErr != nil {
					errCh <- progressErr
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for progressErr := range errCh {
		t.Fatal(progressErr)
	}
	_, _, _, bodyEvents := handle.snapshot()
	if got, want := len(bodyEvents), goroutines*eventsPerGoroutine; got != want {
		t.Fatalf("concurrent progress lost events: got=%d want=%d", got, want)
	}
}

func TestSealedRenderDurableReviewProgress(t *testing.T) {
	handle := &recordingPipelineWatchdog{}
	release, err := bindCurrentPipelineWatchdog(handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	bodySHA := "sha256:" + strings.Repeat("c", 64)
	snapshot := &pipelineRenderedChapterSnapshot{
		Store:      store.NewStore(t.TempDir()),
		Body:       "candidate body",
		BodySHA256: bodySHA,
	}
	formalReviewCalled := false
	err = runPipelineSealedFormalReviewAfterBodyEvidence(
		t.TempDir(),
		snapshot,
		func(*pipelineRenderedChapterSnapshot) (pipelineSealedActualDeltaMatch, error) {
			return pipelineSealedActualDeltaMatch{ProjectionMatch: true}, nil
		},
		func() error {
			formalReviewCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !formalReviewCalled {
		t.Fatal("formal review was not dispatched after deterministic preflight")
	}
	_, _, _, bodyEvents := handle.snapshot()
	if len(bodyEvents) != 2 ||
		bodyEvents[0] != (recordingPipelineWatchdogBodyEvent{
			EventCode: pipelineWatchdogEventRenderPreflightPassed,
			BodySHA:   bodySHA,
		}) ||
		bodyEvents[1] != (recordingPipelineWatchdogBodyEvent{
			EventCode: pipelineWatchdogEventRenderFormalReviewed,
			BodySHA:   bodySHA,
		}) {
		t.Fatalf("durable progress order/identity drifted: %+v", bodyEvents)
	}
}

func TestSavePipelineSealedActualMatchReportsProjectionMatch(t *testing.T) {
	handle := &recordingPipelineWatchdog{}
	release, err := bindCurrentPipelineWatchdog(handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	if err := savePipelineSealedActualMatch(
		t.TempDir(),
		pipelineSealedActualDeltaMatch{ProjectionMatch: true},
	); err != nil {
		t.Fatal(err)
	}
	_, _, events, _ := handle.snapshot()
	if len(events) != 1 || events[0] != pipelineWatchdogEventRenderActualMatched {
		t.Fatalf("actual-match durable progress missing: %v", events)
	}
}

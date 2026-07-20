package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineHostTurnDispatchBudgetBlocksFourthBeforeProvider(t *testing.T) {
	liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
	providerCalls := 0
	hooks := pipelineRenderHostTurnDispatchHooks{
		Needed: func(*store.Store, int) (bool, int64, error) {
			return true, 0, nil
		},
		Reserve: reservePipelineWholeBodyDispatch,
		Arm: func(outputDir string, reservation *pipelineRenderDispatchReservation) error {
			return store.NewStore(outputDir).Runtime.ArmPipelineRenderProsePermit(reservation.AuthorizationDigest, reservation.Attempt)
		},
		Finish: finishPipelineWholeBodyDispatchFromCandidate,
		Clear: func(outputDir, authorization string) error {
			return store.NewStore(outputDir).Runtime.ClearPipelineRenderProsePermit(authorization)
		},
	}
	for attempt := 1; attempt <= pipelineRenderWholeBodyDispatchLimit; attempt++ {
		err := runPipelineHostTurnWithDispatchBudgetUsing(
			true,
			candidateOutputDir,
			fmt.Sprintf("provider-invocation-%d", attempt),
			manifest.Chapter,
			1,
			func() error {
				if err := store.NewStore(candidateOutputDir).Runtime.ConsumePipelineRenderProsePermit(manifest.Chapter); err != nil {
					return err
				}
				providerCalls++
				return nil
			},
			hooks,
		)
		if err != nil {
			t.Fatalf("dispatch %d: %v", attempt, err)
		}
	}
	err := runPipelineHostTurnWithDispatchBudgetUsing(
		true,
		candidateOutputDir,
		"provider-invocation-4",
		manifest.Chapter,
		1,
		func() error {
			if err := store.NewStore(candidateOutputDir).Runtime.ConsumePipelineRenderProsePermit(manifest.Chapter); err != nil {
				return err
			}
			providerCalls++
			return nil
		},
		hooks,
	)
	var exhausted *pipelineRenderDispatchBudgetExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("fourth dispatch error=%T %v, want budget exhaustion", err, err)
	}
	if providerCalls != pipelineRenderWholeBodyDispatchLimit {
		t.Fatalf("provider calls=%d want=%d; fourth call crossed circuit breaker", providerCalls, pipelineRenderWholeBodyDispatchLimit)
	}
	ledger, err := loadPipelineRenderDispatchLedger(liveOutputDir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Reservations) != pipelineRenderWholeBodyDispatchLimit {
		t.Fatalf("reservations=%d want=%d", len(ledger.Reservations), pipelineRenderWholeBodyDispatchLimit)
	}
}

func TestPipelineHostTurnNonProseModesDoNotReserveDispatch(t *testing.T) {
	for _, mode := range []string{"finalizer", "cache", "review-only"} {
		t.Run(mode, func(t *testing.T) {
			classified := 0
			providerCalls := 0
			reserveCalls := 0
			finishCalls := 0
			err := runPipelineHostTurnWithDispatchBudgetUsing(
				true,
				t.TempDir(),
				"non-prose-invocation",
				1,
				1,
				func() error {
					providerCalls++
					return nil
				},
				pipelineRenderHostTurnDispatchHooks{
					Needed: func(*store.Store, int) (bool, int64, error) {
						classified++
						return false, 41, nil
					},
					Reserve: func(string, string, int) (*pipelineRenderDispatchReservation, bool, error) {
						reserveCalls++
						return nil, false, nil
					},
					Arm: func(string, *pipelineRenderDispatchReservation) error { return nil },
					Finish: func(string, int, int64, *pipelineRenderDispatchReservation, error) error {
						finishCalls++
						return nil
					},
					Clear: func(string, string) error { return nil },
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if classified != 1 || providerCalls != 1 || reserveCalls != 0 || finishCalls != 0 {
				t.Fatalf(
					"classified=%d provider=%d reserve=%d finish=%d",
					classified,
					providerCalls,
					reserveCalls,
					finishCalls,
				)
			}
		})
	}
}

func TestPipelineHostTurnCrashReservationSurvivesRestart(t *testing.T) {
	liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
	crashed, reused, err := reservePipelineWholeBodyDispatch(
		candidateOutputDir,
		"crashed-process",
		1,
	)
	if err != nil || reused || crashed == nil {
		t.Fatalf("crash reservation=%+v reused=%v err=%v", crashed, reused, err)
	}
	// No finish call: this is the durable state left when a process exits after
	// reservation but before Host/provider returns.
	providerCalls := 0
	err = runPipelineHostTurnWithDispatchBudgetUsing(
		true,
		candidateOutputDir,
		"restarted-process",
		manifest.Chapter,
		1,
		func() error {
			if err := store.NewStore(candidateOutputDir).Runtime.ConsumePipelineRenderProsePermit(manifest.Chapter); err != nil {
				return err
			}
			providerCalls++
			return nil
		},
		pipelineRenderHostTurnDispatchHooks{
			Needed: func(*store.Store, int) (bool, int64, error) {
				return true, 0, nil
			},
			Reserve: reservePipelineWholeBodyDispatch,
			Arm: func(outputDir string, reservation *pipelineRenderDispatchReservation) error {
				return store.NewStore(outputDir).Runtime.ArmPipelineRenderProsePermit(reservation.AuthorizationDigest, reservation.Attempt)
			},
			Finish: finishPipelineWholeBodyDispatchFromCandidate,
			Clear: func(outputDir, authorization string) error {
				return store.NewStore(outputDir).Runtime.ClearPipelineRenderProsePermit(authorization)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if providerCalls != 1 {
		t.Fatalf("restart provider calls=%d want=1", providerCalls)
	}
	ledger, err := loadPipelineRenderDispatchLedger(liveOutputDir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	reservations := pipelineRenderDispatchReservations(ledger)
	if len(reservations) != 2 ||
		reservations[0].AuthorizationDigest != crashed.AuthorizationDigest ||
		reservations[0].FinishedAt != "" ||
		reservations[1].FinishedAt == "" {
		t.Fatalf("crash/restart reservation history was not durable: %+v", reservations)
	}
}

func TestPipelineHostTurnFinishErrorJoinsHostError(t *testing.T) {
	hostErr := errors.New("provider failed")
	finishErr := errors.New("finish ledger failed")
	reservation := &pipelineRenderDispatchReservation{
		AuthorizationDigest: "sha256:" + strings.Repeat("f", 64),
		Attempt:             1,
	}
	err := runPipelineHostTurnWithDispatchBudgetUsing(
		true,
		t.TempDir(),
		"join-errors",
		1,
		1,
		func() error { return hostErr },
		pipelineRenderHostTurnDispatchHooks{
			Needed: func(*store.Store, int) (bool, int64, error) {
				return true, 17, nil
			},
			Reserve: func(string, string, int) (*pipelineRenderDispatchReservation, bool, error) {
				return reservation, false, nil
			},
			Arm: func(string, *pipelineRenderDispatchReservation) error { return nil },
			Finish: func(_ string, _ int, baseline int64, got *pipelineRenderDispatchReservation, gotHostErr error) error {
				if baseline != 17 || got != reservation || !errors.Is(gotHostErr, hostErr) {
					t.Fatalf("finish evidence baseline=%d reservation=%+v hostErr=%v", baseline, got, gotHostErr)
				}
				return finishErr
			},
			Clear: func(string, string) error { return nil },
		},
	)
	if !errors.Is(err, hostErr) || !errors.Is(err, finishErr) {
		t.Fatalf("host or finish error was lost: %v", err)
	}
}

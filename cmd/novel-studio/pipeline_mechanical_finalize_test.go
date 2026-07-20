package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineTryMechanicalSealedFinalizeWithRoutesOnlyCommittedResult(t *testing.T) {
	tests := []struct {
		name        string
		result      tools.SealedMechanicalFinalizeResult
		finalizeErr error
		wantHandled bool
		wantErr     string
	}{
		{
			name: "not applicable falls back",
			result: tools.SealedMechanicalFinalizeResult{
				Disposition: tools.SealedMechanicalFinalizeNotApplicable,
				Reason:      "no current draft",
			},
		},
		{
			name: "needs agent falls back",
			result: tools.SealedMechanicalFinalizeResult{
				Disposition: tools.SealedMechanicalFinalizeNeedsAgent,
				Reason:      "consistency edit required",
			},
		},
		{
			name: "committed skips headless",
			result: tools.SealedMechanicalFinalizeResult{
				Disposition: tools.SealedMechanicalFinalizeCommitted,
			},
			wantHandled: true,
		},
		{
			name:        "execution error fails closed",
			finalizeErr: errors.New("commit saga interrupted"),
			wantErr:     "失败关闭",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			called := 0
			handled, err := pipelineTryMechanicalSealedFinalizeWith(
				context.Background(),
				st,
				pipelineFlags{RenderOnly: true, StopAfterCommit: 3},
				2,
				func(_ context.Context, _ *store.Store, chapter int) (tools.SealedMechanicalFinalizeResult, error) {
					called++
					if chapter != 3 {
						t.Fatalf("finalizer chapter=%d, want 3", chapter)
					}
					result := tt.result
					result.Chapter = chapter
					return result, tt.finalizeErr
				},
			)
			if called != 1 {
				t.Fatalf("finalizer calls=%d, want 1", called)
			}
			if handled != tt.wantHandled {
				t.Fatalf("handled=%t, want %t", handled, tt.wantHandled)
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error=%v, want substring %q", err, tt.wantErr)
			}
			raw, readErr := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(pipelineTimingLogPath)))
			if readErr != nil || !strings.Contains(string(raw), `"stage":"mechanical_finalize"`) ||
				!strings.Contains(string(raw), `"chapter":3`) ||
				!strings.Contains(string(raw), `"attempt":2`) {
				t.Fatalf("mechanical timing was not persisted: raw=%s err=%v", raw, readErr)
			}
		})
	}
}

func TestPipelineTryMechanicalSealedFinalizeWithDoesNotTouchOrdinaryRoute(t *testing.T) {
	called := false
	handled, err := pipelineTryMechanicalSealedFinalizeWith(
		context.Background(),
		store.NewStore(t.TempDir()),
		pipelineFlags{RenderOnly: false, StopAfterCommit: 3},
		1,
		func(context.Context, *store.Store, int) (tools.SealedMechanicalFinalizeResult, error) {
			called = true
			return tools.SealedMechanicalFinalizeResult{}, nil
		},
	)
	if err != nil || handled || called {
		t.Fatalf("ordinary route changed: handled=%t called=%t err=%v", handled, called, err)
	}
}

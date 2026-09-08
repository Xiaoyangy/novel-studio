package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestInspectPipelineExecutionNeverCreatesGuardOrCleansStaleLease(t *testing.T) {
	st := NewStore(t.TempDir())
	if lock, err := st.Runtime.InspectPipelineExecution(); err != nil || lock != nil {
		t.Fatalf("absent lease: lock=%+v err=%v", lock, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), pipelineExecutionGuardPath)); !os.IsNotExist(err) {
		t.Fatalf("read-only lease inspection created a transaction guard: %v", err)
	}
	lease := domain.PipelineExecutionLock{Version: pipelineExecutionVersion, Mode: domain.PipelineExecutionProjectAll,
		TargetChapter: 1, Owner: "inspection-test", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := st.Runtime.io.WriteJSON(pipelineExecutionPath, lease); err != nil {
		t.Fatal(err)
	}
	before, err := st.Runtime.io.ReadFile(pipelineExecutionPath)
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := st.Runtime.InspectPipelineExecution(); err != nil || lock != nil {
		t.Fatalf("expired lease: lock=%+v err=%v", lock, err)
	}
	after, err := st.Runtime.io.ReadFile(pipelineExecutionPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only inspection cleaned or changed the expired lease: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), pipelineExecutionGuardPath)); !os.IsNotExist(err) {
		t.Fatalf("read-only lease inspection created a transaction guard: %v", err)
	}
}

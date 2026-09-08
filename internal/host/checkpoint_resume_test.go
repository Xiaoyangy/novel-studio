package host

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPreparedStageResumePreservesDurableCheckpoints(t *testing.T) {
	st := store.NewStore(t.TempDir())
	checkpoint, err := st.Checkpoints.Append(domain.GlobalScope(), "premise", "premise.md", "sha256:accepted-premise")
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st, preserveCheckpointsOnStart: true}
	if err := h.resetStartRuntimeState(); err != nil {
		t.Fatal(err)
	}
	reopened := store.NewStore(st.Dir())
	got, err := reopened.Checkpoints.AllStrict()
	if err != nil || len(got) != 1 || got[0].Seq != checkpoint.Seq || got[0].Digest != checkpoint.Digest {
		t.Fatalf("resuming a prepared stage lost saved foundation evidence: %+v, %v", got, err)
	}
}

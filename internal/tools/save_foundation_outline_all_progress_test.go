package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSaveFoundationPlanStructurePreservesFrozenProgress(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	receipt := authorizeOutlineAllForTest(t, st)
	receipt.PendingAction = &domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: receipt.PendingAction.BeforeLayeredDigest}
	var err error
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "meta", "progress.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	before["phase"] = json.RawMessage(`"init"`)
	before["future_state"] = json.RawMessage(`{"keep":true}`)
	raw, _ = json.Marshal(before)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	volumes := make([]domain.VolumeOutline, 4)
	for i := range volumes {
		volumes[i] = domain.VolumeOutline{Index: i + 1, Title: fmt.Sprintf("Volume %d", i+1), Theme: "resolve the public obligation", Arcs: []domain.ArcOutline{{Index: 1, Title: "A bounded decision", Goal: "verify the obligation", EstimatedChapters: 20}}}
	}
	args, err := json.Marshal(map[string]any{"type": "plan_structure", "content": volumes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if string(after["total_chapters"]) != "80" {
		t.Fatalf("incorrect reservation total: %s", after["total_chapters"])
	}
	delete(before, "total_chapters")
	delete(after, "total_chapters")
	// Compare JSON values because persisted formatting is intentionally free.
	var beforeValues, afterValues any
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_ = json.Unmarshal(b, &beforeValues)
	_ = json.Unmarshal(a, &afterValues)
	if !reflect.DeepEqual(beforeValues, afterValues) {
		t.Fatalf("plan_structure modified frozen progress: before=%s after=%s", b, a)
	}
}

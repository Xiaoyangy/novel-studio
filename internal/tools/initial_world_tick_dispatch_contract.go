package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	// InitialWorldTickDispatchContractVersion is part of the content-addressed
	// identity. Changing the protocol therefore invalidates every older task
	// even when the chapter-one prose happens to stay unchanged.
	InitialWorldTickDispatchContractVersion = "initial-world-tick-dispatch/v1"

	// InitialWorldTickNoPreemptToken is the machine-checkable policy anchor that
	// must survive the Coordinator -> architect_long hand-off verbatim.
	InitialWorldTickNoPreemptToken = "NO_PREEMPT_CHAPTER_1"

	initialWorldTickDispatchMarkerPrefix = "initial_world_tick_dispatch:sha256:"
)

// InitialWorldTickDispatchContract pins an initial world-tick task to the
// authoritative chapter-one boundary. CoreEvent and Hook deliberately retain
// the exact stored strings: trimming or paraphrasing either one changes the
// marker and cannot silently reuse an older dispatch task.
type InitialWorldTickDispatchContract struct {
	Marker    string
	CoreEvent string
	Hook      string
	Block     string
}

// BuildInitialWorldTickDispatchContract reads chapter one from the authored
// layered outline when one exists. A present layered outline is authoritative:
// an unreadable, skeleton-only, or chapter-one-incomplete layered artifact may
// not fall back to a potentially stale flat compatibility outline.
func BuildInitialWorldTickDispatchContract(st *store.Store) (InitialWorldTickDispatchContract, error) {
	if st == nil || st.Outline == nil {
		return InitialWorldTickDispatchContract{}, fmt.Errorf("initial world tick dispatch contract: missing project store")
	}

	entry, source, err := initialWorldTickChapterOne(st)
	if err != nil {
		return InitialWorldTickDispatchContract{}, err
	}
	if strings.TrimSpace(entry.CoreEvent) == "" {
		return InitialWorldTickDispatchContract{}, fmt.Errorf("initial world tick dispatch contract: %s chapter 1 core_event is empty", source)
	}
	if strings.TrimSpace(entry.Hook) == "" {
		return InitialWorldTickDispatchContract{}, fmt.Errorf("initial world tick dispatch contract: %s chapter 1 hook is empty", source)
	}

	marker, err := initialWorldTickDispatchMarker(entry.CoreEvent, entry.Hook)
	if err != nil {
		return InitialWorldTickDispatchContract{}, err
	}
	contract := InitialWorldTickDispatchContract{
		Marker:    marker,
		CoreEvent: entry.CoreEvent,
		Hook:      entry.Hook,
	}
	contract.Block = fmt.Sprintf(`<initial_world_tick_dispatch_contract version=%q>
marker: %s
core_event_exact:
%s
hook_exact:
%s
%s
- chapter0 只设置第1章实际发生所需的条件；不得提前完成、替换、预答，也不得换演员执行第1章发现、选择、动作和结果。
- 不得新增 exact core_event / exact hook 未授权的证据物、转交、证言、直接接触或场景结果。
- 第1章状态转换必须保持 pending；visibility_path 必须以第1章实际发生为条件，不得声称 chapter0 已完成该转换。
- 无需把全部角色塞入 tick；只保留建立条件所必需且不越过第1章边界的角色和事件。
</initial_world_tick_dispatch_contract>`,
		InitialWorldTickDispatchContractVersion,
		contract.Marker,
		contract.CoreEvent,
		contract.Hook,
		InitialWorldTickNoPreemptToken,
	)
	return contract, nil
}

// ValidateInitialWorldTickDispatchTask rejects a Coordinator task that loses
// any exact chapter-one anchor during delegation. It also verifies the supplied
// contract before inspecting the task so a caller cannot bless a drifted
// marker/block pair.
func ValidateInitialWorldTickDispatchTask(task string, contract InitialWorldTickDispatchContract) error {
	if err := validateInitialWorldTickDispatchContract(contract); err != nil {
		return err
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("initial world tick dispatch task is empty")
	}
	for _, required := range []struct {
		name  string
		value string
	}{
		{name: "marker", value: contract.Marker},
		{name: "exact core_event", value: contract.CoreEvent},
		{name: "exact hook", value: contract.Hook},
		{name: "no-preempt token", value: InitialWorldTickNoPreemptToken},
	} {
		if !strings.Contains(task, required.value) {
			return fmt.Errorf("initial world tick dispatch task missing %s", required.name)
		}
	}
	if !strings.Contains(task, contract.Block) {
		return fmt.Errorf("initial world tick dispatch task missing exact contract block")
	}
	return nil
}

func initialWorldTickChapterOne(st *store.Store) (domain.OutlineEntry, string, error) {
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return domain.OutlineEntry{}, "", fmt.Errorf("initial world tick dispatch contract: read layered_outline: %w", err)
	}
	if len(layered) > 0 {
		for _, entry := range domain.FlattenOutline(layered) {
			if entry.Chapter == 1 {
				return entry, "layered_outline", nil
			}
		}
		return domain.OutlineEntry{}, "", fmt.Errorf("initial world tick dispatch contract: authoritative layered_outline has no expanded chapter 1")
	}

	flat, err := st.Outline.LoadOutline()
	if err != nil {
		return domain.OutlineEntry{}, "", fmt.Errorf("initial world tick dispatch contract: read outline: %w", err)
	}
	for _, entry := range flat {
		if entry.Chapter == 1 {
			return entry, "outline", nil
		}
	}
	return domain.OutlineEntry{}, "", fmt.Errorf("initial world tick dispatch contract: no authoritative chapter 1 outline")
}

func initialWorldTickDispatchMarker(coreEvent, hook string) (string, error) {
	payload, err := json.Marshal(struct {
		Version   string `json:"version"`
		CoreEvent string `json:"core_event"`
		Hook      string `json:"hook"`
	}{
		Version:   InitialWorldTickDispatchContractVersion,
		CoreEvent: coreEvent,
		Hook:      hook,
	})
	if err != nil {
		return "", fmt.Errorf("initial world tick dispatch contract: encode marker payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return initialWorldTickDispatchMarkerPrefix + hex.EncodeToString(sum[:]), nil
}

func validateInitialWorldTickDispatchContract(contract InitialWorldTickDispatchContract) error {
	if strings.TrimSpace(contract.CoreEvent) == "" {
		return fmt.Errorf("initial world tick dispatch contract has empty core_event")
	}
	if strings.TrimSpace(contract.Hook) == "" {
		return fmt.Errorf("initial world tick dispatch contract has empty hook")
	}
	expectedMarker, err := initialWorldTickDispatchMarker(contract.CoreEvent, contract.Hook)
	if err != nil {
		return err
	}
	if contract.Marker != expectedMarker {
		return fmt.Errorf("initial world tick dispatch contract marker drift: want=%s got=%s", expectedMarker, contract.Marker)
	}
	if strings.TrimSpace(contract.Block) == "" {
		return fmt.Errorf("initial world tick dispatch contract block is empty")
	}
	for _, required := range []struct {
		name  string
		value string
	}{
		{name: "marker", value: contract.Marker},
		{name: "exact core_event", value: contract.CoreEvent},
		{name: "exact hook", value: contract.Hook},
		{name: "no-preempt token", value: InitialWorldTickNoPreemptToken},
	} {
		if !strings.Contains(contract.Block, required.value) {
			return fmt.Errorf("initial world tick dispatch contract block missing %s", required.name)
		}
	}
	return nil
}

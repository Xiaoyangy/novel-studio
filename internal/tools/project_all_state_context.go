package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const projectAllStateContextPath = "meta/project_all_state.json"

func (t *ContextTool) addProjectAllStateContext(
	result map[string]any,
	chapter int,
) error {
	if t == nil || t.store == nil || result == nil || chapter <= 0 {
		return nil
	}
	context, token, err := loadProjectAllStateForExecution(t.store, chapter)
	if err != nil {
		return err
	}
	if context == nil {
		return nil
	}
	result["project_all_state"] = *context
	result["project_all_state_source_token"] = token
	result["project_all_state_policy"] = "这是本轮 sealed generation 的唯一 projected 前态；World Simulator 与 Planner 必须消费 state_root、recent_transitions、open_obligations，以及存在时的 predecessor_contract。Planner 必须把 predecessor_contract 的 outgoing_consequence_id/text 逐字写入 arc_transition_contract.incoming_consequence_id/text，并用本章某个 causal_beats[].cause 逐字填写 consumed_by_cause。其他 shadow 台账只用于展开细节，冲突时以本对象为准。"
	return nil
}

func loadProjectAllStateForExecution(
	st *store.Store,
	chapter int,
) (*domain.ProjectedPlanningContextV2, string, error) {
	if st == nil || chapter <= 0 {
		return nil, "", nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, "", err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll {
		return nil, "", nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "project-all context"); err != nil {
		return nil, "", err
	}
	if lock.TargetChapter != chapter {
		return nil, "", fmt.Errorf(
			"project-all authoritative context targets chapter %d, requested chapter %d",
			lock.TargetChapter,
			chapter,
		)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(projectAllStateContextPath)))
	if err != nil {
		return nil, "", fmt.Errorf("load project-all authoritative state: %w", err)
	}
	var context domain.ProjectedPlanningContextV2
	if err := json.Unmarshal(raw, &context); err != nil {
		return nil, "", fmt.Errorf("decode project-all authoritative state: %w", err)
	}
	if err := domain.ValidateProjectedPlanningContextV2(context); err != nil {
		return nil, "", fmt.Errorf("validate project-all authoritative state: %w", err)
	}
	if context.NextChapter != chapter {
		return nil, "", fmt.Errorf(
			"project-all authoritative state next_chapter=%d, requested=%d",
			context.NextChapter,
			chapter,
		)
	}
	token, err := domain.ProjectedPlanningContextSourceTokenV2(context.ContextDigest)
	if err != nil {
		return nil, "", err
	}
	return &context, token, nil
}

func requireProjectAllStateSourceToken(
	st *store.Store,
	chapter int,
	sources []string,
) (string, error) {
	_, token, err := loadProjectAllStateForExecution(st, chapter)
	if err != nil || token == "" {
		return token, err
	}
	if projectAllStateSourcesContain(sources, token) {
		return token, nil
	}
	return token, fmt.Errorf(
		"project-all chapter %d must attest the exact authoritative context binding",
		chapter,
	)
}

func projectAllStateSourcesContain(sources []string, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	for _, source := range sources {
		if strings.TrimSpace(source) == token {
			return true
		}
	}
	return false
}

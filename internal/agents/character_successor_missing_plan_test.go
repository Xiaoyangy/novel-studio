package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// Ends the actual runner without calling its submission tool. beforeReturn can
// inject a filesystem fault after the runner's initial successor lookup.
type successorNoSubmissionModel struct {
	calls        int
	beforeReturn func() error
}

func (*successorNoSubmissionModel) SupportsTools() bool { return true }

func (m *successorNoSubmissionModel) Generate(_ context.Context, _ []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_character_agent_successor_plan" {
		return nil, fmt.Errorf("unexpected Architect tool capabilities")
	}
	if m.beforeReturn != nil {
		if err := m.beforeReturn(); err != nil {
			return nil, err
		}
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("未提交 successor 方案。")},
	}}, nil
}

func (m *successorNoSubmissionModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(stream)
	return stream, nil
}

func TestCharacterSuccessorArchitectMissingSubmissionAndFinalLoadFailure(t *testing.T) {
	for _, loadFailure := range []bool{false, true} {
		name := "no_submission"
		if loadFailure {
			name = "final_store_load_failure"
		}
		t.Run(name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "原章位", CoreEvent: "原软方向"}}))
			selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "完整收束", NonNegotiables: []string{"保留实际角色选择"}}))
			generationID := "pg2_successor_no_submission"
			receipt := domain.WorldArbitrationReceipt{
				GenerationID: generationID, Chapter: 1, Digest: "sha256:" + strings.Repeat("a", 64),
				HardContractStatus: "infeasible", HardContractConflicts: []string{"测试固定硬冲突"},
			}
			boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
			model := &successorNoSubmissionModel{}
			pointerPath := filepath.Join(st.Dir(), "meta", "character_agents", "successors", "current.json")
			if loadFailure {
				model.beforeReturn = func() error { return os.MkdirAll(pointerPath, 0o755) }
			}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "successor-no-submission", model)}
			plan, err := runCharacterAgentSuccessorArchitectForCause(context.Background(), bootstrap.Config{}, st, models, boundary, characterAgentChapterInputs{}, nil, receipt, nil)
			if plan != nil || err == nil || model.calls != 1 {
				t.Fatalf("missing submission must fail without a replacement or retry: plan=%v err=%v calls=%d", plan, err, model.calls)
			}
			if strings.Contains(err.Error(), "%!") {
				t.Fatalf("error contains a nil wrapping format artifact: %v", err)
			}
			if loadFailure {
				var pathErr *os.PathError
				if !strings.HasPrefix(err.Error(), "load Architect successor plan after execution:") || !errors.As(err, &pathErr) || pathErr.Path != pointerPath {
					t.Fatalf("final lookup lost its filesystem cause: %v", err)
				}
			} else {
				if err.Error() != "Architect returned without a successor plan" || errors.Unwrap(err) != nil {
					t.Fatalf("missing submission was confused with a store error: %v", err)
				}
				stored, loadErr := st.CharacterAgents.LoadCurrentSuccessorPlan()
				if loadErr != nil || stored != nil {
					t.Fatalf("runner synthesized a successor: plan=%v err=%v", stored, loadErr)
				}
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(pointerPath), generationID)); !os.IsNotExist(statErr) {
				t.Fatalf("runner created a replacement generation's successor artifacts: %v", statErr)
			}
		})
	}
}

package agents

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const arcRehearsalContractScopePolicy = "arc-rehearsal-contract-scope.v1"

const arcRehearsalContractScopePrompt = `
contract_checks逐项评价当前整弧的条件性可达性与当前是否违反，不是全书终态验收。不得改写或删除任何硬合同；全书规模、最终交付及持续性约束仍须保留原文，说明本弧如何遵守、哪些结果尚待后续实际履行。条件路径有现有来源支持且当前未违反时可标conditional，不能因此声称全书目标已经完成，也不能把本弧应兑现的义务任意推迟。
不得仅因章零没有已接受章节、未来正文尚未写出或全书总字数尚未完成就标unresolved/infeasible_prediction。当前合同禁止以规划冒充正文时，预演应明确自己不是正文，不把尚待交付本身判为本弧条件路径不可达。未来许可、角色意愿、测量或签认尚未发生，仍按已有规则写成conditions/assumptions；有条件不等于已经获准或保证配合。
如果所选本弧路径的必需来源或执行能力确实缺失、适用于本弧的硬义务无来源支持的可行路径，或当前已经违反持续性硬约束，仍须标unresolved/infeasible_prediction；对应材料仍按实际missing/unclear处理，不得用未来条件掩盖真实缺口。仅属后续阶段待履行且不阻断本弧的事项可在conditions或unresolved_items说明其阶段边界与理由；不得把真正阻断藏到备注。`

func ArcRehearsalProtocolDigest() (string, error) {
	previous, err := ReviewDeltaArcRehearsalProtocolDigest()
	if err != nil {
		return "", err
	}
	digest, err := domain.DeterministicPlanningHash(struct {
		Policy           string `json:"policy"`
		PreviousProtocol string `json:"previous_protocol"`
		ScopePrompt      string `json:"scope_prompt"`
	}{arcRehearsalContractScopePolicy, previous, arcRehearsalContractScopePrompt})
	if err != nil {
		return "", err
	}
	return "sha256:" + strings.TrimPrefix(digest, "sha256:"), nil
}

// Selection is bound to the exact persisted input; historical prompts and
// schemas must not acquire new guidance during a resume.
func arcRehearsalProtocolFeatures(protocol string) (scope, delta bool, err error) {
	current, err := ArcRehearsalProtocolDigest()
	if err != nil {
		return false, false, err
	}
	if protocol == current {
		return true, true, nil
	}
	previous, err := ReviewDeltaArcRehearsalProtocolDigest()
	if err != nil {
		return false, false, err
	}
	if protocol == previous {
		return false, true, nil
	}
	legacy, err := LegacyArcRehearsalProtocolDigest()
	if err != nil {
		return false, false, err
	}
	if protocol == legacy {
		return false, false, nil
	}
	return false, false, fmt.Errorf("rehearsal execution policy changed; rebuild a new input without rewriting historical reports")
}

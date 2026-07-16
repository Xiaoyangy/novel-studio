package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/errs"
	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
)

// validateFictionProseMetadataFree is the shared write/delivery guard for
// machine-only planning, retrieval and checkpoint identifiers. It does not
// inspect ordinary story vocabulary (including “系统”); only the exact internal
// keys maintained by rules.OrchestrationMetadataLeaks are rejected.
func validateFictionProseMetadataFree(content string) error {
	leaks := qualityrules.OrchestrationMetadataLeaks(content)
	if len(leaks) == 0 {
		return nil
	}
	keys := make([]string, 0, len(leaks))
	for _, leak := range leaks {
		keys = append(keys, leak.Target)
	}
	return fmt.Errorf(
		"小说正文包含内部 orchestration/RAG/checkpoint 元数据键：%s；删除整段机器标识，不要改名藏入正文: %w",
		strings.Join(keys, "、"), errs.ErrToolPrecondition,
	)
}

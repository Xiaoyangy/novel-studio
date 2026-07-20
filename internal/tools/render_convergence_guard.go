package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const renderConvergenceGuardVersion = "render-convergence-guard.v1"

type renderConvergenceGuard struct {
	Version          string   `json:"version"`
	Chapter          int      `json:"chapter"`
	PlanDigest       string   `json:"plan_digest"`
	FailedBodySHA256 []string `json:"failed_body_sha256"`
}

func renderConvergenceGuardPath(projectDir string, chapter int) string {
	return filepath.Join(
		projectDir,
		"meta",
		"planning",
		fmt.Sprintf("render_convergence_ch%04d.json", chapter),
	)
}

// SaveRenderConvergenceGuard projects the plan-owned outer ledger into an
// isolated render candidate. draft_chapter consults it before replacing the
// current draft, so an old DeepSeek/structural/formal-rejected exact hash can
// never be accepted as a new whole-body attempt.
func SaveRenderConvergenceGuard(
	st *store.Store,
	chapter int,
	planDigest string,
	failedBodySHA256 []string,
) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return fmt.Errorf("render convergence guard requires store, chapter and plan digest")
	}
	ancestorHashes, err := sealedConvergenceAncestorFailedBodySHA256(st, chapter, planDigest)
	if err != nil {
		return fmt.Errorf("render convergence guard load sealed successor ancestors: %w", err)
	}
	allHashes := append(append([]string(nil), failedBodySHA256...), ancestorHashes...)
	seen := map[string]struct{}{}
	hashes := make([]string, 0, len(allHashes))
	for _, hash := range allHashes {
		hash = strings.TrimSpace(hash)
		if !validExternalBodySHA256(hash) {
			return fmt.Errorf("render convergence guard body hash is malformed")
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	guard := renderConvergenceGuard{
		Version:          renderConvergenceGuardVersion,
		Chapter:          chapter,
		PlanDigest:       strings.TrimSpace(planDigest),
		FailedBodySHA256: hashes,
	}
	raw, err := json.MarshalIndent(guard, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicDraftIntent(renderConvergenceGuardPath(st.Dir(), chapter), raw)
}

func rejectPreviouslyFailedRenderBody(
	st *store.Store,
	chapter int,
	content string,
) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(content) == "" {
		return nil
	}
	path := renderConvergenceGuardPath(st.Dir(), chapter)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var guard renderConvergenceGuard
	if err := json.Unmarshal(raw, &guard); err != nil {
		return fmt.Errorf("parse render convergence guard: %w", err)
	}
	if guard.Version != renderConvergenceGuardVersion || guard.Chapter != chapter ||
		strings.TrimSpace(guard.PlanDigest) == "" {
		return fmt.Errorf("render convergence guard identity is invalid")
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("validate render convergence guard plan: %w", err)
	}
	if plan.Digest != guard.PlanDigest {
		// The outer pipeline will replace this stale projection when it binds the
		// new plan. Never let an old plan's failures poison a new causal epoch.
		return nil
	}
	candidateSHA := reviewreport.BodySHA256(content)
	for _, failedSHA := range guard.FailedBodySHA256 {
		if strings.TrimSpace(failedSHA) == candidateSHA {
			return fmt.Errorf(
				"第 %d 章候选正文哈希 %s 已在当前 plan 下被外判、结构门或正式 review 明确拒绝；禁止重复落盘或复判同一 exact hash，必须生成真正不同的完整稿，若持久化预算已满则返回 plan 阶段",
				chapter,
				candidateSHA,
			)
		}
	}
	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineOutlineAllContractPolicyArgument(policies []string) string {
	if len(policies) == 0 {
		return ""
	}
	return policies[0]
}

// Select only from the two source/model/prompt-addressed attempts. A policy
// default is not permission to replace a previously dispatched operation.
// The chosen attempt still goes through the ordinary source, operation-chain,
// lease and visible-context validation before any model call.
func selectPipelineOutlineAllContractPolicy(cfg bootstrap.Config, bundle assets.Bundle, sourceRoot, repairDigest string) (string, error) {
	selected, found := domain.StoryContractEvidencePolicyNarrationV1, false
	for _, policy := range []string{"", domain.StoryContractEvidencePolicyNarrationV1} {
		_, modelDigest, promptDigest, identity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, policy)
		if err != nil {
			return "", err
		}
		if repairDigest != "" {
			identity += "\noutline-repair=" + repairDigest
		}
		attempt := outlineAllAttemptID(sourceRoot, identity)
		candidate, err := filepath.Abs(pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt))
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		if err := validatePipelineOutlineAllCandidateNamespace(cfg.OutputDir, candidate, attempt, true); err != nil {
			return "", err
		}
		if err := validatePipelineOutlineAllCandidatePrepareReceipt(cfg.OutputDir, candidate, attempt); err != nil {
			if !os.IsNotExist(err) {
				return "", err
			}
			// Preserve the existing preparer's interrupted-copy recovery. It may
			// rebuild only a pre-execution copy, never erase dispatched work just
			// because its final prepare receipt disappeared.
			for _, rel := range []string{store.OutlineAllExecutionReceiptPath, "meta/planning/outline_all_operations", "meta/usage.json", store.UsageAuditPath} {
				if _, proofErr := os.Lstat(filepath.Join(candidate, filepath.FromSlash(rel))); proofErr == nil {
					return "", fmt.Errorf("outline-all candidate lost prepare receipt after execution evidence")
				} else if !os.IsNotExist(proofErr) {
					return "", proofErr
				}
			}
		}
		receipt, err := store.NewStore(candidate).LoadOutlineAllExecutionReceipt()
		if err != nil {
			return "", err
		}
		if receipt != nil && (receipt.ContractEvidencePolicy != policy || receipt.SourceSnapshotRoot != sourceRoot || receipt.AttemptID != attempt || filepath.Clean(receipt.CandidateDir) != candidate || receipt.ModelIdentityDigest != modelDigest || receipt.PromptProtocolDigest != promptDigest) {
			return "", fmt.Errorf("outline-all existing candidate contract policy/source identity drifted")
		}
		if found {
			return "", fmt.Errorf("outline-all multiple policy attempts match current sources; refusing ambiguous recovery")
		}
		selected, found = policy, true
	}
	return selected, nil
}

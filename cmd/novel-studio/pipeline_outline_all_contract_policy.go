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

func pipelineOutlineAllInputPolicyArgument(policies []string) string {
	if len(policies) < 2 {
		return ""
	}
	return policies[1]
}

type pipelineOutlineAllPolicies struct {
	ContractEvidence string
	Input            string
}

// Retain the contract-only interface for existing callers and fixtures.
func selectPipelineOutlineAllContractPolicy(cfg bootstrap.Config, bundle assets.Bundle, sourceRoot, repairDigest string) (string, error) {
	selected, err := selectPipelineOutlineAllPolicies(cfg, bundle, sourceRoot, repairDigest)
	return selected.ContractEvidence, err
}

// Select only from the three source/model/prompt-addressed attempts. A policy
// default is not permission to replace a previously dispatched operation.
// The chosen attempt still goes through the ordinary source, operation-chain,
// lease and visible-context validation before any model call.
func selectPipelineOutlineAllPolicies(cfg bootstrap.Config, bundle assets.Bundle, sourceRoot, repairDigest string) (pipelineOutlineAllPolicies, error) {
	selected := pipelineOutlineAllPolicies{ContractEvidence: domain.StoryContractEvidencePolicyNarrationV1, Input: domain.OutlineAllInputPolicyCompleteFoundationV1}
	found := false
	for _, policy := range []pipelineOutlineAllPolicies{{}, {ContractEvidence: domain.StoryContractEvidencePolicyNarrationV1}, selected} {
		_, modelDigest, promptDigest, identity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle, policy.ContractEvidence, policy.Input)
		if err != nil {
			return pipelineOutlineAllPolicies{}, err
		}
		if repairDigest != "" {
			identity += "\noutline-repair=" + repairDigest
		}
		attempt := outlineAllAttemptID(sourceRoot, identity)
		candidate, err := filepath.Abs(pipelineOutlineAllCandidatePath(cfg.OutputDir, attempt))
		if err != nil {
			return pipelineOutlineAllPolicies{}, err
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return pipelineOutlineAllPolicies{}, err
		}
		if err := validatePipelineOutlineAllCandidateNamespace(cfg.OutputDir, candidate, attempt, true); err != nil {
			return pipelineOutlineAllPolicies{}, err
		}
		if err := validatePipelineOutlineAllCandidatePrepareReceipt(cfg.OutputDir, candidate, attempt); err != nil {
			if !os.IsNotExist(err) {
				return pipelineOutlineAllPolicies{}, err
			}
			// Preserve the existing preparer's interrupted-copy recovery. It may
			// rebuild only a pre-execution copy, never erase dispatched work just
			// because its final prepare receipt disappeared.
			for _, rel := range []string{store.OutlineAllExecutionReceiptPath, "meta/planning/outline_all_operations", "meta/usage.json", store.UsageAuditPath} {
				if _, proofErr := os.Lstat(filepath.Join(candidate, filepath.FromSlash(rel))); proofErr == nil {
					return pipelineOutlineAllPolicies{}, fmt.Errorf("outline-all candidate lost prepare receipt after execution evidence")
				} else if !os.IsNotExist(proofErr) {
					return pipelineOutlineAllPolicies{}, proofErr
				}
			}
		}
		receipt, err := store.NewStore(candidate).LoadOutlineAllExecutionReceipt()
		if err != nil {
			return pipelineOutlineAllPolicies{}, err
		}
		if receipt != nil && (receipt.ContractEvidencePolicy != policy.ContractEvidence || receipt.InputPolicy != policy.Input || receipt.SourceSnapshotRoot != sourceRoot || receipt.AttemptID != attempt || filepath.Clean(receipt.CandidateDir) != candidate || receipt.ModelIdentityDigest != modelDigest || receipt.PromptProtocolDigest != promptDigest) {
			return pipelineOutlineAllPolicies{}, fmt.Errorf("outline-all existing candidate contract/input policy/source identity drifted")
		}
		if found {
			return pipelineOutlineAllPolicies{}, fmt.Errorf("outline-all multiple policy attempts match current sources; refusing ambiguous recovery")
		}
		selected, found = policy, true
	}
	return selected, nil
}

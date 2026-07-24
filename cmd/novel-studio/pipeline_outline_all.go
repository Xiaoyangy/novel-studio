package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const pipelineOutlineAllPromptProtocol = `outline-all.single-mutation.direct-architect.v3
- host dispatches the complete frozen operation prompt directly to the configured Architect primary model
- the direct Architect receives only save_foundation and may make exactly one receipt-authorized mutation
- append_volume is reservation-only; expand_arc and revise_arc preserve the exact span
- title/core_event/hook/scenes are prose design, contract_refs are structural receipts
- each payoff chapter must contain planned_resolution evidence in core_event/scenes
- model-visible context is bounded and digest-bound to the complete layered outline
- no Coordinator execution dependency, prose, world tick, user rules, compass, foundation replacement, fallback model, or global renumbering`

const (
	pipelineOutlineAllDispatchProvider  = "host"
	pipelineOutlineAllDispatchModel     = "direct_dispatch"
	pipelineOutlineAllDispatchReasoning = "not_applicable"
)

var pipelineOutlineAllLease = pipelineExecutionLease

var runPipelineOutlineAllArchitect = func(
	cfg bootstrap.Config,
	bundle assets.Bundle,
	prompt string,
) error {
	cfg.DisableModelFailover = true
	return agents.RunOutlineAllOperation(context.Background(), cfg, bundle, cfg.OutputDir, prompt)
}

func pipelineOutlineAll(opts cliOptions, flags pipelineFlags) (returnErr error) {
	outputDir, err := pipelineOutlineAllOutputDirBeforeLoad(opts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("outline-all requires configured output directory before pipeline setup")
	}
	releaseControl, err := acquirePipelineOutlineAllControl(outputDir, true)
	if err != nil {
		return err
	}
	defer func() {
		if err := releaseControl(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("outline-all release run-root control: %w", err)
		}
	}()
	// The exclusive run-root control must cover recovery, marker creation, and
	// every live-directory read/write. loadCfgBundle writes prompt_manifest, so
	// it deliberately runs only after the control has been acquired.
	if err := recoverAllDirectoryPublishesWithControlHeld(outputDir); err != nil {
		return err
	}
	if err := ensurePipelineOutlineAllRequirement(outputDir); err != nil {
		return fmt.Errorf("outline-all persist run-root requirement: %w", err)
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if filepath.Clean(cfg.OutputDir) != filepath.Clean(outputDir) {
		return fmt.Errorf("outline-all output directory changed between pre-load control and config load")
	}
	live := store.NewStore(cfg.OutputDir)
	if err := live.Init(); err != nil {
		return err
	}
	var outlineRepairManifest *pipelineOutlineRepairManifest
	if strings.TrimSpace(flags.OutlineRepairFile) != "" {
		manifest, digest, err := loadPipelineOutlineRepairManifest(flags.OutlineRepairFile)
		if err != nil {
			return err
		}
		if flags.OutlineRepairDigest == "" || digest != flags.OutlineRepairDigest {
			return fmt.Errorf("outline repair manifest changed after pipeline run identity was bound")
		}
		outlineRepairManifest = &manifest
	}
	if completed, err := loadCompletedPipelineOutlineAll(live); err != nil {
		return err
	} else if completed {
		if outlineRepairManifest != nil {
			return fmt.Errorf("--outline-repair-file requires a fresh outline-all; existing completed outline-all must first be retired with --rebase-all-chapters")
		}
		if _, err := verifyPipelineOutlineAllReceiptAndArtifacts(cfg.OutputDir); err != nil {
			return err
		}
		return nil
	}
	if outlineRepairManifest != nil {
		if err := validatePipelineOutlineRepairLiveEntry(live); err != nil {
			return fmt.Errorf("outline repair lock-before entry rejected: %w", err)
		}
	} else if err := validatePipelineOutlineAllLiveEntry(live); err != nil {
		return fmt.Errorf("outline-all lock-before entry rejected: %w", err)
	}

	owner := pipelineExecutionOwner("outline-all", 1)
	if err := live.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionOutlineAll,
		TargetChapter: 1,
		PlanDigest:    "outline-all-live-guard",
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("outline-all acquire live execution lock: %w", err)
	}
	defer func() {
		if err := live.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("outline-all release live execution lock: %w", err)
		}
	}()
	if err := validatePipelineOutlineAllLiveEntry(live); err != nil {
		return fmt.Errorf("outline-all lock-after entry rejected: %w", err)
	}
	compass, err := live.Outline.LoadCompass()
	if err != nil || compass == nil {
		return fmt.Errorf("outline-all requires compass: %w", err)
	}
	liveVolumes, err := live.Outline.LoadLayeredOutline()
	if err != nil || len(liveVolumes) == 0 {
		return fmt.Errorf("outline-all requires layered_outline: %w", err)
	}
	if len(compass.NonNegotiables) == 0 {
		return fmt.Errorf("outline-all requires compass.non_negotiables; rerun the restricted architect compass migration before full-book planning")
	}
	if issues := domain.OutlineAllArcSpanIssues(liveVolumes); len(issues) > 0 {
		return fmt.Errorf("outline-all existing arc spans cannot be expanded in one reliable call: %s", summarizeOutlineContractIssues(issues, 12))
	}
	target, err := domain.ResolveBookScaleTarget(
		compass.EstimatedScale,
		domain.RealVolumeCount(liveVolumes),
		domain.TotalChapters(liveVolumes),
	)
	if err != nil {
		return fmt.Errorf("outline-all parse compass estimated_scale: %w", err)
	}
	if generationID, created, err := ensurePipelineOutlineAllGeneration(
		live,
		zeroSimulationGenerationID(time.Now().UTC().Format(time.RFC3339Nano)),
	); err != nil {
		return fmt.Errorf("outline-all chapter-zero generation rejected: %w", err)
	} else if created {
		fmt.Fprintf(os.Stderr, "[pipeline:outline-all] 已建立章零 generation %s\n", generationID)
	}
	sourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(cfg.OutputDir)
	if err != nil {
		return err
	}
	protectedRoot, err := pipelineOutlineAllProtectedCanonRoot(cfg.OutputDir)
	if err != nil {
		return err
	}
	stableProgressRoot, err := pipelineOutlineAllStableProgressRoot(cfg.OutputDir)
	if err != nil {
		return err
	}
	frozenFoundation, err := loadPipelineOutlineAllFrozenFoundation(cfg.OutputDir)
	if err != nil {
		return err
	}
	identity, modelDigest, promptDigest, executionIdentity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		return err
	}
	attemptID := outlineAllAttemptID(sourceRoot, executionIdentity)
	if outlineRepairManifest != nil {
		attemptID = outlineAllAttemptID(
			sourceRoot,
			executionIdentity+"\noutline-repair="+flags.OutlineRepairDigest,
		)
	}
	candidateDir, err := filepath.Abs(pipelineOutlineAllCandidatePath(cfg.OutputDir, attemptID))
	if err != nil {
		return err
	}
	var candidate *store.Store
	var outlineRepairReceipt *pipelineOutlineRepairReceipt
	for preparation := 0; preparation < 2; preparation++ {
		if err := preparePipelineOutlineAllCandidate(cfg.OutputDir, candidateDir, attemptID); err != nil {
			return err
		}
		candidate = store.NewStore(candidateDir)
		if err := candidate.Init(); err != nil {
			return err
		}
		if err := validatePipelineOutlineAllEntry(candidate); err != nil {
			return fmt.Errorf("outline-all candidate entry rejected: %w", err)
		}
		if outlineRepairManifest != nil {
			outlineRepairReceipt, err = applyPipelineOutlineRepairCandidate(
				candidate, attemptID, sourceRoot, *outlineRepairManifest, flags.OutlineRepairDigest,
			)
			if errors.Is(err, errPipelineOutlineRepairCandidateIncomplete) && preparation == 0 {
				if err := validatePipelineOutlineAllCandidateNamespace(cfg.OutputDir, candidateDir, attemptID, true); err != nil {
					return err
				}
				if err := os.RemoveAll(candidateDir); err != nil {
					return fmt.Errorf("rebuild interrupted outline repair candidate: %w", err)
				}
				continue
			}
			if err != nil {
				return err
			}
		}
		break
	}
	if candidate == nil || (outlineRepairManifest != nil && outlineRepairReceipt == nil) {
		return fmt.Errorf("outline-all failed to prepare outline repair candidate")
	}
	existingCandidateReceipt, err := candidate.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return err
	}
	if existingCandidateReceipt == nil {
		if outlineRepairReceipt == nil {
			candidateSourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(candidateDir)
			if err != nil {
				return err
			}
			if candidateSourceRoot != sourceRoot {
				return fmt.Errorf("outline-all candidate source snapshot differs from live baseline")
			}
		}
	} else if existingCandidateReceipt.SourceSnapshotRoot != sourceRoot {
		return fmt.Errorf("outline-all recovered candidate does not bind the current live baseline")
	}
	candidateProtected, err := pipelineOutlineAllProtectedCanonRoot(candidateDir)
	if err != nil {
		return err
	}
	if candidateProtected != protectedRoot {
		return fmt.Errorf("outline-all candidate protected canon differs from live baseline")
	}
	if candidateStable, err := pipelineOutlineAllStableProgressRoot(candidateDir); err != nil {
		return err
	} else if candidateStable != stableProgressRoot {
		return fmt.Errorf("outline-all candidate stable progress differs from live baseline")
	}
	if candidateFoundation, err := loadPipelineOutlineAllFrozenFoundation(candidateDir); err != nil {
		return err
	} else if candidateFoundation.Root != frozenFoundation.Root {
		return fmt.Errorf("outline-all candidate frozen foundation differs from live baseline")
	}

	if err := candidate.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionOutlineAll,
		TargetChapter: 1,
		PlanDigest:    modelDigest,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("outline-all acquire candidate execution lock: %w", err)
	}
	candidateLockHeld := true
	defer func() {
		if candidateLockHeld {
			if err := candidate.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
				returnErr = fmt.Errorf("outline-all release candidate execution lock: %w", err)
			}
		}
	}()
	lock, err := candidate.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		return fmt.Errorf("outline-all load candidate execution lock: %w", err)
	}
	receipt, err := ensurePipelineOutlineAllReceipt(
		candidate, *lock, *compass, target, sourceRoot, protectedRoot,
		stableProgressRoot, frozenFoundation.Root,
		attemptID, candidateDir, identity, modelDigest, promptDigest,
	)
	if err != nil {
		return err
	}
	if receipt.CompletedActionCount == 0 && receipt.PendingAction == nil {
		if outlineRepairReceipt != nil {
			if err := validatePipelineOutlineRepairCurrentTail(candidate, *outlineRepairReceipt); err != nil {
				return err
			}
		} else {
			if currentSource, err := pipelineOutlineAllSourceSnapshotRoot(candidateDir); err != nil {
				return err
			} else if currentSource != sourceRoot {
				return fmt.Errorf("outline-all zero-operation candidate drifted from source snapshot")
			}
		}
	}
	if err := validatePipelineOutlineAllOperationChain(candidate, receipt); err != nil {
		return err
	}

	candidateCfg := cfg
	candidateCfg.OutputDir = candidateDir
	candidateCfg.DisableModelFailover = true
	for iteration := 0; iteration < 4096; iteration++ {
		if receipt.Status == domain.OutlineAllExecutionComplete {
			break
		}
		receipt, err = refreshPipelineOutlineAllLeases(live, candidate, owner, modelDigest, receipt)
		if err != nil {
			return err
		}
		if err := validatePipelineOutlineAllEntry(candidate); err != nil {
			return fmt.Errorf("outline-all candidate left chapter-zero boundary: %w", err)
		}
		if err := validatePipelineOutlineAllCandidateNamespace(cfg.OutputDir, candidateDir, attemptID, true); err != nil {
			return err
		}
		if currentProtected, err := pipelineOutlineAllProtectedCanonRoot(candidateDir); err != nil {
			return err
		} else if currentProtected != protectedRoot {
			return fmt.Errorf("outline-all candidate modified protected canon")
		}
		if err := validatePipelineOutlineAllStableInputs(candidateDir, stableProgressRoot, frozenFoundation.Root); err != nil {
			return err
		}
		if receipt.PendingAction != nil {
			receipt, err = recoverOrRunPipelineOutlineAllOperation(
				candidateCfg, bundle, live, candidate, owner, *compass, target,
				receipt, modelDigest, promptDigest,
			)
			if err != nil {
				return err
			}
			continue
		}
		volumes, err := candidate.Outline.LoadLayeredOutline()
		if err != nil {
			return err
		}
		action, ok, err := outlineAllNextStructuralAction(volumes, *compass, target)
		if err != nil {
			return err
		}
		if !ok {
			action, ok = outlineAllNextRevisionAction(volumes, *compass)
		}
		if !ok {
			finalVolumes, err := validatePipelineOutlineAllFinal(candidate, *compass, target)
			if err != nil {
				return err
			}
			flat := domain.FlattenOutline(finalVolumes)
			layeredDigest, err := domain.ComputeLayeredOutlineDigest(finalVolumes)
			if err != nil {
				return err
			}
			flatDigest, err := domain.ComputeFlatOutlineDigest(flat)
			if err != nil {
				return err
			}
			readinessJSONDigest, readinessMDDigest, err := refreshPipelineOutlineAllArchitectReadiness(candidateDir)
			if err != nil {
				return err
			}
			receipt, err = candidate.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
				current.Status = domain.OutlineAllExecutionComplete
				current.PendingAction = nil
				current.FinalLayeredDigest = layeredDigest
				current.FinalFlatDigest = flatDigest
				current.ArchitectReadinessJSONDigest = readinessJSONDigest
				current.ArchitectReadinessMDDigest = readinessMDDigest
				current.UpdatedAt = time.Now().UTC()
				return nil
			})
			if err != nil {
				return err
			}
			break
		}
		beforeDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
		if err != nil {
			return err
		}
		action.Operation = receipt.CompletedActionCount + 1
		action.BeforeLayeredDigest = beforeDigest
		_, visibleRaw, visibleDigest, err := buildPipelineOutlineAllModelVisibleContext(
			volumes, *compass, target, action, frozenFoundation, bundle.References,
		)
		if err != nil {
			return err
		}
		intent, err := createPipelineOutlineAllOperationIntent(
			candidateDir, attemptID, action, volumes, receipt.FoundationContextRoot, modelDigest, promptDigest,
			visibleDigest, len(visibleRaw),
		)
		if err != nil {
			return err
		}
		_ = intent
		receipt, err = candidate.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
			current.PendingAction = &action
			current.UpdatedAt = time.Now().UTC()
			return nil
		})
		if err != nil {
			return err
		}
	}
	if receipt.Status != domain.OutlineAllExecutionComplete {
		return fmt.Errorf("outline-all exceeded operation safety limit")
	}
	if _, err := validatePipelineOutlineAllFinal(candidate, *compass, target); err != nil {
		return err
	}
	if currentProtected, err := pipelineOutlineAllProtectedCanonRoot(candidateDir); err != nil {
		return err
	} else if currentProtected != protectedRoot {
		return fmt.Errorf("outline-all final candidate modified protected canon")
	}
	if err := validatePipelineOutlineAllStableInputs(candidateDir, stableProgressRoot, frozenFoundation.Root); err != nil {
		return err
	}
	if err := validatePipelineOutlineAllEntry(candidate); err != nil {
		return fmt.Errorf("outline-all final candidate contains prose/canon: %w", err)
	}
	if err := validatePipelineOutlineAllLiveEntry(live); err != nil {
		return fmt.Errorf("outline-all live changed before publish: %w", err)
	}
	if currentSource, err := pipelineOutlineAllSourceSnapshotRoot(cfg.OutputDir); err != nil {
		return err
	} else if currentSource != sourceRoot {
		return fmt.Errorf("outline-all live source CAS changed before publish")
	}
	if currentProtected, err := pipelineOutlineAllProtectedCanonRoot(cfg.OutputDir); err != nil {
		return err
	} else if currentProtected != protectedRoot {
		return fmt.Errorf("outline-all live protected canon changed before publish")
	}
	if err := validatePipelineOutlineAllStableInputs(cfg.OutputDir, stableProgressRoot, frozenFoundation.Root); err != nil {
		return err
	}
	if err := validatePipelineOutlineAllCandidateNamespace(cfg.OutputDir, candidateDir, attemptID, true); err != nil {
		return err
	}

	if err := copyPipelineMetadataForOutlineAllPublish(cfg.OutputDir, candidateDir); err != nil {
		return err
	}
	expectedLiveRoot, err := store.DirectoryContentRoot(cfg.OutputDir)
	if err != nil {
		return err
	}
	if receipt.ExpectedLiveDirectoryRoot != "" && receipt.ExpectedLiveDirectoryRoot != expectedLiveRoot {
		return fmt.Errorf("outline-all expected live directory root drifted before publish")
	}
	receipt, err = candidate.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.ExpectedLiveDirectoryRoot = expectedLiveRoot
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		return fmt.Errorf("outline-all bind expected live directory root: %w", err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(cfg.OutputDir))
	var publishReceipt *store.DirectoryPublishReceipt
	err = withPipelineWatchdogPaused(func() error {
		var publishErr error
		publishReceipt, publishErr = publisher.PublishDirectory(store.PublishDirectoryRequest{
			TransactionID:    attemptID,
			LiveDir:          cfg.OutputDir,
			CandidateDir:     candidateDir,
			ExpectedLiveRoot: expectedLiveRoot,
		})
		return publishErr
	})
	if err != nil {
		return fmt.Errorf("outline-all publish candidate: %w", err)
	}
	if publishReceipt == nil || publishReceipt.CommittedLiveRoot == "" {
		return fmt.Errorf("outline-all directory publish returned incomplete receipt")
	}
	if err := publisher.FinalizeDirectoryPublish(attemptID); err != nil {
		return fmt.Errorf("outline-all finalize candidate publish: %w", err)
	}
	published := store.NewStore(cfg.OutputDir)
	publishedExecution, err := published.LoadOutlineAllExecutionReceipt()
	if err != nil || publishedExecution == nil {
		return fmt.Errorf("outline-all published execution receipt missing: %w", err)
	}
	if _, err := published.UpdateOutlineAllExecutionReceipt(publishedExecution.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.PublishedCandidateRoot = publishReceipt.CandidateRoot
		current.DirectoryPublishReceiptDigest = publishReceipt.ReceiptDigest
		current.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		return fmt.Errorf("outline-all bind directory publish receipt: %w", err)
	}
	if err := published.Runtime.ReleasePipelineExecution(owner); err != nil {
		return fmt.Errorf("outline-all release published execution lock: %w", err)
	}
	candidateLockHeld = false
	if err := validatePipelineOutlineAllEntry(published); err != nil {
		return fmt.Errorf("outline-all published prose/canon contamination: %w", err)
	}
	if currentProtected, err := pipelineOutlineAllProtectedCanonRoot(cfg.OutputDir); err != nil {
		return err
	} else if currentProtected != protectedRoot {
		return fmt.Errorf("outline-all published protected canon differs from entry root")
	}
	if _, err = verifyPipelineOutlineAllReceiptAndArtifacts(cfg.OutputDir); err != nil {
		return err
	}
	return nil
}

func refreshPipelineOutlineAllArchitectReadiness(outputDir string) (string, string, error) {
	readiness := assessArchitectReadiness(outputDir)
	if err := writeArchitectReadiness(outputDir, readiness); err != nil {
		return "", "", fmt.Errorf("outline-all write refreshed architect readiness: %w", err)
	}
	if !readiness.Ready {
		return "", "", fmt.Errorf(
			"outline-all final foundation is not architect-ready: missing=%v issues=%v warnings=%v",
			readiness.Missing, readiness.Issues, readiness.Warnings,
		)
	}
	if ok, reason := architectReadinessState(outputDir); !ok {
		return "", "", fmt.Errorf("outline-all refreshed architect readiness is stale or invalid: %s", reason)
	}
	jsonDigest, err := pipelineRequiredFileSHA(outputDir, "meta/architect_readiness.json")
	if err != nil {
		return "", "", err
	}
	mdDigest, err := pipelineRequiredFileSHA(outputDir, "meta/architect_readiness.md")
	if err != nil {
		return "", "", err
	}
	return jsonDigest, mdDigest, nil
}

func refreshPipelineOutlineAllLeases(
	live, candidate *store.Store,
	owner, modelDigest string,
	receipt *domain.OutlineAllExecutionReceipt,
) (*domain.OutlineAllExecutionReceipt, error) {
	if err := validatePipelineOutlineAllCandidateNamespace(live.Dir(), candidate.Dir(), receipt.AttemptID, true); err != nil {
		return receipt, err
	}
	expiresAt := time.Now().UTC().Add(pipelineOutlineAllLease)
	for label, st := range map[string]*store.Store{"live": live, "candidate": candidate} {
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1,
			PlanDigest: modelDigest, Owner: owner, ExpiresAt: expiresAt,
		}); err != nil {
			return receipt, fmt.Errorf("outline-all refresh %s lease: %w", label, err)
		}
	}
	lock, err := candidate.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		return receipt, fmt.Errorf("outline-all refreshed candidate lease missing: %w", err)
	}
	return candidate.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		if err := domain.BindOutlineAllExecutionLock(current, *lock); err != nil {
			return err
		}
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func pipelineOutlineAllOutputDirBeforeLoad(opts cliOptions) (string, error) {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return "", nil
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("outline-all pre-load config: %w", err)
	}
	if err := normalizeOutputAndRAGForInvocation(&cfg, opts.Dir, hasConfiguredRAGQdrantCollection(opts)); err != nil {
		return "", err
	}
	return cfg.OutputDir, nil
}
func loadCompletedPipelineOutlineAll(st *store.Store) (bool, error) {
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil {
		return false, err
	}
	return receipt.Status == domain.OutlineAllExecutionComplete, nil
}

func pipelineOutlineAllExecutionIdentity(
	cfg bootstrap.Config,
	bundle assets.Bundle,
) (domain.OutlineAllModelIdentity, string, string, string, error) {
	role := func(name string) (provider, model, reasoning string) {
		provider, model = cfg.Provider, cfg.ModelName
		if configured, ok := cfg.Roles[name]; ok {
			if strings.TrimSpace(configured.Provider) != "" {
				provider = configured.Provider
			}
			if strings.TrimSpace(configured.Model) != "" {
				model = configured.Model
			}
		}
		reasoning = strings.TrimSpace(cfg.ResolveReasoningEffort(name))
		if reasoning == "" {
			reasoning = "provider_default"
		}
		return strings.TrimSpace(provider), strings.TrimSpace(model), reasoning
	}
	ap, am, ar := role("architect")
	identity := domain.OutlineAllModelIdentity{
		// Kept non-empty for the v1 receipt schema. These constants describe
		// the host's direct dispatch boundary; they are not an LLM dependency.
		CoordinatorProvider:  pipelineOutlineAllDispatchProvider,
		CoordinatorModel:     pipelineOutlineAllDispatchModel,
		CoordinatorReasoning: pipelineOutlineAllDispatchReasoning,
		ArchitectProvider:    ap, ArchitectModel: am, ArchitectReasoning: ar,
	}
	if ap == "" || am == "" {
		return identity, "", "", "", fmt.Errorf("outline-all requires a configured Architect primary model")
	}
	modelDigest, err := domain.ComputeOutlineAllModelIdentityDigest(identity)
	if err != nil {
		return identity, "", "", "", err
	}
	directArchitectDigest, err := agents.OutlineAllOperationProtocolDigest(bundle.Prompts.ArchitectLong)
	if err != nil {
		return identity, "", "", "", err
	}
	promptBinding := pipelineOutlineAllPromptProtocol + "\ndirect_architect_protocol=" +
		directArchitectDigest + "\nreference_pack=" +
		pipelineProjectAllDigest(bundle.References)
	promptDigest, err := domain.ComputeOutlineAllPromptProtocolDigest(promptBinding)
	if err != nil {
		return identity, "", "", "", err
	}
	executionIdentity := modelDigest + "\n" + promptDigest
	return identity, modelDigest, promptDigest, executionIdentity, nil
}

func ensurePipelineOutlineAllReceipt(
	st *store.Store,
	lock domain.PipelineExecutionLock,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
	sourceRoot, protectedRoot, stableProgressRoot, foundationContextRoot, attemptID, candidateDir string,
	identity domain.OutlineAllModelIdentity,
	modelDigest, promptDigest string,
) (*domain.OutlineAllExecutionReceipt, error) {
	mode, err := st.LoadWritingPipelineMode()
	if err != nil || mode == nil || mode.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
		return nil, fmt.Errorf("outline-all candidate requires sealed_two_pass_v2 receipt: %w", err)
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(compass)
	if err != nil {
		return nil, err
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, fmt.Errorf("outline-all candidate progress missing: %w", err)
	}
	generationID := strings.TrimSpace(progress.GenerationID)
	if generationID == "" || generationID != progress.GenerationID {
		return nil, fmt.Errorf("outline-all candidate progress requires a canonical generation_id")
	}
	existing, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return nil, err
	}
	if existing == nil {
		now := time.Now().UTC()
		receipt := domain.OutlineAllExecutionReceipt{
			Version: domain.OutlineAllExecutionReceiptVersion, Mode: domain.OutlineAllExecutionMode,
			Status: domain.OutlineAllExecutionBuilding, BaseCanonChapter: 0,
			GenerationID: generationID,
			WritingMode:  mode.Mode, WritingModeReceiptDigest: mode.ReceiptDigest,
			CompassDigest: compassDigest, EstimatedScale: compass.EstimatedScale,
			EndingDirection: compass.EndingDirection, NonNegotiables: append([]string(nil), compass.NonNegotiables...),
			MinVolumes: target.Range.MinVolumes, MaxVolumes: target.Range.MaxVolumes,
			MinChapters: target.Range.MinChapters, MaxChapters: target.Range.MaxChapters,
			TargetVolumes: target.TargetVolumes, TargetChapters: target.TargetChapters,
			TargetWords: target.TargetWords, TargetWordsPerChapter: target.TargetWordsPerChapter,
			StoryTimeHint:      target.StoryTimeHint,
			SourceSnapshotRoot: sourceRoot, ProtectedCanonRoot: protectedRoot,
			StableProgressRoot: stableProgressRoot, FoundationContextRoot: foundationContextRoot,
			AttemptID: attemptID, CandidateDir: candidateDir,
			CoordinatorProvider: identity.CoordinatorProvider, CoordinatorModel: identity.CoordinatorModel,
			CoordinatorReasoning: identity.CoordinatorReasoning,
			ArchitectProvider:    identity.ArchitectProvider, ArchitectModel: identity.ArchitectModel,
			ArchitectReasoning:  identity.ArchitectReasoning,
			ModelIdentityDigest: modelDigest, PromptProtocolDigest: promptDigest,
			StartedAt: now, UpdatedAt: now,
		}
		if err := domain.BindOutlineAllExecutionLock(&receipt, lock); err != nil {
			return nil, err
		}
		signed, err := domain.SignOutlineAllExecutionReceipt(receipt)
		if err != nil {
			return nil, err
		}
		if err := st.SaveOutlineAllExecutionReceipt(signed); err != nil {
			return nil, err
		}
		return &signed, nil
	}
	if existing.GenerationID != generationID ||
		existing.SourceSnapshotRoot != sourceRoot || existing.ProtectedCanonRoot != protectedRoot ||
		existing.StableProgressRoot != stableProgressRoot || existing.FoundationContextRoot != foundationContextRoot ||
		existing.AttemptID != attemptID || filepath.Clean(existing.CandidateDir) != filepath.Clean(candidateDir) ||
		existing.CompassDigest != compassDigest || existing.ModelIdentityDigest != modelDigest ||
		existing.PromptProtocolDigest != promptDigest || existing.TargetVolumes != target.TargetVolumes ||
		existing.TargetChapters != target.TargetChapters || existing.TargetWords != target.TargetWords ||
		existing.TargetWordsPerChapter != target.TargetWordsPerChapter || existing.StoryTimeHint != target.StoryTimeHint {
		return nil, fmt.Errorf("outline-all recovered attempt identity drift; start a new deterministic attempt")
	}
	return st.UpdateOutlineAllExecutionReceipt(existing.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		if err := domain.BindOutlineAllExecutionLock(current, lock); err != nil {
			return err
		}
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func createPipelineOutlineAllOperationIntent(
	candidateDir, attemptID string,
	action domain.OutlineAllPendingAction,
	before []domain.VolumeOutline,
	foundationContextRoot string,
	modelDigest, promptDigest string,
	visibleContextDigest string,
	visibleContextBytes int,
) (pipelineOutlineAllOperationIntent, error) {
	contextRoot := pipelineOutlineAllOperationContextRoot(foundationContextRoot, action.BeforeLayeredDigest)
	path := filepath.Join(candidateDir, filepath.FromSlash(pipelineOutlineAllOperationIntentPath(action.Operation)))
	if raw, err := os.ReadFile(path); err == nil {
		var existing pipelineOutlineAllOperationIntent
		if err := json.Unmarshal(raw, &existing); err != nil {
			return existing, err
		}
		storedDigest := existing.IntentDigest
		validated, signErr := signPipelineOutlineAllOperationIntent(existing)
		if signErr != nil || validated.IntentDigest != storedDigest ||
			existing.Version != "outline-all-operation-intent.v1" ||
			existing.AttemptID != attemptID ||
			!domain.OutlineAllPendingActionEqual(existing.Action, action) ||
			existing.BeforeLayeredDigest != action.BeforeLayeredDigest ||
			existing.ContextRoot != contextRoot ||
			existing.VisibleContextDigest != visibleContextDigest ||
			existing.VisibleContextBytes != visibleContextBytes ||
			pipelineOutlineAllLayeredDigest(existing.BeforeVolumes) != action.BeforeLayeredDigest ||
			existing.ModelIdentityDigest != modelDigest || existing.PromptProtocolDigest != promptDigest ||
			!reflect.DeepEqual(existing.BeforeVolumes, before) {
			return existing, fmt.Errorf("outline-all operation %d existing intent is invalid or drifted", action.Operation)
		}
		return existing, nil
	} else if !os.IsNotExist(err) {
		return pipelineOutlineAllOperationIntent{}, err
	}
	intent := pipelineOutlineAllOperationIntent{
		Version: "outline-all-operation-intent.v1", AttemptID: attemptID,
		Action: action, BeforeLayeredDigest: action.BeforeLayeredDigest,
		BeforeVolumes: before, ContextRoot: contextRoot, ModelIdentityDigest: modelDigest,
		VisibleContextDigest: visibleContextDigest, VisibleContextBytes: visibleContextBytes,
		PromptProtocolDigest: promptDigest, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	signed, err := signPipelineOutlineAllOperationIntent(intent)
	if err != nil {
		return intent, err
	}
	_, err = writePipelinePlanningJSON(path, signed)
	return signed, err
}

func recoverOrRunPipelineOutlineAllOperation(
	cfg bootstrap.Config,
	bundle assets.Bundle,
	live, st *store.Store,
	owner string,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
	receipt *domain.OutlineAllExecutionReceipt,
	modelDigest, promptDigest string,
) (*domain.OutlineAllExecutionReceipt, error) {
	action := *receipt.PendingAction
	if err := validatePipelineOutlineAllCandidateNamespace(live.Dir(), st.Dir(), receipt.AttemptID, true); err != nil {
		return receipt, err
	}
	intentPath := filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineAllOperationIntentPath(action.Operation)))
	var intent pipelineOutlineAllOperationIntent
	if err := readPipelinePlanningJSON(intentPath, &intent); err != nil {
		return receipt, fmt.Errorf("outline-all pending operation intent missing: %w", err)
	}
	wantIntentDigest := intent.IntentDigest
	signedIntent, err := signPipelineOutlineAllOperationIntent(intent)
	if err != nil || signedIntent.IntentDigest != wantIntentDigest ||
		!domain.OutlineAllPendingActionEqual(intent.Action, action) ||
		intent.AttemptID != receipt.AttemptID || intent.ModelIdentityDigest != modelDigest ||
		intent.PromptProtocolDigest != promptDigest ||
		intent.ContextRoot != pipelineOutlineAllOperationContextRoot(receipt.FoundationContextRoot, action.BeforeLayeredDigest) ||
		intent.VisibleContextDigest == "" || intent.VisibleContextBytes <= 0 ||
		pipelineOutlineAllLayeredDigest(intent.BeforeVolumes) != action.BeforeLayeredDigest {
		return receipt, fmt.Errorf("outline-all pending operation intent is invalid or drifted")
	}
	current, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return receipt, err
	}
	currentDigest, err := domain.ComputeLayeredOutlineDigest(current)
	if err != nil {
		return receipt, err
	}
	foundation, loadErr := loadPipelineOutlineAllFrozenFoundation(st.Dir())
	if loadErr != nil {
		return receipt, loadErr
	}
	if foundation.Root != receipt.FoundationContextRoot {
		return receipt, fmt.Errorf("outline-all operation %d frozen context root drifted", action.Operation)
	}
	visibleContext, visibleRaw, visibleDigest, err := buildPipelineOutlineAllModelVisibleContext(
		intent.BeforeVolumes, compass, target, action, foundation, bundle.References,
	)
	if err != nil {
		return receipt, err
	}
	if visibleDigest != intent.VisibleContextDigest || len(visibleRaw) != intent.VisibleContextBytes {
		return receipt, fmt.Errorf("outline-all operation %d model-visible context drifted", action.Operation)
	}
	if currentDigest == action.BeforeLayeredDigest {
		prompt, err := pipelineOutlineAllOperationPrompt(current, compass, target, action, visibleContext, visibleRaw)
		if err != nil {
			return receipt, err
		}
		if err := runPipelineOutlineAllArchitect(cfg, bundle, prompt); err != nil {
			return receipt, fmt.Errorf("outline-all operation %d architect: %w", action.Operation, err)
		}
		if err := validatePipelineOutlineAllCandidateNamespace(live.Dir(), st.Dir(), receipt.AttemptID, true); err != nil {
			return receipt, err
		}
		receipt, err = refreshPipelineOutlineAllLeases(live, st, owner, modelDigest, receipt)
		if err != nil {
			return receipt, err
		}
		current, err = st.Outline.LoadLayeredOutline()
		if err != nil {
			return receipt, err
		}
		currentDigest, err = domain.ComputeLayeredOutlineDigest(current)
		if err != nil {
			return receipt, err
		}
	}
	if currentDigest == action.BeforeLayeredDigest {
		return receipt, fmt.Errorf("outline-all operation %d returned without its one authorized mutation", action.Operation)
	}
	if err := validatePipelineOutlineAllMutation(intent.BeforeVolumes, current, action, compass, target); err != nil {
		return receipt, fmt.Errorf("outline-all operation %d exact delta invalid: %w", action.Operation, err)
	}
	derivedFlatDigest, err := repairPipelineOutlineAllDerivedArtifacts(st, current)
	if err != nil {
		return receipt, fmt.Errorf("outline-all operation %d repair derived artifacts: %w", action.Operation, err)
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, current); err != nil {
		return receipt, err
	}
	receiptPath := filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineAllOperationReceiptPath(action.Operation)))
	if raw, err := os.ReadFile(receiptPath); err == nil {
		var existing pipelineOutlineAllOperationReceipt
		if err := json.Unmarshal(raw, &existing); err != nil {
			return receipt, err
		}
		storedDigest := existing.ReceiptDigest
		validated, signErr := signPipelineOutlineAllOperationReceipt(existing)
		if signErr != nil || validated.ReceiptDigest != storedDigest ||
			existing.Version != "outline-all-operation-receipt.v1" ||
			existing.AttemptID != receipt.AttemptID ||
			!domain.OutlineAllPendingActionEqual(existing.Action, action) ||
			existing.IntentDigest != intent.IntentDigest ||
			existing.ContextRoot != intent.ContextRoot ||
			existing.VisibleContextDigest != intent.VisibleContextDigest ||
			existing.VisibleContextBytes != intent.VisibleContextBytes ||
			existing.BeforeLayeredDigest != action.BeforeLayeredDigest ||
			existing.AfterLayeredDigest != currentDigest ||
			existing.DerivedFlatDigest != derivedFlatDigest ||
			existing.ModelIdentityDigest != modelDigest || existing.PromptProtocolDigest != promptDigest {
			return receipt, fmt.Errorf("outline-all operation %d existing receipt is invalid or drifted", action.Operation)
		}
	} else if os.IsNotExist(err) {
		opReceipt := pipelineOutlineAllOperationReceipt{
			Version: "outline-all-operation-receipt.v1", AttemptID: receipt.AttemptID,
			Action: action, IntentDigest: intent.IntentDigest,
			ContextRoot:          intent.ContextRoot,
			VisibleContextDigest: intent.VisibleContextDigest,
			VisibleContextBytes:  intent.VisibleContextBytes,
			BeforeLayeredDigest:  action.BeforeLayeredDigest, AfterLayeredDigest: currentDigest,
			DerivedFlatDigest:   derivedFlatDigest,
			ModelIdentityDigest: modelDigest, PromptProtocolDigest: promptDigest,
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		opReceipt, err = signPipelineOutlineAllOperationReceipt(opReceipt)
		if err != nil {
			return receipt, err
		}
		if _, err := writePipelinePlanningJSON(receiptPath, opReceipt); err != nil {
			return receipt, err
		}
	} else {
		return receipt, err
	}
	if err := validatePipelineOutlineAllCandidateNamespace(live.Dir(), st.Dir(), receipt.AttemptID, true); err != nil {
		return receipt, err
	}
	return st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(currentReceipt *domain.OutlineAllExecutionReceipt) error {
		if currentReceipt.PendingAction == nil || !domain.OutlineAllPendingActionEqual(*currentReceipt.PendingAction, action) {
			return fmt.Errorf("outline-all pending action changed before completion checkpoint")
		}
		currentReceipt.PendingAction = nil
		currentReceipt.CompletedActionCount = action.Operation
		currentReceipt.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func pipelineOutlineAllOperationPrompt(
	volumes []domain.VolumeOutline,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
	action domain.OutlineAllPendingAction,
	visible pipelineOutlineAllModelVisibleContext,
	visibleRaw []byte,
) (string, error) {
	marker, err := domain.FormatOutlineAllIntent(action)
	if err != nil {
		return "", err
	}
	registry := pipelineOutlineAllContractRegistry(compass)
	registryJSON, _ := json.MarshalIndent(registry, "", "  ")
	var b strings.Builder
	b.WriteString("[PIPELINE OUTLINE-ALL / SINGLE MUTATION]\n")
	b.WriteString("宿主已将本次冻结 operation 直接交给 Architect 主模型；完整执行下面唯一的 OUTLINE_ALL_INTENT，不得转派。\n")
	b.WriteString(marker + "\n")
	b.WriteString("本轮只允许一次 save_foundation 结构变更；禁止更换模型/降级，禁止写正文、world_tick、user_rules、compass、全量 outline/layered_outline 或任何其他设定。\n")
	contextRoot := pipelineOutlineAllOperationContextRoot(visible.FoundationContextRoot, action.BeforeLayeredDigest)
	if visible.FoundationContextRoot == "" || contextRoot == "" || len(visibleRaw) == 0 {
		return "", fmt.Errorf("outline-all operation frozen context is incomplete")
	}
	fmt.Fprintf(&b, "OUTLINE_ALL_CONTEXT_ROOT %s\n", contextRoot)
	fmt.Fprintf(&b, "OUTLINE_ALL_VISIBLE_CONTEXT digest=%s bytes=%d\n", pipelineBytesSHA(visibleRaw), len(visibleRaw))
	b.WriteString("以下 MODEL_VISIBLE_CONTEXT 由宿主按 intent.before_layered_digest 冻结：包含全书每个弧的卷题/主题/目标/跨度/合同、目标弧全部章节、相邻弧首尾边界、冻结设定、权威 brief 与静态 RAG 写作参考。完整 layered_outline 只以 digest 绑定，禁止调用 novel_context/craft_recall/web_research 或补入外来事实。\nMODEL_VISIBLE_CONTEXT:\n")
	b.Write(visibleRaw)
	b.WriteString("\n")
	fmt.Fprintf(&b, "冻结目标：%d 卷 / %d 章；本次 operation=%d。contract_refs 是结构回执，不得把 source 原句机械复制进 core_event/hook。\n", target.TargetVolumes, target.TargetChapters, action.Operation)
	b.WriteString("指南针合同 registry（ref 对象必须逐字原样放入 contract_refs，source 只用于语义设计）：\n")
	b.Write(registryJSON)
	b.WriteString("\n")
	switch action.Type {
	case domain.OutlineAllActionAppendVolume:
		fmt.Fprintf(&b, "只调用 save_foundation(type=\"append_volume\", volume=%d, content=<VolumeOutline>)。追加且只追加第 %d 卷，本卷总预留必须恰好 %d 章。弧数与顺序跨度必须逐项严格等于 [%s]；每弧必须 chapters=[] 且 estimated_chapters 为对应值（每弧硬限制 %d-%d 章），此阶段只搭完整卷弧骨架，禁止提前展开任何弧。\n", action.Volume, action.ExpectedVolumeIndex, action.ExpectedChapterSpan, action.ExpectedArcSpans, domain.OutlineAllMinArcChapters, domain.OutlineAllMaxArcChapters)
		b.WriteString("本操作的所有 arc.contract_refs 必须为空；全书骨架齐全后会有独立 map_contracts 操作统一分配，禁止提前占用合同。\n")
		if action.FinalSkeleton {
			fmt.Fprintf(&b, "这是最终骨架卷：末弧的 goal 必须在语义上为第 %d 章完成 ending_direction 留出充足行动与因果空间，但 contract_refs 仍保持空。\n", target.TargetChapters)
		} else {
			b.WriteString("本卷要与前后卷形成清晰的因果升级，但不做 contract ref 分配。\n")
		}
	case domain.OutlineAllActionMapContracts:
		arcMap := pipelineOutlineAllArcMap(volumes)
		arcMapJSON, _ := json.MarshalIndent(arcMap, "", "  ")
		fmt.Fprintf(&b, "只调用 save_foundation(type=\"map_contracts\", content=<ArcContractAssignment数组>)。数组必须为全书每个弧各提供且只提供一项 {volume,arc,contract_refs}，空分配也必须显式列出。不得改 title/goal/span/chapters。\n")
		fmt.Fprintf(&b, "每个 registry ref 全书必须恰好出现一次；planned_payoff_chapter 必须落在所分配弧的闭区间内；planned_resolution 必须为至少18个有效字的具体‘行动者+行动+终态’，不得占位且各合同互异。ending/non_negotiable 必须全部分配给末弧，planned_payoff_chapter=%d。open_thread 按真实因果回收点唯一分配。\n全书弧区间：\n", target.TargetChapters)
		b.Write(arcMapJSON)
		b.WriteString("\n")
	case domain.OutlineAllActionExpandArc, domain.OutlineAllActionReviseArc:
		arc, start, err := locatePipelineOutlineAllArc(volumes, action.Volume, action.Arc)
		if err != nil {
			return "", err
		}
		arcJSON, _ := json.MarshalIndent(arc, "", "  ")
		verb := string(action.Type)
		fmt.Fprintf(&b, "目标弧 V%dA%d，全局章号 %d-%d，固定 span=%d。只调用 save_foundation(type=\"%s\", volume=%d, arc=%d, content=<%d个 OutlineEntry>)。\n", action.Volume, action.Arc, start, start+action.ExpectedChapterSpan-1, action.ExpectedChapterSpan, verb, action.Volume, action.Arc, action.ExpectedChapterSpan)
		b.WriteString("每章必须：唯一且具体的 title；core_event 写清行动者+阻力+选择+状态变化；hook 是可执行后果；scenes 至少3条可直接阅读的场景句，严禁JSON字符串壳、待细化、重复金句/通用悬念。\n")
		b.WriteString("弧上 contract_refs（含 planned_resolution）必须逐字段原样复制到各自 planned_payoff_chapter 对应的那一个 OutlineEntry.contract_refs 中，各出现且只出现一次；core_event/scenes 必须真正落实该 planned_resolution 的行动与终态，其他章不得携带。\n目标弧当前合同：\n")
		b.Write(arcJSON)
		b.WriteString("\n")
	}
	b.WriteString("工具明确返回 saved=true 后立即结束，不再调用第二个写工具。")
	return b.String(), nil
}

type pipelineOutlineAllContractSource struct {
	Ref    domain.StoryContractRef `json:"ref"`
	Source string                  `json:"source"`
}

type pipelineOutlineAllArcRange struct {
	Volume       int                       `json:"volume"`
	VolumeTitle  string                    `json:"volume_title"`
	VolumeTheme  string                    `json:"volume_theme"`
	Arc          int                       `json:"arc"`
	Title        string                    `json:"title"`
	Goal         string                    `json:"goal"`
	Start        int                       `json:"start_chapter"`
	End          int                       `json:"end_chapter"`
	Expanded     bool                      `json:"expanded"`
	ContractRefs []domain.StoryContractRef `json:"contract_refs,omitempty"`
}

func pipelineOutlineAllArcMap(volumes []domain.VolumeOutline) []pipelineOutlineAllArcRange {
	cursor := 1
	var result []pipelineOutlineAllArcRange
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			span := arc.ChapterSpan()
			result = append(result, pipelineOutlineAllArcRange{
				Volume: volume.Index, VolumeTitle: pipelineOutlineAllBoundInline(volume.Title, 1024),
				VolumeTheme: pipelineOutlineAllBoundInline(volume.Theme, 2048),
				Arc:         arc.Index, Title: pipelineOutlineAllBoundInline(arc.Title, 1024),
				Goal:  pipelineOutlineAllBoundInline(arc.Goal, 2048),
				Start: cursor, End: cursor + span - 1, Expanded: arc.IsExpanded(),
				ContractRefs: append([]domain.StoryContractRef(nil), arc.ContractRefs...),
			})
			cursor += span
		}
	}
	return result
}

func pipelineOutlineAllContractRegistry(compass domain.StoryCompass) []pipelineOutlineAllContractSource {
	refs := domain.BuildStoryContractRegistry(compass)
	sources := make([]string, 0, len(refs))
	if strings.TrimSpace(compass.EndingDirection) != "" {
		sources = append(sources, compass.EndingDirection)
	}
	for _, source := range compass.NonNegotiables {
		if strings.TrimSpace(source) != "" {
			sources = append(sources, source)
		}
	}
	for _, source := range compass.OpenThreads {
		if strings.TrimSpace(source) != "" {
			sources = append(sources, source)
		}
	}
	out := make([]pipelineOutlineAllContractSource, 0, len(refs))
	for i, ref := range refs {
		source := ""
		if i < len(sources) {
			source = sources[i]
		}
		out = append(out, pipelineOutlineAllContractSource{Ref: ref, Source: source})
	}
	return out
}

func locatePipelineOutlineAllArc(volumes []domain.VolumeOutline, volume, arc int) (domain.ArcOutline, int, error) {
	cursor := 1
	for _, candidateVolume := range volumes {
		for _, candidateArc := range candidateVolume.Arcs {
			if candidateVolume.Index == volume && candidateArc.Index == arc {
				return candidateArc, cursor, nil
			}
			cursor += candidateArc.ChapterSpan()
		}
	}
	return domain.ArcOutline{}, 0, fmt.Errorf("outline-all target V%dA%d not found", volume, arc)
}

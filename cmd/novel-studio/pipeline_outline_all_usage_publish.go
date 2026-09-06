package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func copyPipelineOutlineAllUsageForPublish(liveOutputDir, candidateDir string) (*store.UsageAuditSnapshotCopyProof, error) {
	live, candidate := store.NewStore(liveOutputDir), store.NewStore(candidateDir)
	snapshot, err := live.Usage.Load()
	if err != nil {
		return nil, err
	}
	journalInfo, statErr := os.Stat(filepath.Join(liveOutputDir, store.UsageAuditPath))
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	// Direct operations already flush live. Avoid rewriting a complete usage
	// snapshot's timestamp on a publish retry; only recover an absent/lagging
	// snapshot or establish an older live book's initial WAL baseline.
	if snapshot == nil || os.IsNotExist(statErr) || snapshot.AuditOffset != journalInfo.Size() {
		meter, err := host.NewDurableUsageMeter(live)
		if err != nil {
			return nil, err
		}
		if err := meter.Flush(); err != nil {
			return nil, err
		}
	}
	// A resumable older candidate may contain paid calls that were never sent
	// to live. Do not erase that evidence or guess how its baseline overlaps.
	if err := validatePipelineOutlineAllCandidateUsagePrefix(liveOutputDir, candidateDir); err != nil {
		return nil, err
	}
	return live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage)
}

func validatePipelineOutlineAllCandidateUsagePrefix(liveOutputDir, candidateDir string) error {
	raw, err := os.ReadFile(filepath.Join(candidateDir, "meta/usage.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot domain.UsageState
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("outline candidate accounting is unreadable; preserve it for explicit audit: %w", err)
	}
	journal, err := os.ReadFile(filepath.Join(candidateDir, store.UsageAuditPath))
	if os.IsNotExist(err) {
		if snapshot.Overall.Input == 0 && snapshot.Overall.Output == 0 && snapshot.Overall.Cost == 0 && snapshot.MissingUsage == 0 && len(snapshot.AccountedUsageIDs) == 0 && len(snapshot.PendingUsageCalls) == 0 && snapshot.AuditOffset == 0 {
			return nil
		}
		return fmt.Errorf("outline candidate contains unimported legacy accounting; preserve its usage/session evidence and explicitly reconcile it before publishing, never add snapshots blindly")
	}
	if err != nil {
		return err
	}
	liveJournal, err := os.ReadFile(filepath.Join(liveOutputDir, store.UsageAuditPath))
	if err != nil {
		return err
	}
	if len(journal) == 0 || !bytes.HasPrefix(liveJournal, journal) {
		return fmt.Errorf("outline candidate accounting is not a prefix of the authoritative live WAL; preserve it for explicit reconciliation")
	}
	return nil
}

// Must run after ExpectedLiveRoot has been computed. Changes before that root
// are caught here; changes after it are caught by directory-publication CAS.
func verifyPipelineOutlineAllUsageCopy(liveOutputDir string, proof *store.UsageAuditSnapshotCopyProof) error {
	if proof == nil {
		return fmt.Errorf("outline publish lacks audited usage copy proof")
	}
	live := store.NewStore(liveOutputDir)
	return live.Usage.WithAuditTransaction(func(tx *store.UsageAuditTransaction) error {
		state, err := tx.Load()
		if err != nil {
			return err
		}
		if state == nil || state.AuditOffset != proof.AuditOffset {
			return fmt.Errorf("outline publish live usage advanced after candidate accounting copy")
		}
		journal, end, err := tx.Read(0)
		if err != nil {
			return err
		}
		if end != proof.AuditOffset || pipelineBytesSHA(journal) != proof.JournalSHA256 {
			return fmt.Errorf("outline publish WAL changed after accounting copy")
		}
		raw, err := os.ReadFile(filepath.Join(liveOutputDir, "meta/usage.json"))
		if err != nil {
			return err
		}
		if pipelineBytesSHA(raw) != proof.SnapshotSHA256 {
			return fmt.Errorf("outline publish usage snapshot changed after accounting copy")
		}
		return nil
	})
}

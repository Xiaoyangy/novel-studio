package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type UsageAuditSnapshotCopyProof struct {
	AuditOffset    int64  `json:"audit_offset"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	JournalSHA256  string `json:"journal_sha256"`
}

// CopyAuditedSnapshotToCandidate replaces only the two accounting artifacts in
// an explicitly disposable staging candidate. It is not a merge, live import,
// or reconciliation of two independent ledgers. Callers must flush the source
// DurableUsageMeter first and must not publish on any error.
//
// The old candidate snapshot is invalidated before replacing its WAL, so a
// crash cannot pair an old snapshot with a different journal prefix. A failed
// candidate must be recopied before publication. The source is never written,
// except for the normal advisory lock's creation when absent; its lock inode
// is never copied. Both accounting locks are acquired in canonical path order.
func (s *UsageStore) CopyAuditedSnapshotToCandidate(candidate *UsageStore) (*UsageAuditSnapshotCopyProof, error) {
	if s == nil || candidate == nil || s.io == nil || candidate.io == nil {
		return nil, fmt.Errorf("audited usage copy requires source and staging candidate stores")
	}
	sourceRoot, err := usageCopyRoot(s.io.dir)
	if err != nil {
		return nil, fmt.Errorf("usage copy source: %w", err)
	}
	targetRoot, err := usageCopyRoot(candidate.io.dir)
	if err != nil {
		return nil, fmt.Errorf("usage copy candidate: %w", err)
	}
	if sourceRoot == targetRoot || strings.HasPrefix(sourceRoot, targetRoot+string(filepath.Separator)) || strings.HasPrefix(targetRoot, sourceRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("usage copy source and candidate must be distinct, non-nested directories")
	}
	paths := []string{"meta/usage.json", UsageAuditPath, "meta/runtime/usage_audit.lock"}
	for _, io := range []*IO{s.io, candidate.io} {
		for _, rel := range paths {
			if err := validateUsageCopyFilePath(io, rel); err != nil {
				return nil, err
			}
		}
	}
	sourceLock, sourceLockErr := os.Lstat(s.io.path("meta/runtime/usage_audit.lock"))
	targetLock, targetLockErr := os.Lstat(candidate.io.path("meta/runtime/usage_audit.lock"))
	if sourceLockErr == nil && targetLockErr == nil && os.SameFile(sourceLock, targetLock) {
		return nil, fmt.Errorf("usage copy source and candidate cannot share an accounting lock inode")
	}
	var proof *UsageAuditSnapshotCopyProof
	copyLocked := func(sourceTx *UsageAuditTransaction) error {
		// Recheck after lock acquisition: a bad candidate path must not be
		// followed or overwritten merely because it passed an earlier check.
		for _, io := range []*IO{s.io, candidate.io} {
			for _, rel := range paths {
				if err := validateUsageCopyFilePath(io, rel); err != nil {
					return err
				}
			}
		}
		state, err := sourceTx.Load()
		if err != nil {
			return fmt.Errorf("load audited usage copy source: %w", err)
		}
		if state == nil {
			return fmt.Errorf("usage copy requires an existing audited source snapshot")
		}
		journal, end, err := sourceTx.Read(0)
		if err != nil {
			return err
		}
		if end == 0 || state.AuditOffset != end {
			return fmt.Errorf("usage copy source snapshot is not replayed through WAL end; flush the durable meter first")
		}
		snapshot, err := s.io.ReadFile("meta/usage.json")
		if err != nil {
			return err
		}
		result := &UsageAuditSnapshotCopyProof{AuditOffset: end, SnapshotSHA256: usageCopySHA(snapshot), JournalSHA256: usageCopySHA(journal)}
		oldSnapshot, snapshotErr := candidate.io.ReadFile("meta/usage.json")
		if snapshotErr != nil && !os.IsNotExist(snapshotErr) {
			return snapshotErr
		}
		oldJournal, journalErr := candidate.io.ReadFile(UsageAuditPath)
		if journalErr != nil && !os.IsNotExist(journalErr) {
			return journalErr
		}
		if !bytes.Equal(snapshot, oldSnapshot) || !bytes.Equal(journal, oldJournal) {
			if err := os.Remove(candidate.io.path("meta/usage.json")); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := syncUsageDirectory(candidate.io.path("meta")); err != nil {
				return err
			}
			if err := candidate.io.WriteFileUnlocked(UsageAuditPath, journal); err != nil {
				return err
			}
			if err := syncUsageDirectory(candidate.io.path("meta/runtime")); err != nil {
				return err
			}
			if err := candidate.io.WriteFileUnlocked("meta/usage.json", snapshot); err != nil {
				return err
			}
		}
		// Exact retries also finish a directory sync that may have failed
		// after a prior successful rename.
		if err := syncUsageDirectory(candidate.io.path("meta/runtime")); err != nil {
			return err
		}
		if err := syncUsageDirectory(candidate.io.path("meta")); err != nil {
			return err
		}
		copiedSnapshot, err := candidate.io.ReadFile("meta/usage.json")
		if err != nil {
			return err
		}
		copiedJournal, err := candidate.io.ReadFile(UsageAuditPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(snapshot, copiedSnapshot) || !bytes.Equal(journal, copiedJournal) {
			return fmt.Errorf("staging usage copy does not match the exact source snapshot/WAL pair")
		}
		proof = result
		return nil
	}
	if sourceRoot < targetRoot {
		err = s.WithAuditTransaction(func(sourceTx *UsageAuditTransaction) error {
			return candidate.WithAuditTransaction(func(*UsageAuditTransaction) error { return copyLocked(sourceTx) })
		})
	} else {
		err = candidate.WithAuditTransaction(func(*UsageAuditTransaction) error {
			return s.WithAuditTransaction(copyLocked)
		})
	}
	if err != nil {
		return nil, err
	}
	return proof, nil
}

func usageCopySHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func usageCopyRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("usage copy root must be an existing nonsymlink directory")
	}
	// Resolve parent aliases (including platform /var -> /private/var)
	// before ordering locks or detecting source/target overlap.
	return filepath.EvalSymlinks(abs)
}

func validateUsageCopyFilePath(io *IO, rel string) error {
	parts := strings.Split(rel, string(filepath.Separator))
	current := io.dir
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return fmt.Errorf("usage copy rejects symlink or nonregular managed path %s", rel)
		}
	}
	return nil
}

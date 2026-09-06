package store

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const UsageAuditPath = "meta/runtime/usage_audit.jsonl"

// UsageAuditTransaction serializes WAL append + snapshot publication across
// handles and processes. The caller must not retain tx after the callback.
type UsageAuditTransaction struct{ store *UsageStore }

func (s *UsageStore) WithAuditTransaction(fn func(*UsageAuditTransaction) error) error {
	root := s.io.path("meta/runtime")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "usage_audit.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn(&UsageAuditTransaction{store: s})
}

func (tx *UsageAuditTransaction) Load() (*domain.UsageState, error) { return tx.store.Load() }

// Read returns exactly the suffix following a previously committed newline.
// Partial trailing records are preserved on disk and rejected, never truncated.
func (tx *UsageAuditTransaction) Read(offset int64) ([]byte, int64, error) {
	if offset < 0 {
		return nil, 0, fmt.Errorf("usage audit offset is negative")
	}
	f, err := os.Open(tx.store.io.path(UsageAuditPath))
	if os.IsNotExist(err) {
		if offset != 0 {
			return nil, 0, fmt.Errorf("usage audit is missing at snapshot offset %d", offset)
		}
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || offset > info.Size() {
		return nil, 0, fmt.Errorf("usage audit offset %d exceeds regular journal size %d", offset, info.Size())
	}
	if offset > 0 {
		var boundary [1]byte
		if _, err := f.ReadAt(boundary[:], offset-1); err != nil || boundary[0] != '\n' {
			return nil, 0, fmt.Errorf("usage audit offset %d is not a record boundary", offset)
		}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		return nil, 0, fmt.Errorf("usage audit has an incomplete trailing record; journal preserved")
	}
	return raw, info.Size(), nil
}

func (tx *UsageAuditTransaction) Append(expectedOffset int64, payload []byte) (int64, error) {
	if len(payload) == 0 || bytes.ContainsAny(payload, "\r\n") {
		return 0, fmt.Errorf("usage audit requires one nonempty JSON line")
	}
	_, size, err := tx.Read(expectedOffset)
	if err != nil {
		return 0, err
	}
	if size != expectedOffset {
		return 0, fmt.Errorf("usage audit append offset changed")
	}
	f, err := os.OpenFile(tx.store.io.path(UsageAuditPath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	line := append(append([]byte(nil), payload...), '\n')
	n, writeErr := f.Write(line)
	if writeErr == nil && n != len(line) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return 0, writeErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err := syncUsageDirectory(filepath.Dir(tx.store.io.path(UsageAuditPath))); err != nil {
		return 0, err
	}
	return expectedOffset + int64(len(line)), nil
}

func (tx *UsageAuditTransaction) Save(state domain.UsageState) error {
	state.Schema = domain.UsageSchemaVersion
	_, end, err := tx.Read(state.AuditOffset)
	if err != nil {
		return err
	}
	if end != state.AuditOffset {
		return fmt.Errorf("usage snapshot must replay audit tail before saving")
	}
	previous, err := tx.Load()
	if err != nil {
		return err
	}
	if previous != nil {
		if previous.AuditOffset > state.AuditOffset {
			return fmt.Errorf("usage snapshot would rewind audit offset")
		}
		if previous.AuditOffset == state.AuditOffset {
			if len(previous.PendingUsageCalls) != len(state.PendingUsageCalls) {
				return fmt.Errorf("usage snapshot cannot change pending calls without advancing the audit offset")
			}
			for id, start := range previous.PendingUsageCalls {
				if current, exists := state.PendingUsageCalls[id]; !exists || current != start {
					return fmt.Errorf("usage snapshot cannot rewrite pending call identity without a journal event")
				}
			}
		}
		for id, digest := range previous.AccountedUsageIDs {
			if state.AccountedUsageIDs[id] != digest {
				return fmt.Errorf("usage snapshot would drop or rewrite accounted usage id %q", id)
			}
		}
		if state.AuditOffset > 0 && (state.Overall.Input < previous.Overall.Input || state.Overall.Output < previous.Overall.Output || state.Overall.CacheRead < previous.Overall.CacheRead || state.Overall.CacheWrite < previous.Overall.CacheWrite || state.Overall.Cost < previous.Overall.Cost || state.MissingUsage < previous.MissingUsage) {
			return fmt.Errorf("usage snapshot would discard already persisted accounting totals")
		}
	}
	if err := tx.store.io.WriteJSON("meta/usage.json", state); err != nil {
		return err
	}
	return syncUsageDirectory(tx.store.io.path("meta"))
}

func syncUsageDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

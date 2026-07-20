package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	chapterRenderTransactionRootName = ".render-transactions"
	chapterRenderTransactionLockName = ".write.lock"
	chapterRenderTransactionBodyName = "body.md"
)

// syscall.Flock provides the cross-process boundary. The process mutex is
// still required because separate Store instances do not share an IO mutex and
// flock semantics for separate descriptors in one process vary by platform.
var chapterRenderTransactionProcessMu sync.RWMutex

// ChapterRenderTransactionStore owns immutable body bytes and the monotonic
// receipt chain for sealed chapter rendering. Its root is deliberately a
// namespaced sibling of outputDir so an atomic candidate->live directory rename
// cannot move or hide transaction evidence, while two live output clones under
// the same parent cannot accidentally adopt each other's receipts.
type ChapterRenderTransactionStore struct {
	root    string
	initErr error
	now     func() time.Time
}

func NewChapterRenderTransactionStore(outputDir string) *ChapterRenderTransactionStore {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return &ChapterRenderTransactionStore{
			initErr: fmt.Errorf("chapter render transaction output directory is empty"),
			now:     time.Now,
		}
	}
	absolute, err := filepath.Abs(outputDir)
	if err != nil {
		return &ChapterRenderTransactionStore{initErr: err, now: time.Now}
	}
	absolute = filepath.Clean(absolute)
	projectNamespace := sha256.Sum256([]byte(absolute))
	return &ChapterRenderTransactionStore{
		root: filepath.Join(
			filepath.Dir(absolute),
			chapterRenderTransactionRootName,
			fmt.Sprintf("%x", projectNamespace[:]),
		),
		now: time.Now,
	}
}

func (s *Store) ChapterRenderTransactions() *ChapterRenderTransactionStore {
	return NewChapterRenderTransactionStore(s.dir)
}

func (s *ChapterRenderTransactionStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *ChapterRenderTransactionStore) BeginBody(
	identity domain.ChapterRenderBodyIdentity,
	body []byte,
	evidence domain.ChapterRenderPhaseEvidence,
) (*domain.ChapterRenderPhaseReceipt, error) {
	if s == nil {
		return nil, fmt.Errorf("chapter render transaction store is nil")
	}
	if err := domain.ValidateChapterRenderBodyIdentity(identity); err != nil {
		return nil, err
	}
	if got := domain.ComputeChapterRenderBodySHA256(body); got != identity.BodySHA256 {
		return nil, fmt.Errorf("chapter render body sha mismatch: got %s want %s", got, identity.BodySHA256)
	}
	var receipt *domain.ChapterRenderPhaseReceipt
	err := s.withWriteLock(func() error {
		if err := s.ensureBodyUnlocked(identity, body); err != nil {
			return err
		}
		var err error
		receipt, err = s.advanceUnlocked(identity, domain.ChapterRenderPhaseBodyReady, evidence)
		return err
	})
	return receipt, err
}

func (s *ChapterRenderTransactionStore) Advance(
	identity domain.ChapterRenderBodyIdentity,
	phase domain.ChapterRenderPhase,
	evidence domain.ChapterRenderPhaseEvidence,
) (*domain.ChapterRenderPhaseReceipt, error) {
	if s == nil {
		return nil, fmt.Errorf("chapter render transaction store is nil")
	}
	if err := domain.ValidateChapterRenderBodyIdentity(identity); err != nil {
		return nil, err
	}
	var receipt *domain.ChapterRenderPhaseReceipt
	err := s.withWriteLock(func() error {
		if _, err := s.loadAndVerifyBodyUnlocked(identity); err != nil {
			return fmt.Errorf("advance chapter render transaction requires immutable body: %w", err)
		}
		var err error
		receipt, err = s.advanceUnlocked(identity, phase, evidence)
		return err
	})
	return receipt, err
}

func (s *ChapterRenderTransactionStore) LoadReceipts(
	identity domain.ChapterRenderBodyIdentity,
) ([]domain.ChapterRenderPhaseReceipt, error) {
	if s == nil {
		return nil, fmt.Errorf("chapter render transaction store is nil")
	}
	if err := domain.ValidateChapterRenderBodyIdentity(identity); err != nil {
		return nil, err
	}
	var receipts []domain.ChapterRenderPhaseReceipt
	err := s.withReadLock(func() error {
		var err error
		receipts, err = s.loadReceiptsUnlocked(identity)
		return err
	})
	return receipts, err
}

func (s *ChapterRenderTransactionStore) LoadLatest(
	identity domain.ChapterRenderBodyIdentity,
) (*domain.ChapterRenderPhaseReceipt, error) {
	receipts, err := s.LoadReceipts(identity)
	if err != nil || len(receipts) == 0 {
		return nil, err
	}
	latest := receipts[len(receipts)-1]
	return &latest, nil
}

// ListPlanBodies returns every independently validated body identity already
// materialized for one plan attempt. Directory names alone have no authority:
// each result must have an exact body and a valid receipt chain whose embedded
// plan identity matches the requested plan.
func (s *ChapterRenderTransactionStore) ListPlanBodies(
	plan domain.ChapterRenderPlanIdentity,
) ([]domain.ChapterRenderBodyIdentity, error) {
	if s == nil {
		return nil, fmt.Errorf("chapter render transaction store is nil")
	}
	planID, err := domain.ComputeChapterRenderPlanAttemptID(plan)
	if err != nil {
		return nil, err
	}
	var identities []domain.ChapterRenderBodyIdentity
	err = s.withReadLock(func() error {
		root := filepath.Join(s.root, "plans", planID, "bodies")
		entries, readErr := os.ReadDir(root)
		if os.IsNotExist(readErr) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				return fmt.Errorf("invalid chapter render body transaction entry %s", entry.Name())
			}
			txnDir := filepath.Join(root, entry.Name())
			phaseEntries, err := os.ReadDir(txnDir)
			if err != nil {
				return err
			}
			var firstPath string
			for _, phaseEntry := range phaseEntries {
				if phaseEntry.Name() == chapterRenderPhaseFileName(domain.ChapterRenderPhaseBodyReady) {
					firstPath = filepath.Join(txnDir, phaseEntry.Name())
					break
				}
			}
			if firstPath == "" {
				// BeginBody publishes and fsyncs body.md before it can publish the
				// body_ready receipt. A crash at that exact boundary leaves a safe,
				// content-addressed orphan. It has no phase authority, so omit it from
				// the result while allowing the next exact BeginBody to finish the
				// receipt idempotently. Any other durable entry remains corruption.
				for _, phaseEntry := range phaseEntries {
					if phaseEntry.Name() == chapterRenderTransactionBodyName ||
						(strings.HasPrefix(phaseEntry.Name(), ".") && strings.Contains(phaseEntry.Name(), ".tmp-")) {
						continue
					}
					return fmt.Errorf("chapter render body transaction %s has artifacts but no body_ready receipt", entry.Name())
				}
				bodyPath := filepath.Join(txnDir, chapterRenderTransactionBodyName)
				info, statErr := os.Lstat(bodyPath)
				if statErr != nil {
					return fmt.Errorf("chapter render body transaction %s stat orphan body: %w", entry.Name(), statErr)
				}
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("chapter render body transaction %s has invalid orphan body type", entry.Name())
				}
				body, readErr := os.ReadFile(bodyPath)
				if readErr != nil {
					return readErr
				}
				orphanIdentity, identityErr := domain.NewChapterRenderBodyIdentity(
					plan,
					domain.ComputeChapterRenderBodySHA256(body),
				)
				if identityErr != nil {
					return fmt.Errorf("chapter render body transaction %s has invalid orphan body identity: %w", entry.Name(), identityErr)
				}
				if orphanIdentity.TransactionID != entry.Name() {
					return fmt.Errorf("chapter render body transaction %s has mismatched orphan body identity", entry.Name())
				}
				continue
			}
			var first domain.ChapterRenderPhaseReceipt
			if err := readChapterRenderJSONStrict(firstPath, &first); err != nil {
				return err
			}
			if first.Identity.Plan != plan || first.Identity.PlanAttemptID != planID ||
				first.Identity.TransactionID != entry.Name() {
				return fmt.Errorf("chapter render body transaction %s has mismatched embedded identity", entry.Name())
			}
			if _, err := s.loadReceiptsUnlocked(first.Identity); err != nil {
				return err
			}
			identities = append(identities, first.Identity)
		}
		sort.Slice(identities, func(i, j int) bool {
			return identities[i].TransactionID < identities[j].TransactionID
		})
		return nil
	})
	return identities, err
}

func (s *ChapterRenderTransactionStore) LoadBody(
	identity domain.ChapterRenderBodyIdentity,
) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("chapter render transaction store is nil")
	}
	if err := domain.ValidateChapterRenderBodyIdentity(identity); err != nil {
		return nil, err
	}
	var body []byte
	err := s.withReadLock(func() error {
		var err error
		body, err = s.loadAndVerifyBodyUnlocked(identity)
		return err
	})
	return body, err
}

func (s *ChapterRenderTransactionStore) advanceUnlocked(
	identity domain.ChapterRenderBodyIdentity,
	phase domain.ChapterRenderPhase,
	evidence domain.ChapterRenderPhaseEvidence,
) (*domain.ChapterRenderPhaseReceipt, error) {
	receipts, err := s.loadReceiptsUnlocked(identity)
	if err != nil {
		return nil, err
	}
	evidence.ReviewArtifacts = domain.CanonicalChapterRenderArtifacts(evidence.ReviewArtifacts)
	for i := range receipts {
		existing := receipts[i]
		if existing.Phase != phase {
			continue
		}
		if existing.Identity == identity && reflect.DeepEqual(existing.Evidence, evidence) {
			return &existing, nil
		}
		return nil, fmt.Errorf(
			"chapter render phase %s already exists with different evidence",
			phase,
		)
	}

	previousPhase := domain.ChapterRenderPhase("")
	previousDigest := ""
	if len(receipts) > 0 {
		latest := receipts[len(receipts)-1]
		previousPhase = latest.Phase
		previousDigest = latest.ReceiptDigest
	}
	if err := domain.ValidateChapterRenderPhaseTransition(previousPhase, phase); err != nil {
		return nil, err
	}
	receipt, err := domain.SignChapterRenderPhaseReceipt(domain.ChapterRenderPhaseReceipt{
		Version:               domain.ChapterRenderPhaseReceiptVersion,
		Identity:              identity,
		Phase:                 phase,
		PreviousReceiptDigest: previousDigest,
		Evidence:              evidence,
		CreatedAt:             s.now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	rel := s.phaseRelativePath(identity, phase)
	if err := s.writeImmutableJSONUnlocked(rel, receipt); err != nil {
		return nil, err
	}
	// Read back through the full chain validator. This catches an impossible
	// path/content mismatch before the caller treats the phase as durable.
	refreshed, err := s.loadReceiptsUnlocked(identity)
	if err != nil {
		return nil, err
	}
	for i := range refreshed {
		if refreshed[i].Phase == phase {
			copy := refreshed[i]
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("chapter render phase %s disappeared after write", phase)
}

func (s *ChapterRenderTransactionStore) loadReceiptsUnlocked(
	identity domain.ChapterRenderBodyIdentity,
) ([]domain.ChapterRenderPhaseReceipt, error) {
	txnDir := s.transactionDir(identity)
	entries, err := os.ReadDir(txnDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.loadAndVerifyBodyUnlocked(identity); err != nil {
		return nil, fmt.Errorf("verify chapter render transaction body: %w", err)
	}
	receipts := make([]domain.ChapterRenderPhaseReceipt, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == chapterRenderTransactionBodyName {
			continue
		}
		// A process can die after syncing a temporary file but before hard-link
		// publication. Dot-prefixed temps have no authority and are ignored.
		if strings.HasPrefix(entry.Name(), ".") && strings.Contains(entry.Name(), ".tmp-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("invalid chapter render transaction entry %s", filepath.Join(txnDir, entry.Name()))
		}
		var receipt domain.ChapterRenderPhaseReceipt
		if err := readChapterRenderJSONStrict(filepath.Join(txnDir, entry.Name()), &receipt); err != nil {
			return nil, err
		}
		if err := domain.ValidateChapterRenderPhaseReceipt(receipt); err != nil {
			return nil, fmt.Errorf("invalid chapter render phase receipt %s: %w", entry.Name(), err)
		}
		if receipt.Identity != identity {
			return nil, fmt.Errorf("chapter render phase receipt %s has different body identity", entry.Name())
		}
		if entry.Name() != chapterRenderPhaseFileName(receipt.Phase) {
			return nil, fmt.Errorf("chapter render receipt path/phase mismatch: %s/%s", entry.Name(), receipt.Phase)
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool {
		left := domain.ChapterRenderPhaseOrdinal(receipts[i].Phase)
		right := domain.ChapterRenderPhaseOrdinal(receipts[j].Phase)
		if left != right {
			return left < right
		}
		return receipts[i].Phase < receipts[j].Phase
	})
	previousPhase := domain.ChapterRenderPhase("")
	previousDigest := ""
	previousOrdinal := 0
	for i := range receipts {
		receipt := receipts[i]
		ordinal := domain.ChapterRenderPhaseOrdinal(receipt.Phase)
		if ordinal == previousOrdinal {
			return nil, fmt.Errorf("chapter render transaction has competing receipts at ordinal %d", ordinal)
		}
		if err := domain.ValidateChapterRenderPhaseTransition(previousPhase, receipt.Phase); err != nil {
			return nil, fmt.Errorf("invalid chapter render receipt chain: %w", err)
		}
		if receipt.PreviousReceiptDigest != previousDigest {
			return nil, fmt.Errorf("chapter render receipt %s does not bind its predecessor", receipt.Phase)
		}
		previousPhase = receipt.Phase
		previousDigest = receipt.ReceiptDigest
		previousOrdinal = ordinal
	}
	return receipts, nil
}

func (s *ChapterRenderTransactionStore) ensureBodyUnlocked(
	identity domain.ChapterRenderBodyIdentity,
	body []byte,
) error {
	path := filepath.Join(s.transactionDir(identity), chapterRenderTransactionBodyName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("chapter render body path is not a regular file")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, body) {
			return fmt.Errorf("chapter render body address already contains different bytes")
		}
		if domain.ComputeChapterRenderBodySHA256(existing) != identity.BodySHA256 {
			return fmt.Errorf("chapter render body address failed sha verification")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeChapterRenderFileNoReplace(path, body); err != nil {
		if os.IsExist(err) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, body) &&
				domain.ComputeChapterRenderBodySHA256(existing) == identity.BodySHA256 {
				return nil
			}
		}
		return err
	}
	return nil
}

func (s *ChapterRenderTransactionStore) loadAndVerifyBodyUnlocked(
	identity domain.ChapterRenderBodyIdentity,
) ([]byte, error) {
	path := filepath.Join(s.transactionDir(identity), chapterRenderTransactionBodyName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("chapter render body path is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if got := domain.ComputeChapterRenderBodySHA256(body); got != identity.BodySHA256 {
		return nil, fmt.Errorf("chapter render body sha drift: got %s want %s", got, identity.BodySHA256)
	}
	return body, nil
}

func (s *ChapterRenderTransactionStore) writeImmutableJSONUnlocked(rel string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.root, rel)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("immutable chapter render receipt path is not a regular file")
		}
		var existing domain.ChapterRenderPhaseReceipt
		if err := readChapterRenderJSONStrict(path, &existing); err != nil {
			return err
		}
		var wanted domain.ChapterRenderPhaseReceipt
		if err := decodeChapterRenderJSONStrict(raw, &wanted); err != nil {
			return err
		}
		if reflect.DeepEqual(existing, wanted) {
			return nil
		}
		return fmt.Errorf("immutable chapter render receipt already exists with different content")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeChapterRenderFileNoReplace(path, raw); err != nil {
		return err
	}
	return nil
}

func (s *ChapterRenderTransactionStore) transactionDir(
	identity domain.ChapterRenderBodyIdentity,
) string {
	return filepath.Join(
		s.root,
		"plans",
		identity.PlanAttemptID,
		"bodies",
		identity.TransactionID,
	)
}

func (s *ChapterRenderTransactionStore) phaseRelativePath(
	identity domain.ChapterRenderBodyIdentity,
	phase domain.ChapterRenderPhase,
) string {
	return filepath.Join(
		"plans",
		identity.PlanAttemptID,
		"bodies",
		identity.TransactionID,
		chapterRenderPhaseFileName(phase),
	)
}

func chapterRenderPhaseFileName(phase domain.ChapterRenderPhase) string {
	return fmt.Sprintf("%02d-%s.json", domain.ChapterRenderPhaseOrdinal(phase), phase)
}

func (s *ChapterRenderTransactionStore) withWriteLock(fn func() error) error {
	if s.initErr != nil {
		return s.initErr
	}
	chapterRenderTransactionProcessMu.Lock()
	defer chapterRenderTransactionProcessMu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.root, chapterRenderTransactionLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func (s *ChapterRenderTransactionStore) withReadLock(fn func() error) error {
	if s.initErr != nil {
		return s.initErr
	}
	chapterRenderTransactionProcessMu.RLock()
	defer chapterRenderTransactionProcessMu.RUnlock()
	if _, err := os.Lstat(s.root); os.IsNotExist(err) {
		return fn()
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.root, chapterRenderTransactionLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func writeChapterRenderFileNoReplace(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && syncErr != syscall.EINVAL && syncErr != syscall.ENOTSUP {
		return syncErr
	}
	return closeErr
}

func readChapterRenderJSONStrict(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decodeChapterRenderJSONStrict(raw, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func decodeChapterRenderJSONStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

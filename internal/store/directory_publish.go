package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	directoryPublishIntentVersion        = "directory-publish-intent.v1"
	directoryPublishReceiptVersion       = "directory-publish-receipt.v2"
	directoryPublishReceiptLegacyVersion = "directory-publish-receipt.v1"
	directoryPublishFinalizeVersion      = "directory-publish-finalize.v1"
	directoryPublishAbortVersion         = "directory-publish-abort.v1"
	directoryPublishAbortedVersion       = "directory-publish-aborted.v1"
	directoryPublishLockName             = ".directory-publish.lock"
	directoryPublishIntentName           = "intent.json"
	directoryPublishReceiptName          = "receipt.json"
	directoryPublishFinalizeName         = "finalized_receipt.json"
	directoryPublishAbortIntentName      = "abort_intent.json"
	directoryPublishAbortedName          = "aborted_receipt.json"
)

var directoryPublishTransactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var directoryAtomicTempNamePattern = regexp.MustCompile(`^(?:\.?[^/]+\.tmp-[0-9]+|\.[^/]+-[0-9]+\.tmp)$`)

var ErrDirectoryPublishAborted = errors.New("directory publish transaction was aborted")

type PublishDirectoryRequest struct {
	TransactionID    string
	LiveDir          string
	CandidateDir     string
	ExpectedLiveRoot string
}

type DirectoryPublishIntent struct {
	Version          string `json:"version"`
	TransactionID    string `json:"transaction_id"`
	LiveDir          string `json:"live_dir"`
	CandidateDir     string `json:"candidate_dir"`
	ArchiveDir       string `json:"archive_dir"`
	BeforeLiveRoot   string `json:"before_live_root"`
	CandidateRoot    string `json:"candidate_root"`
	ParametersDigest string `json:"parameters_digest"`
	CreatedAt        string `json:"created_at"`
	IntentDigest     string `json:"intent_digest"`
}

type DirectoryPublishReceipt struct {
	Version           string `json:"version"`
	TransactionID     string `json:"transaction_id"`
	IntentDigest      string `json:"intent_digest"`
	LiveDir           string `json:"live_dir"`
	CandidateDir      string `json:"candidate_dir,omitempty"`
	ArchiveDir        string `json:"archive_dir"`
	BeforeLiveRoot    string `json:"before_live_root"`
	CandidateRoot     string `json:"candidate_root"`
	CommittedLiveRoot string `json:"committed_live_root"`
	CommittedAt       string `json:"committed_at"`
	ReceiptDigest     string `json:"receipt_digest"`
}

type DirectoryPublishPhase string

const (
	DirectoryPublishIntentWritten     DirectoryPublishPhase = "intent_written"
	DirectoryPublishLiveArchived      DirectoryPublishPhase = "live_archived"
	DirectoryPublishCandidatePromoted DirectoryPublishPhase = "candidate_promoted"
	DirectoryPublishReceiptWritten    DirectoryPublishPhase = "receipt_written"
	DirectoryPublishFinalized         DirectoryPublishPhase = "finalized"
	DirectoryPublishAborted           DirectoryPublishPhase = "aborted"
)

type DirectoryPublishState struct {
	TransactionID string                   `json:"transaction_id"`
	Phase         DirectoryPublishPhase    `json:"phase"`
	Intent        *DirectoryPublishIntent  `json:"intent,omitempty"`
	Receipt       *DirectoryPublishReceipt `json:"receipt,omitempty"`
}

type directoryPublishFinalizeReceipt struct {
	Version        string `json:"version"`
	TransactionID  string `json:"transaction_id"`
	ReceiptDigest  string `json:"receipt_digest"`
	IntentDigest   string `json:"intent_digest"`
	LiveRoot       string `json:"live_root"`
	FinalizedAt    string `json:"finalized_at"`
	FinalizeDigest string `json:"finalize_digest"`
}

type directoryPublishAbortIntent struct {
	Version       string `json:"version"`
	TransactionID string `json:"transaction_id"`
	IntentDigest  string `json:"intent_digest"`
	RequestedAt   string `json:"requested_at"`
	AbortDigest   string `json:"abort_digest"`
}

type directoryPublishAbortedReceipt struct {
	Version       string `json:"version"`
	TransactionID string `json:"transaction_id"`
	IntentDigest  string `json:"intent_digest"`
	RestoredRoot  string `json:"restored_root"`
	AbortedAt     string `json:"aborted_at"`
	ReceiptDigest string `json:"receipt_digest"`
}

type DirectoryPublishStore struct {
	transactionRoot string
	now             func() time.Time
	testFault       func(stage string) error
}

func NewDirectoryPublishStore(transactionRoot string) *DirectoryPublishStore {
	transactionRoot = strings.TrimSpace(transactionRoot)
	if transactionRoot != "" {
		absolute, err := filepath.Abs(transactionRoot)
		if err == nil {
			transactionRoot = absolute
		}
		transactionRoot = filepath.Clean(transactionRoot)
	}
	return &DirectoryPublishStore{
		transactionRoot: transactionRoot,
		now:             time.Now,
	}
}

func (s *DirectoryPublishStore) PublishDirectory(
	request PublishDirectoryRequest,
) (*DirectoryPublishReceipt, error) {
	normalized, err := s.normalizeRequest(request)
	if err != nil {
		return nil, err
	}
	var receipt *DirectoryPublishReceipt
	err = s.withWriteLock(func() error {
		if aborted, err := s.loadAbortedReceiptUnlocked(normalized.TransactionID); err != nil {
			return err
		} else if aborted != nil {
			return fmt.Errorf("transaction_id %s was already aborted", normalized.TransactionID)
		}
		intent, err := s.loadIntentUnlocked(normalized.TransactionID)
		if err != nil {
			return err
		}
		if intent != nil {
			if intent.LiveDir != normalized.LiveDir || intent.CandidateDir != normalized.CandidateDir {
				return fmt.Errorf("transaction_id %s parameter drift: live/candidate path changed", normalized.TransactionID)
			}
			if intent.BeforeLiveRoot != normalized.ExpectedLiveRoot {
				return fmt.Errorf("transaction_id %s parameter drift: expected live root changed", normalized.TransactionID)
			}
			receipt, err = s.recoverUnlocked(*intent)
			return err
		}
		if existing, err := s.loadReceiptUnlocked(normalized.TransactionID); err != nil {
			return err
		} else if existing != nil {
			return fmt.Errorf("transaction_id %s already committed with no active intent", normalized.TransactionID)
		}
		intent, err = s.createIntentUnlocked(normalized)
		if err != nil {
			return err
		}
		if err := s.failStage("intent_written"); err != nil {
			return err
		}
		receipt, err = s.recoverUnlocked(*intent)
		return err
	})
	return receipt, err
}

func (s *DirectoryPublishStore) RecoverDirectoryPublish(
	transactionID string,
) (*DirectoryPublishReceipt, error) {
	if err := validateDirectoryPublishTransactionID(transactionID); err != nil {
		return nil, err
	}
	var receipt *DirectoryPublishReceipt
	err := s.withWriteLock(func() error {
		intent, err := s.loadIntentUnlocked(transactionID)
		if err != nil {
			return err
		}
		if intent == nil {
			if aborted, err := s.loadAbortedReceiptUnlocked(transactionID); err != nil {
				return err
			} else if aborted != nil {
				return ErrDirectoryPublishAborted
			}
			receipt, err = s.loadReceiptUnlocked(transactionID)
			if err != nil {
				return err
			}
			if receipt == nil {
				return fmt.Errorf("directory publish transaction %s does not exist", transactionID)
			}
			if err := s.validateCommittedReceiptUnlocked(*receipt); err != nil {
				return err
			}
			finalized, err := s.loadFinalizeReceiptUnlocked(transactionID)
			if err != nil {
				return err
			}
			if finalized == nil {
				return fmt.Errorf("transaction %s lost intent before finalize receipt", transactionID)
			}
			return validateDirectoryPublishFinalizeReceipt(*finalized, *receipt, nil)
		}
		if abortIntent, err := s.loadAbortIntentUnlocked(transactionID); err != nil {
			return err
		} else if abortIntent != nil {
			if err := s.completeAbortUnlocked(*intent, *abortIntent); err != nil {
				return err
			}
			return ErrDirectoryPublishAborted
		}
		receipt, err = s.recoverUnlocked(*intent)
		return err
	})
	return receipt, err
}

func (s *DirectoryPublishStore) RecoverAllDirectoryPublishes() ([]DirectoryPublishReceipt, error) {
	var receipts []DirectoryPublishReceipt
	err := s.withWriteLock(func() error {
		entries, err := os.ReadDir(s.transactionRoot)
		if err != nil {
			return err
		}
		var ids []string
		for _, entry := range entries {
			if !entry.IsDir() || !directoryPublishTransactionIDPattern.MatchString(entry.Name()) {
				continue
			}
			intentPath := filepath.Join(s.transactionRoot, entry.Name(), directoryPublishIntentName)
			info, err := os.Lstat(intentPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("transaction %s intent is not a regular file", entry.Name())
			}
			ids = append(ids, entry.Name())
		}
		sort.Strings(ids)
		for _, id := range ids {
			intent, err := s.loadIntentUnlocked(id)
			if err != nil {
				return err
			}
			if intent == nil {
				continue
			}
			if abortIntent, err := s.loadAbortIntentUnlocked(id); err != nil {
				return err
			} else if abortIntent != nil {
				if err := s.completeAbortUnlocked(*intent, *abortIntent); err != nil {
					return err
				}
				continue
			}
			receipt, err := s.recoverUnlocked(*intent)
			if err != nil {
				return fmt.Errorf("recover directory publish %s: %w", id, err)
			}
			receipts = append(receipts, *receipt)
		}
		return nil
	})
	return receipts, err
}

func (s *DirectoryPublishStore) AbortDirectoryPublish(transactionID string) error {
	if err := validateDirectoryPublishTransactionID(transactionID); err != nil {
		return err
	}
	return s.withWriteLock(func() error {
		aborted, err := s.loadAbortedReceiptUnlocked(transactionID)
		if err != nil {
			return err
		}
		intent, err := s.loadIntentUnlocked(transactionID)
		if err != nil {
			return err
		}
		if intent == nil {
			if aborted != nil {
				return nil
			}
			return fmt.Errorf("directory publish transaction %s has no active intent", transactionID)
		}
		if aborted != nil {
			abortIntent, err := s.loadAbortIntentUnlocked(transactionID)
			if err != nil {
				return err
			}
			if abortIntent == nil {
				return fmt.Errorf("aborted transaction %s retained intent without abort journal", transactionID)
			}
			return s.completeAbortUnlocked(*intent, *abortIntent)
		}
		if receipt, err := s.loadReceiptUnlocked(transactionID); err != nil {
			return err
		} else if receipt != nil {
			return fmt.Errorf("directory publish transaction %s is already promoted and cannot abort", transactionID)
		}
		phase, err := s.phaseUnlocked(*intent)
		if err != nil {
			return err
		}
		if phase == DirectoryPublishCandidatePromoted || phase == DirectoryPublishReceiptWritten {
			return fmt.Errorf("directory publish transaction %s is already promoted and cannot abort", transactionID)
		}
		abortIntent, err := s.ensureAbortIntentUnlocked(*intent)
		if err != nil {
			return err
		}
		return s.completeAbortUnlocked(*intent, *abortIntent)
	})
}

func (s *DirectoryPublishStore) FinalizeDirectoryPublish(transactionID string) error {
	if err := validateDirectoryPublishTransactionID(transactionID); err != nil {
		return err
	}
	return s.withWriteLock(func() error {
		if aborted, err := s.loadAbortedReceiptUnlocked(transactionID); err != nil {
			return err
		} else if aborted != nil {
			return fmt.Errorf("aborted transaction %s cannot be finalized", transactionID)
		}
		intent, err := s.loadIntentUnlocked(transactionID)
		if err != nil {
			return err
		}
		receipt, err := s.loadReceiptUnlocked(transactionID)
		if err != nil {
			return err
		}
		if receipt == nil {
			return fmt.Errorf("transaction %s has no committed receipt", transactionID)
		}
		if intent == nil {
			if err := validateDirectoryPublishReceipt(*receipt); err != nil {
				return err
			}
			finalized, err := s.loadFinalizeReceiptUnlocked(transactionID)
			if err != nil {
				return err
			}
			if finalized == nil {
				return fmt.Errorf("transaction %s lost intent before finalize receipt", transactionID)
			}
			return validateDirectoryPublishFinalizeReceipt(*finalized, *receipt, nil)
		}
		if err := s.validateCommittedReceiptUnlocked(*receipt); err != nil {
			return err
		}
		if err := validateDirectoryPublishReceiptAgainstIntent(*receipt, *intent); err != nil {
			return err
		}
		if err := s.ensureFinalizeReceiptUnlocked(*intent, *receipt); err != nil {
			return err
		}
		if err := removeDirectoryIfExact(intent.ArchiveDir, intent.BeforeLiveRoot); err != nil {
			return fmt.Errorf("finalize archive: %w", err)
		}
		if err := removeDirectoryIfExact(intent.CandidateDir, intent.CandidateRoot); err != nil {
			return fmt.Errorf("finalize candidate residue: %w", err)
		}
		if err := removeEmptyDirectory(filepath.Join(s.transactionDir(transactionID), "archive")); err != nil {
			return fmt.Errorf("finalize archive journal directory: %w", err)
		}
		if err := os.Remove(filepath.Join(s.transactionDir(transactionID), directoryPublishIntentName)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return syncDirectoryPublishParents(
			s.transactionDir(transactionID),
			filepath.Dir(intent.LiveDir),
			filepath.Dir(intent.CandidateDir),
		)
	})
}

func (s *DirectoryPublishStore) LoadDirectoryPublishState(
	transactionID string,
) (*DirectoryPublishState, error) {
	if err := validateDirectoryPublishTransactionID(transactionID); err != nil {
		return nil, err
	}
	var state *DirectoryPublishState
	err := s.withReadLock(func() error {
		if aborted, err := s.loadAbortedReceiptUnlocked(transactionID); err != nil {
			return err
		} else if aborted != nil {
			state = &DirectoryPublishState{TransactionID: transactionID, Phase: DirectoryPublishAborted}
			return nil
		}
		intent, err := s.loadIntentUnlocked(transactionID)
		if err != nil {
			return err
		}
		receipt, err := s.loadReceiptUnlocked(transactionID)
		if err != nil {
			return err
		}
		if intent == nil {
			if receipt == nil {
				state = nil
				return nil
			}
			if err := validateDirectoryPublishReceipt(*receipt); err != nil {
				return err
			}
			finalized, err := s.loadFinalizeReceiptUnlocked(transactionID)
			if err != nil {
				return err
			}
			if finalized == nil {
				return fmt.Errorf("transaction %s lost intent before finalize receipt", transactionID)
			}
			if err := validateDirectoryPublishFinalizeReceipt(*finalized, *receipt, nil); err != nil {
				return err
			}
			state = &DirectoryPublishState{
				TransactionID: transactionID,
				Phase:         DirectoryPublishFinalized,
				Receipt:       receipt,
			}
			return nil
		}
		phase, err := s.phaseUnlocked(*intent)
		if err != nil {
			return err
		}
		state = &DirectoryPublishState{
			TransactionID: transactionID,
			Phase:         phase,
			Intent:        intent,
			Receipt:       receipt,
		}
		return nil
	})
	return state, err
}

// LoadDirectoryPublishReceiptEvidence authenticates the immutable publish
// intent/receipt (and finalize receipt once the intent has been removed)
// without requiring the current live directory to retain the historical full
// tree root. This is intentionally narrower than LoadDirectoryPublishState:
// callers must independently verify every immutable live artifact before
// using it after known runtime-only files, such as pipeline_execution owner,
// have changed.
func (s *DirectoryPublishStore) LoadDirectoryPublishReceiptEvidence(
	transactionID string,
) (*DirectoryPublishReceipt, error) {
	if err := validateDirectoryPublishTransactionID(transactionID); err != nil {
		return nil, err
	}
	var result *DirectoryPublishReceipt
	err := s.withReadLock(func() error {
		if aborted, err := s.loadAbortedReceiptUnlocked(transactionID); err != nil {
			return err
		} else if aborted != nil {
			return fmt.Errorf("directory publish transaction %s was aborted", transactionID)
		}
		intent, err := s.loadIntentUnlocked(transactionID)
		if err != nil {
			return err
		}
		receipt, err := s.loadReceiptUnlocked(transactionID)
		if err != nil || receipt == nil {
			return err
		}
		if intent != nil {
			if err := validateDirectoryPublishReceiptAgainstIntent(*receipt, *intent); err != nil {
				return err
			}
		} else {
			if err := validateDirectoryPublishReceipt(*receipt); err != nil {
				return err
			}
			finalized, err := s.loadFinalizeReceiptUnlocked(transactionID)
			if err != nil {
				return err
			}
			if finalized == nil {
				return fmt.Errorf("transaction %s lost intent before finalize receipt", transactionID)
			}
			if err := validateDirectoryPublishFinalizeReceipt(*finalized, *receipt, nil); err != nil {
				return err
			}
		}
		copy := *receipt
		result = &copy
		return nil
	})
	return result, err
}

func (s *DirectoryPublishStore) recoverUnlocked(
	intent DirectoryPublishIntent,
) (*DirectoryPublishReceipt, error) {
	if err := validateDirectoryPublishIntent(intent); err != nil {
		return nil, err
	}
	if abortIntent, err := s.loadAbortIntentUnlocked(intent.TransactionID); err != nil {
		return nil, err
	} else if abortIntent != nil {
		if err := s.completeAbortUnlocked(intent, *abortIntent); err != nil {
			return nil, err
		}
		return nil, ErrDirectoryPublishAborted
	}
	if receipt, err := s.loadReceiptUnlocked(intent.TransactionID); err != nil {
		return nil, err
	} else if receipt != nil {
		if err := validateDirectoryPublishReceiptAgainstIntent(*receipt, intent); err != nil {
			return nil, err
		}
		if err := s.validateCommittedReceiptUnlocked(*receipt); err != nil {
			return nil, err
		}
		return receipt, nil
	}

	phase, err := s.phaseUnlocked(intent)
	if err != nil {
		return nil, err
	}
	if phase == DirectoryPublishIntentWritten {
		if err := os.MkdirAll(filepath.Dir(intent.ArchiveDir), 0o755); err != nil {
			return nil, err
		}
		if err := syncDirectoryPublishParents(s.transactionDir(intent.TransactionID)); err != nil {
			return nil, err
		}
		if err := os.Rename(intent.LiveDir, intent.ArchiveDir); err != nil {
			return nil, fmt.Errorf("archive live directory: %w", err)
		}
		if err := syncDirectoryPublishParents(filepath.Dir(intent.LiveDir), filepath.Dir(intent.ArchiveDir)); err != nil {
			return nil, err
		}
		if err := s.failStage("live_archived"); err != nil {
			return nil, err
		}
		phase = DirectoryPublishLiveArchived
	}
	if phase == DirectoryPublishLiveArchived {
		if root, err := directoryContentRoot(intent.ArchiveDir); err != nil {
			return nil, err
		} else if root != intent.BeforeLiveRoot {
			return nil, fmt.Errorf("archive root drift: got %s want %s", root, intent.BeforeLiveRoot)
		}
		if root, err := directoryContentRoot(intent.CandidateDir); err != nil {
			return nil, err
		} else if root != intent.CandidateRoot {
			return nil, fmt.Errorf("candidate root drift: got %s want %s", root, intent.CandidateRoot)
		}
		if err := os.Rename(intent.CandidateDir, intent.LiveDir); err != nil {
			return nil, fmt.Errorf("promote candidate directory: %w", err)
		}
		if err := syncDirectoryPublishParents(filepath.Dir(intent.CandidateDir), filepath.Dir(intent.LiveDir)); err != nil {
			return nil, err
		}
		if err := s.failStage("candidate_promoted"); err != nil {
			return nil, err
		}
		phase = DirectoryPublishCandidatePromoted
	}
	if phase != DirectoryPublishCandidatePromoted {
		return nil, fmt.Errorf("unexpected directory publish phase %q", phase)
	}
	liveRoot, err := directoryContentRoot(intent.LiveDir)
	if err != nil {
		return nil, err
	}
	if liveRoot != intent.CandidateRoot {
		return nil, fmt.Errorf("promoted live root drift: got %s want %s", liveRoot, intent.CandidateRoot)
	}
	archiveRoot, err := directoryContentRoot(intent.ArchiveDir)
	if err != nil {
		return nil, err
	}
	if archiveRoot != intent.BeforeLiveRoot {
		return nil, fmt.Errorf("archive root drift: got %s want %s", archiveRoot, intent.BeforeLiveRoot)
	}
	receipt := DirectoryPublishReceipt{
		Version:           directoryPublishReceiptVersion,
		TransactionID:     intent.TransactionID,
		IntentDigest:      intent.IntentDigest,
		LiveDir:           intent.LiveDir,
		CandidateDir:      intent.CandidateDir,
		ArchiveDir:        intent.ArchiveDir,
		BeforeLiveRoot:    intent.BeforeLiveRoot,
		CandidateRoot:     intent.CandidateRoot,
		CommittedLiveRoot: liveRoot,
		CommittedAt:       s.now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest, err = computeDirectoryPublishReceiptDigest(receipt)
	if err != nil {
		return nil, err
	}
	if _, err := writeDirectoryPublishJSONNoReplace(
		filepath.Join(s.transactionDir(intent.TransactionID), directoryPublishReceiptName),
		receipt,
	); err != nil {
		return nil, err
	}
	if err := s.failStage("receipt_written"); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *DirectoryPublishStore) normalizeRequest(
	request PublishDirectoryRequest,
) (PublishDirectoryRequest, error) {
	request.TransactionID = strings.TrimSpace(request.TransactionID)
	request.ExpectedLiveRoot = strings.TrimSpace(request.ExpectedLiveRoot)
	if err := validateDirectoryPublishTransactionID(request.TransactionID); err != nil {
		return PublishDirectoryRequest{}, err
	}
	if err := validateDirectoryPublishDigest("expected_live_root", request.ExpectedLiveRoot); err != nil {
		return PublishDirectoryRequest{}, err
	}
	var err error
	request.LiveDir, err = absoluteCleanPath(request.LiveDir)
	if err != nil {
		return PublishDirectoryRequest{}, fmt.Errorf("live_dir: %w", err)
	}
	request.CandidateDir, err = absoluteCleanPath(request.CandidateDir)
	if err != nil {
		return PublishDirectoryRequest{}, fmt.Errorf("candidate_dir: %w", err)
	}
	root, err := absoluteCleanPath(s.transactionRoot)
	if err != nil {
		return PublishDirectoryRequest{}, fmt.Errorf("transaction_root: %w", err)
	}
	if pathsOverlap(request.LiveDir, request.CandidateDir) {
		if pathWithin(request.CandidateDir, request.LiveDir) {
			return PublishDirectoryRequest{}, fmt.Errorf("candidate_dir must not be inside live_dir")
		}
		return PublishDirectoryRequest{}, fmt.Errorf("live_dir and candidate_dir overlap")
	}
	for _, item := range []struct {
		name string
		path string
	}{
		{"live_dir", request.LiveDir},
		{"candidate_dir", request.CandidateDir},
	} {
		if pathsOverlap(item.path, root) {
			return PublishDirectoryRequest{}, fmt.Errorf("%s overlaps transaction_root", item.name)
		}
	}
	return request, nil
}

func (s *DirectoryPublishStore) createIntentUnlocked(
	request PublishDirectoryRequest,
) (*DirectoryPublishIntent, error) {
	if err := requireRegularDirectory(request.LiveDir, "live_dir"); err != nil {
		return nil, err
	}
	if err := requireRegularDirectory(request.CandidateDir, "candidate_dir"); err != nil {
		return nil, err
	}
	resolvedLive, err := filepath.EvalSymlinks(request.LiveDir)
	if err != nil {
		return nil, err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(request.CandidateDir)
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(s.transactionRoot)
	if err != nil {
		return nil, err
	}
	if pathsOverlap(resolvedLive, resolvedCandidate) {
		return nil, fmt.Errorf("resolved live_dir and candidate_dir overlap")
	}
	if pathsOverlap(resolvedLive, resolvedRoot) || pathsOverlap(resolvedCandidate, resolvedRoot) {
		return nil, fmt.Errorf("resolved publish directories overlap transaction_root")
	}
	if err := requireSameFilesystem(
		filepath.Dir(request.LiveDir),
		filepath.Dir(request.CandidateDir),
		s.transactionRoot,
	); err != nil {
		return nil, err
	}
	txDir := s.transactionDir(request.TransactionID)
	if entries, err := os.ReadDir(txDir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("transaction_id %s directory already contains unrelated state", request.TransactionID)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	beforeRoot, err := directoryContentRoot(request.LiveDir)
	if err != nil {
		return nil, err
	}
	if beforeRoot != request.ExpectedLiveRoot {
		return nil, fmt.Errorf(
			"live directory changed after candidate snapshot: got %s want %s",
			beforeRoot,
			request.ExpectedLiveRoot,
		)
	}
	candidateRoot, err := directoryContentRoot(request.CandidateDir)
	if err != nil {
		return nil, err
	}
	archiveDir := filepath.Join(txDir, "archive", "live")
	parametersDigest, err := directoryPublishDigest(struct {
		Version       string `json:"version"`
		TransactionID string `json:"transaction_id"`
		LiveDir       string `json:"live_dir"`
		CandidateDir  string `json:"candidate_dir"`
		ArchiveDir    string `json:"archive_dir"`
	}{
		Version:       directoryPublishIntentVersion,
		TransactionID: request.TransactionID,
		LiveDir:       request.LiveDir,
		CandidateDir:  request.CandidateDir,
		ArchiveDir:    archiveDir,
	})
	if err != nil {
		return nil, err
	}
	intent := DirectoryPublishIntent{
		Version:          directoryPublishIntentVersion,
		TransactionID:    request.TransactionID,
		LiveDir:          request.LiveDir,
		CandidateDir:     request.CandidateDir,
		ArchiveDir:       archiveDir,
		BeforeLiveRoot:   beforeRoot,
		CandidateRoot:    candidateRoot,
		ParametersDigest: parametersDigest,
		CreatedAt:        s.now().UTC().Format(time.RFC3339Nano),
	}
	intent.IntentDigest, err = computeDirectoryPublishIntentDigest(intent)
	if err != nil {
		return nil, err
	}
	if err := validateDirectoryPublishIntent(intent); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		return nil, err
	}
	if _, err := writeDirectoryPublishJSONNoReplace(
		filepath.Join(txDir, directoryPublishIntentName),
		intent,
	); err != nil {
		return nil, err
	}
	if err := syncDirectoryPublishParents(s.transactionRoot); err != nil {
		return nil, err
	}
	return &intent, nil
}

func (s *DirectoryPublishStore) phaseUnlocked(
	intent DirectoryPublishIntent,
) (DirectoryPublishPhase, error) {
	if receipt, err := s.loadReceiptUnlocked(intent.TransactionID); err != nil {
		return "", err
	} else if receipt != nil {
		if err := validateDirectoryPublishReceiptAgainstIntent(*receipt, intent); err != nil {
			return "", err
		}
		if err := s.validateCommittedReceiptUnlocked(*receipt); err != nil {
			return "", err
		}
		return DirectoryPublishReceiptWritten, nil
	}
	liveRoot, liveExists, err := optionalDirectoryRoot(intent.LiveDir)
	if err != nil {
		return "", err
	}
	candidateRoot, candidateExists, err := optionalDirectoryRoot(intent.CandidateDir)
	if err != nil {
		return "", err
	}
	archiveRoot, archiveExists, err := optionalDirectoryRoot(intent.ArchiveDir)
	if err != nil {
		return "", err
	}
	if !archiveExists {
		if liveExists && liveRoot == intent.BeforeLiveRoot &&
			candidateExists && candidateRoot == intent.CandidateRoot {
			return DirectoryPublishIntentWritten, nil
		}
		return "", fmt.Errorf("directory publish state is inconsistent before archive")
	}
	if archiveRoot != intent.BeforeLiveRoot {
		return "", fmt.Errorf("archive root drift: got %s want %s", archiveRoot, intent.BeforeLiveRoot)
	}
	if !liveExists {
		if !candidateExists {
			return "", fmt.Errorf("live and candidate are both missing after archive")
		}
		if candidateRoot != intent.CandidateRoot {
			return "", fmt.Errorf("candidate root drift: got %s want %s", candidateRoot, intent.CandidateRoot)
		}
		return DirectoryPublishLiveArchived, nil
	}
	if liveRoot == intent.CandidateRoot {
		if candidateExists && candidateRoot != intent.CandidateRoot {
			return "", fmt.Errorf("candidate residue differs from intended candidate root")
		}
		return DirectoryPublishCandidatePromoted, nil
	}
	return "", fmt.Errorf("live directory root %s matches neither before nor candidate root", liveRoot)
}

func (s *DirectoryPublishStore) validateCommittedReceiptUnlocked(
	receipt DirectoryPublishReceipt,
) error {
	if err := validateDirectoryPublishReceipt(receipt); err != nil {
		return err
	}
	liveRoot, err := directoryContentRoot(receipt.LiveDir)
	if err != nil {
		return err
	}
	if liveRoot != receipt.CandidateRoot || liveRoot != receipt.CommittedLiveRoot {
		return fmt.Errorf("committed live root drift: got %s want %s", liveRoot, receipt.CandidateRoot)
	}
	if _, err := os.Stat(receipt.ArchiveDir); err == nil {
		archiveRoot, err := directoryContentRoot(receipt.ArchiveDir)
		if err != nil {
			return err
		}
		if archiveRoot != receipt.BeforeLiveRoot {
			return fmt.Errorf("committed archive root drift")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if finalized, err := s.loadFinalizeReceiptUnlocked(receipt.TransactionID); err != nil {
		return err
	} else if finalized == nil {
		return fmt.Errorf("committed archive is missing before finalize")
	} else if err := validateDirectoryPublishFinalizeReceipt(*finalized, receipt, nil); err != nil {
		return err
	}
	return nil
}

func (s *DirectoryPublishStore) loadIntentUnlocked(
	transactionID string,
) (*DirectoryPublishIntent, error) {
	var intent DirectoryPublishIntent
	found, err := readDirectoryPublishJSON(
		filepath.Join(s.transactionDir(transactionID), directoryPublishIntentName),
		&intent,
	)
	if err != nil || !found {
		return nil, err
	}
	if err := validateDirectoryPublishIntent(intent); err != nil {
		return nil, err
	}
	if intent.TransactionID != transactionID {
		return nil, fmt.Errorf("intent path transaction identity mismatch")
	}
	expectedArchive := filepath.Join(s.transactionDir(transactionID), "archive", "live")
	if intent.ArchiveDir != expectedArchive {
		return nil, fmt.Errorf("intent archive_dir is outside its transaction journal")
	}
	if pathsOverlap(intent.LiveDir, s.transactionRoot) {
		return nil, fmt.Errorf("intent live_dir overlaps transaction_root")
	}
	if pathsOverlap(intent.CandidateDir, s.transactionRoot) {
		return nil, fmt.Errorf("intent candidate_dir overlaps transaction_root")
	}
	return &intent, nil
}

func (s *DirectoryPublishStore) loadReceiptUnlocked(
	transactionID string,
) (*DirectoryPublishReceipt, error) {
	var receipt DirectoryPublishReceipt
	found, err := readDirectoryPublishJSON(
		filepath.Join(s.transactionDir(transactionID), directoryPublishReceiptName),
		&receipt,
	)
	if err != nil || !found {
		return nil, err
	}
	if err := validateDirectoryPublishReceipt(receipt); err != nil {
		return nil, err
	}
	if receipt.TransactionID != transactionID {
		return nil, fmt.Errorf("receipt path transaction identity mismatch")
	}
	expectedArchive := filepath.Join(s.transactionDir(transactionID), "archive", "live")
	if receipt.ArchiveDir != expectedArchive {
		return nil, fmt.Errorf("receipt archive_dir is outside its transaction journal")
	}
	if pathsOverlap(receipt.LiveDir, s.transactionRoot) {
		return nil, fmt.Errorf("receipt live_dir overlaps transaction_root")
	}
	if receipt.CandidateDir != "" && pathsOverlap(receipt.CandidateDir, s.transactionRoot) {
		return nil, fmt.Errorf("receipt candidate_dir overlaps transaction_root")
	}
	return &receipt, nil
}

func (s *DirectoryPublishStore) loadFinalizeReceiptUnlocked(
	transactionID string,
) (*directoryPublishFinalizeReceipt, error) {
	var receipt directoryPublishFinalizeReceipt
	found, err := readDirectoryPublishJSON(
		filepath.Join(s.transactionDir(transactionID), directoryPublishFinalizeName),
		&receipt,
	)
	if err != nil || !found {
		return nil, err
	}
	want, err := computeDirectoryPublishFinalizeDigest(receipt)
	if err != nil {
		return nil, err
	}
	if receipt.Version != directoryPublishFinalizeVersion ||
		receipt.TransactionID != transactionID ||
		receipt.FinalizeDigest != want {
		return nil, fmt.Errorf("invalid directory publish finalize receipt")
	}
	for name, digest := range map[string]string{
		"receipt_digest":  receipt.ReceiptDigest,
		"intent_digest":   receipt.IntentDigest,
		"live_root":       receipt.LiveRoot,
		"finalize_digest": receipt.FinalizeDigest,
	} {
		if err := validateDirectoryPublishDigest(name, digest); err != nil {
			return nil, err
		}
	}
	if _, err := time.Parse(time.RFC3339, receipt.FinalizedAt); err != nil {
		return nil, fmt.Errorf("finalize receipt finalized_at must be RFC3339: %w", err)
	}
	return &receipt, nil
}

func (s *DirectoryPublishStore) ensureFinalizeReceiptUnlocked(
	intent DirectoryPublishIntent,
	receipt DirectoryPublishReceipt,
) error {
	if existing, err := s.loadFinalizeReceiptUnlocked(intent.TransactionID); err != nil {
		return err
	} else if existing != nil {
		return validateDirectoryPublishFinalizeReceipt(*existing, receipt, &intent)
	}
	finalized := directoryPublishFinalizeReceipt{
		Version:       directoryPublishFinalizeVersion,
		TransactionID: intent.TransactionID,
		ReceiptDigest: receipt.ReceiptDigest,
		IntentDigest:  intent.IntentDigest,
		LiveRoot:      receipt.CommittedLiveRoot,
		FinalizedAt:   s.now().UTC().Format(time.RFC3339Nano),
	}
	var err error
	finalized.FinalizeDigest, err = computeDirectoryPublishFinalizeDigest(finalized)
	if err != nil {
		return err
	}
	_, err = writeDirectoryPublishJSONNoReplace(
		filepath.Join(s.transactionDir(intent.TransactionID), directoryPublishFinalizeName),
		finalized,
	)
	return err
}

func (s *DirectoryPublishStore) ensureAbortIntentUnlocked(
	intent DirectoryPublishIntent,
) (*directoryPublishAbortIntent, error) {
	if existing, err := s.loadAbortIntentUnlocked(intent.TransactionID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	abortIntent := directoryPublishAbortIntent{
		Version:       directoryPublishAbortVersion,
		TransactionID: intent.TransactionID,
		IntentDigest:  intent.IntentDigest,
		RequestedAt:   s.now().UTC().Format(time.RFC3339Nano),
	}
	var err error
	abortIntent.AbortDigest, err = computeDirectoryPublishAbortDigest(abortIntent)
	if err != nil {
		return nil, err
	}
	if _, err := writeDirectoryPublishJSONNoReplace(
		filepath.Join(s.transactionDir(intent.TransactionID), directoryPublishAbortIntentName),
		abortIntent,
	); err != nil {
		return nil, err
	}
	return &abortIntent, nil
}

func (s *DirectoryPublishStore) loadAbortIntentUnlocked(
	transactionID string,
) (*directoryPublishAbortIntent, error) {
	var record directoryPublishAbortIntent
	found, err := readDirectoryPublishJSON(
		filepath.Join(s.transactionDir(transactionID), directoryPublishAbortIntentName),
		&record,
	)
	if err != nil || !found {
		return nil, err
	}
	want, err := computeDirectoryPublishAbortDigest(record)
	if err != nil {
		return nil, err
	}
	if record.Version != directoryPublishAbortVersion ||
		record.TransactionID != transactionID ||
		record.AbortDigest != want {
		return nil, fmt.Errorf("invalid directory publish abort intent")
	}
	for name, digest := range map[string]string{
		"intent_digest": record.IntentDigest,
		"abort_digest":  record.AbortDigest,
	} {
		if err := validateDirectoryPublishDigest(name, digest); err != nil {
			return nil, err
		}
	}
	if _, err := time.Parse(time.RFC3339, record.RequestedAt); err != nil {
		return nil, fmt.Errorf("abort intent requested_at must be RFC3339: %w", err)
	}
	return &record, nil
}

func (s *DirectoryPublishStore) loadAbortedReceiptUnlocked(
	transactionID string,
) (*directoryPublishAbortedReceipt, error) {
	var receipt directoryPublishAbortedReceipt
	found, err := readDirectoryPublishJSON(
		filepath.Join(s.transactionDir(transactionID), directoryPublishAbortedName),
		&receipt,
	)
	if err != nil || !found {
		return nil, err
	}
	want, err := computeDirectoryPublishAbortedDigest(receipt)
	if err != nil {
		return nil, err
	}
	if receipt.Version != directoryPublishAbortedVersion ||
		receipt.TransactionID != transactionID ||
		receipt.ReceiptDigest != want {
		return nil, fmt.Errorf("invalid directory publish aborted receipt")
	}
	for name, digest := range map[string]string{
		"intent_digest":  receipt.IntentDigest,
		"restored_root":  receipt.RestoredRoot,
		"receipt_digest": receipt.ReceiptDigest,
	} {
		if err := validateDirectoryPublishDigest(name, digest); err != nil {
			return nil, err
		}
	}
	if _, err := time.Parse(time.RFC3339, receipt.AbortedAt); err != nil {
		return nil, fmt.Errorf("aborted receipt aborted_at must be RFC3339: %w", err)
	}
	return &receipt, nil
}

func (s *DirectoryPublishStore) completeAbortUnlocked(
	intent DirectoryPublishIntent,
	abortIntent directoryPublishAbortIntent,
) error {
	if abortIntent.IntentDigest != intent.IntentDigest {
		return fmt.Errorf("abort intent does not bind active publish intent")
	}
	if receipt, err := s.loadReceiptUnlocked(intent.TransactionID); err != nil {
		return err
	} else if receipt != nil {
		return fmt.Errorf("promoted transaction %s cannot abort", intent.TransactionID)
	}
	phase, err := s.phaseUnlocked(intent)
	if err != nil {
		return err
	}
	switch phase {
	case DirectoryPublishIntentWritten:
	case DirectoryPublishLiveArchived:
		if err := os.Rename(intent.ArchiveDir, intent.LiveDir); err != nil {
			return fmt.Errorf("restore archived live directory: %w", err)
		}
		if err := syncDirectoryPublishParents(filepath.Dir(intent.ArchiveDir), filepath.Dir(intent.LiveDir)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("transaction %s is already promoted and cannot abort", intent.TransactionID)
	}
	root, err := directoryContentRoot(intent.LiveDir)
	if err != nil {
		return err
	}
	if root != intent.BeforeLiveRoot {
		return fmt.Errorf("abort restored root drift: got %s want %s", root, intent.BeforeLiveRoot)
	}
	existingAborted, err := s.loadAbortedReceiptUnlocked(intent.TransactionID)
	if err != nil {
		return err
	}
	if existingAborted != nil {
		if existingAborted.IntentDigest != intent.IntentDigest ||
			existingAborted.RestoredRoot != root ||
			existingAborted.AbortedAt != abortIntent.RequestedAt {
			return fmt.Errorf("aborted receipt does not bind active abort")
		}
	} else {
		aborted := directoryPublishAbortedReceipt{
			Version:       directoryPublishAbortedVersion,
			TransactionID: intent.TransactionID,
			IntentDigest:  intent.IntentDigest,
			RestoredRoot:  root,
			AbortedAt:     abortIntent.RequestedAt,
		}
		aborted.ReceiptDigest, err = computeDirectoryPublishAbortedDigest(aborted)
		if err != nil {
			return err
		}
		if _, err := writeDirectoryPublishJSONNoReplace(
			filepath.Join(s.transactionDir(intent.TransactionID), directoryPublishAbortedName),
			aborted,
		); err != nil {
			return err
		}
	}
	if err := removeEmptyDirectory(filepath.Join(s.transactionDir(intent.TransactionID), "archive")); err != nil {
		return fmt.Errorf("abort archive journal directory: %w", err)
	}
	for _, name := range []string{directoryPublishIntentName, directoryPublishAbortIntentName} {
		if err := os.Remove(filepath.Join(s.transactionDir(intent.TransactionID), name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDirectoryPublishParents(s.transactionDir(intent.TransactionID), filepath.Dir(intent.LiveDir))
}

func (s *DirectoryPublishStore) transactionDir(transactionID string) string {
	return filepath.Join(s.transactionRoot, transactionID)
}

func (s *DirectoryPublishStore) failStage(stage string) error {
	if s.testFault == nil {
		return nil
	}
	return s.testFault(stage)
}

func (s *DirectoryPublishStore) withWriteLock(fn func() error) error {
	if err := ensureDirectoryPublishRoot(s.transactionRoot); err != nil {
		return err
	}
	lock, err := os.OpenFile(
		filepath.Join(s.transactionRoot, directoryPublishLockName),
		os.O_CREATE|os.O_RDWR,
		0o644,
	)
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

func (s *DirectoryPublishStore) withReadLock(fn func() error) error {
	if strings.TrimSpace(s.transactionRoot) == "" || !filepath.IsAbs(s.transactionRoot) {
		return fmt.Errorf("transaction_root must be a non-empty absolute path")
	}
	if _, err := os.Lstat(s.transactionRoot); os.IsNotExist(err) {
		return fn()
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(
		filepath.Join(s.transactionRoot, directoryPublishLockName),
		os.O_CREATE|os.O_RDWR,
		0o644,
	)
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

func ensureDirectoryPublishRoot(root string) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("transaction_root must be a non-empty absolute path")
	}
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("transaction_root must be a real directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return syncDirectoryPublishParents(filepath.Dir(root))
}

func validateDirectoryPublishTransactionID(transactionID string) error {
	if !directoryPublishTransactionIDPattern.MatchString(strings.TrimSpace(transactionID)) {
		return fmt.Errorf("invalid directory publish transaction_id %q", transactionID)
	}
	return nil
}

func validateDirectoryPublishIntent(intent DirectoryPublishIntent) error {
	if intent.Version != directoryPublishIntentVersion {
		return fmt.Errorf("unsupported directory publish intent version %q", intent.Version)
	}
	if err := validateDirectoryPublishTransactionID(intent.TransactionID); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"live_dir":      intent.LiveDir,
		"candidate_dir": intent.CandidateDir,
		"archive_dir":   intent.ArchiveDir,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be an absolute clean path", name)
		}
	}
	if pathsOverlap(intent.LiveDir, intent.CandidateDir) {
		return fmt.Errorf("intent live/candidate paths overlap")
	}
	for name, digest := range map[string]string{
		"before_live_root":  intent.BeforeLiveRoot,
		"candidate_root":    intent.CandidateRoot,
		"parameters_digest": intent.ParametersDigest,
		"intent_digest":     intent.IntentDigest,
	} {
		if err := validateDirectoryPublishDigest(name, digest); err != nil {
			return err
		}
	}
	if _, err := time.Parse(time.RFC3339, intent.CreatedAt); err != nil {
		return fmt.Errorf("intent created_at must be RFC3339: %w", err)
	}
	wantParameters, err := directoryPublishDigest(struct {
		Version       string `json:"version"`
		TransactionID string `json:"transaction_id"`
		LiveDir       string `json:"live_dir"`
		CandidateDir  string `json:"candidate_dir"`
		ArchiveDir    string `json:"archive_dir"`
	}{
		Version:       directoryPublishIntentVersion,
		TransactionID: intent.TransactionID,
		LiveDir:       intent.LiveDir,
		CandidateDir:  intent.CandidateDir,
		ArchiveDir:    intent.ArchiveDir,
	})
	if err != nil {
		return err
	}
	if intent.ParametersDigest != wantParameters {
		return fmt.Errorf("intent parameters_digest mismatch")
	}
	want, err := computeDirectoryPublishIntentDigest(intent)
	if err != nil {
		return err
	}
	if intent.IntentDigest != want {
		return fmt.Errorf("intent digest mismatch")
	}
	return nil
}

func validateDirectoryPublishReceipt(receipt DirectoryPublishReceipt) error {
	if receipt.Version != directoryPublishReceiptVersion && receipt.Version != directoryPublishReceiptLegacyVersion {
		return fmt.Errorf("unsupported directory publish receipt version %q", receipt.Version)
	}
	if err := validateDirectoryPublishTransactionID(receipt.TransactionID); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"live_dir":    receipt.LiveDir,
		"archive_dir": receipt.ArchiveDir,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be an absolute clean path", name)
		}
	}
	if receipt.Version == directoryPublishReceiptVersion {
		if !filepath.IsAbs(receipt.CandidateDir) || filepath.Clean(receipt.CandidateDir) != receipt.CandidateDir {
			return fmt.Errorf("candidate_dir must be an absolute clean path")
		}
	} else if receipt.CandidateDir != "" && (!filepath.IsAbs(receipt.CandidateDir) || filepath.Clean(receipt.CandidateDir) != receipt.CandidateDir) {
		return fmt.Errorf("candidate_dir must be an absolute clean path when present")
	}
	for name, digest := range map[string]string{
		"intent_digest":       receipt.IntentDigest,
		"before_live_root":    receipt.BeforeLiveRoot,
		"candidate_root":      receipt.CandidateRoot,
		"committed_live_root": receipt.CommittedLiveRoot,
		"receipt_digest":      receipt.ReceiptDigest,
	} {
		if err := validateDirectoryPublishDigest(name, digest); err != nil {
			return err
		}
	}
	if receipt.CandidateRoot != receipt.CommittedLiveRoot {
		return fmt.Errorf("receipt candidate/committed roots differ")
	}
	if _, err := time.Parse(time.RFC3339, receipt.CommittedAt); err != nil {
		return fmt.Errorf("receipt committed_at must be RFC3339: %w", err)
	}
	want, err := computeDirectoryPublishReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("receipt digest mismatch")
	}
	return nil
}

func validateDirectoryPublishReceiptAgainstIntent(
	receipt DirectoryPublishReceipt,
	intent DirectoryPublishIntent,
) error {
	if receipt.TransactionID != intent.TransactionID ||
		receipt.IntentDigest != intent.IntentDigest ||
		receipt.LiveDir != intent.LiveDir ||
		receipt.ArchiveDir != intent.ArchiveDir ||
		(receipt.CandidateDir != "" && receipt.CandidateDir != intent.CandidateDir) ||
		receipt.BeforeLiveRoot != intent.BeforeLiveRoot ||
		receipt.CandidateRoot != intent.CandidateRoot ||
		receipt.CommittedLiveRoot != intent.CandidateRoot {
		return fmt.Errorf("directory publish receipt does not bind active intent")
	}
	return nil
}

func validateDirectoryPublishFinalizeReceipt(
	finalized directoryPublishFinalizeReceipt,
	receipt DirectoryPublishReceipt,
	intent *DirectoryPublishIntent,
) error {
	if finalized.TransactionID != receipt.TransactionID ||
		finalized.ReceiptDigest != receipt.ReceiptDigest ||
		finalized.IntentDigest != receipt.IntentDigest ||
		finalized.LiveRoot != receipt.CommittedLiveRoot {
		return fmt.Errorf("directory publish finalize receipt does not bind committed receipt")
	}
	if intent != nil &&
		(finalized.TransactionID != intent.TransactionID ||
			finalized.IntentDigest != intent.IntentDigest) {
		return fmt.Errorf("directory publish finalize receipt does not bind active intent")
	}
	return nil
}

func computeDirectoryPublishIntentDigest(intent DirectoryPublishIntent) (string, error) {
	intent.IntentDigest = ""
	return directoryPublishDigest(intent)
}

func computeDirectoryPublishReceiptDigest(receipt DirectoryPublishReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return directoryPublishDigest(receipt)
}

func computeDirectoryPublishFinalizeDigest(receipt directoryPublishFinalizeReceipt) (string, error) {
	receipt.FinalizeDigest = ""
	return directoryPublishDigest(receipt)
}

func computeDirectoryPublishAbortDigest(record directoryPublishAbortIntent) (string, error) {
	record.AbortDigest = ""
	return directoryPublishDigest(record)
}

func computeDirectoryPublishAbortedDigest(receipt directoryPublishAbortedReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return directoryPublishDigest(receipt)
}

func directoryPublishDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateDirectoryPublishDigest(name, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex>", name)
	}
	raw := strings.TrimPrefix(digest, "sha256:")
	if len(raw) != 64 || raw != strings.ToLower(raw) {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex>", name)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex>", name)
	}
	return nil
}

func absoluteCleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func pathsOverlap(left, right string) bool {
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(path, ancestor string) bool {
	rel, err := filepath.Rel(ancestor, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func requireRegularDirectory(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s %s does not exist", name, path)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s %s must be a real directory", name, path)
	}
	return nil
}

func optionalDirectoryRoot(path string) (root string, exists bool, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("%s is not a real directory", path)
	}
	root, err = directoryContentRoot(path)
	return root, true, err
}

// DirectoryContentRoot returns the deterministic path+mode+content root used
// by directory publish CAS. Callers snapshot this before running an isolated
// candidate and must pass it back as ExpectedLiveRoot at publish time.
func DirectoryContentRoot(root string) (string, error) {
	root, err := absoluteCleanPath(root)
	if err != nil {
		return "", err
	}
	if err := requireRegularDirectory(root, "directory"); err != nil {
		return "", err
	}
	return directoryContentRoot(root)
}

func directoryContentRoot(root string) (string, error) {
	const transientRetryLimit = 8
	for attempt := 0; ; attempt++ {
		contentRoot, err := directoryContentRootOnce(root)
		if err == nil {
			return contentRoot, nil
		}
		// Atomic JSON writers create a sibling temp file and rename it into
		// place. WalkDir may observe the directory entry just before that rename
		// and then receive ENOENT from Info/ReadFile. The directory itself is not
		// corrupt; restart the complete snapshot so CAS still binds one coherent
		// tree. Every other error remains fail-closed.
		if !errors.Is(err, fs.ErrNotExist) || attempt+1 >= transientRetryLimit {
			return "", err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
}

func directoryContentRootOnce(root string) (string, error) {
	if err := requireRegularDirectory(root, "directory"); err != nil {
		return "", err
	}
	type entry struct {
		path string
		kind byte
		data []byte
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory content root rejects symlink %s", path)
		}
		if !item.IsDir() && directoryAtomicTempNamePattern.MatchString(item.Name()) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if item.IsDir() {
			entries = append(entries, entry{path: relative, kind: 'd'})
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("directory content root rejects special file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{path: relative, kind: 'f', data: data})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		return entries[i].kind < entries[j].kind
	})
	hash := sha256.New()
	_, _ = hash.Write([]byte("directory-content-root.v1\x00"))
	var size [8]byte
	for _, item := range entries {
		binary.BigEndian.PutUint64(size[:], uint64(len(item.path)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(item.path))
		_, _ = hash.Write([]byte{item.kind})
		binary.BigEndian.PutUint64(size[:], uint64(len(item.data)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(item.data)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func requireSameFilesystem(paths ...string) error {
	var device uint64
	for i, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot determine filesystem device for %s", path)
		}
		current := uint64(stat.Dev)
		if i == 0 {
			device = current
			continue
		}
		if current != device {
			return fmt.Errorf("live, candidate, and transaction_root must share one filesystem")
		}
	}
	return nil
}

func writeDirectoryPublishJSONNoReplace(path string, value any) (bool, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("journal path %s is not a regular file", path)
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if sameDirectoryPublishJSON(existing, raw) {
			return false, nil
		}
		return false, fmt.Errorf("journal path %s already contains different content", path)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return false, err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tempPath, path); err != nil {
		if os.IsExist(err) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && sameDirectoryPublishJSON(existing, raw) {
				return false, nil
			}
		}
		return false, err
	}
	if err := syncDirectoryPublishParents(filepath.Dir(path)); err != nil {
		return false, err
	}
	return true, nil
}

func readDirectoryPublishJSON(path string, value any) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("journal path %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return false, fmt.Errorf("journal path %s contains multiple JSON values", path)
		}
		return false, err
	}
	return true, nil
}

func sameDirectoryPublishJSON(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(left, right)
	}
	leftRaw, leftErr := json.Marshal(leftValue)
	rightRaw, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func syncDirectoryPublishParents(paths ...string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		dir, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil && err != syscall.EINVAL && err != syscall.ENOTSUP {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func removeDirectoryIfExact(path, expectedRoot string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	root, err := directoryContentRoot(path)
	if err != nil {
		return err
	}
	if root != expectedRoot {
		return fmt.Errorf("refusing to remove %s: root got %s want %s", path, root, expectedRoot)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return syncDirectoryPublishParents(filepath.Dir(path))
}

func removeEmptyDirectory(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDirectoryPublishParents(filepath.Dir(path))
}

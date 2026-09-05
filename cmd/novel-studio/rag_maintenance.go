package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const ragMaintenanceReportVersion = "rag-maintenance-report.v1"

type ragMaintenanceOptions struct {
	Root    string
	Apply   bool
	Compact bool
	JSON    bool
	Report  string
}

type ragMaintenanceReport struct {
	Version          string               `json:"version"`
	Root             string               `json:"root"`
	GeneratedAt      string               `json:"generated_at"`
	Applied          bool                 `json:"applied"`
	CanonicalHealthy bool                 `json:"canonical_healthy"`
	AllReadable      bool                 `json:"all_readable"`
	DirectoryCount   int                  `json:"directory_count"`
	CanonicalCount   int                  `json:"canonical_count"`
	HistoricalCount  int                  `json:"historical_count"`
	LogicalBytes     int64                `json:"logical_bytes"`
	IndexChunks      int                  `json:"index_chunks"`
	VectorPoints     int                  `json:"vector_points"`
	IssueCounts      map[string]int       `json:"issue_counts,omitempty"`
	KindCounts       map[string]int       `json:"kind_counts,omitempty"`
	Directories      []ragDirectoryAudit  `json:"directories"`
	Repairs          []ragDirectoryRepair `json:"repairs,omitempty"`
	Compaction       *ragCompactionReport `json:"compaction,omitempty"`
}

type ragDirectoryAudit struct {
	Path              string         `json:"path"`
	Kind              string         `json:"kind"`
	Canonical         bool           `json:"canonical"`
	Readable          bool           `json:"readable"`
	IndexBytes        int64          `json:"index_bytes,omitempty"`
	VectorBytes       int64          `json:"vector_bytes,omitempty"`
	SchemaVersion     int            `json:"schema_version,omitempty"`
	Chunks            int            `json:"chunks,omitempty"`
	FactChunks        int            `json:"fact_chunks,omitempty"`
	DesignChunks      int            `json:"design_chunks,omitempty"`
	VectorPoints      int            `json:"vector_points,omitempty"`
	SourceFiles       int            `json:"source_files,omitempty"`
	PendingChunks     int            `json:"pending_chunks,omitempty"`
	RetrievalTraces   int            `json:"retrieval_traces,omitempty"`
	CraftRecallEvents int            `json:"craft_recall_events,omitempty"`
	CraftReceipts     int            `json:"craft_receipts,omitempty"`
	FactReceipts      int            `json:"fact_receipts,omitempty"`
	Collection        string         `json:"collection,omitempty"`
	EmbeddingModel    string         `json:"embedding_model,omitempty"`
	VectorDimension   int            `json:"vector_dimension,omitempty"`
	Issues            map[string]int `json:"issues,omitempty"`
	Errors            []string       `json:"errors,omitempty"`
}

type ragDirectoryRepair struct {
	Path             string   `json:"path"`
	Changed          bool     `json:"changed"`
	Backup           string   `json:"backup,omitempty"`
	MigratedSchema   bool     `json:"migrated_schema,omitempty"`
	RehashedChunks   int      `json:"rehashed_chunks,omitempty"`
	RemovedChunks    int      `json:"removed_chunks,omitempty"`
	RemovedVectors   int      `json:"removed_vectors,omitempty"`
	RemappedVectors  int      `json:"remapped_vectors,omitempty"`
	NormalizedPoints int      `json:"normalized_points,omitempty"`
	NormalizedLogs   int      `json:"normalized_logs,omitempty"`
	Errors           []string `json:"errors,omitempty"`
}

type ragCompactionReport struct {
	FilesScanned      int   `json:"files_scanned"`
	DuplicateGroups   int   `json:"duplicate_groups"`
	RedundantFiles    int   `json:"redundant_files"`
	ReclaimableBytes  int64 `json:"reclaimable_bytes"`
	FilesLinked       int   `json:"files_linked"`
	BytesReclaimed    int64 `json:"bytes_reclaimed"`
	AlreadyLinked     int   `json:"already_linked"`
	IneligibleFiles   int   `json:"ineligible_files,omitempty"`
	VerificationFails int   `json:"verification_failures,omitempty"`
}

type ragCompactionCandidate struct {
	path    string
	size    int64
	mode    fs.FileMode
	modNano int64
	dev     uint64
	ino     uint64
	hash    string
}

type ragPairAnalysis struct {
	Readable        bool
	SchemaVersion   int
	Chunks          int
	FactChunks      int
	DesignChunks    int
	VectorPoints    int
	SourceFiles     int
	Collection      string
	EmbeddingModel  string
	VectorDimension int
	Issues          map[string]int
	Errors          []string
}

type ragArtifactFingerprint struct {
	Hash string
	Size int64
	Err  error
}

func runRAGCommand(argv []string) int {
	if len(argv) == 0 || hasHelpToken(argv) {
		printRAGCommandUsage(os.Stdout)
		return 0
	}
	subcommand := argv[0]
	switch subcommand {
	case "audit":
		return runRAGMaintenance(argv[1:], false)
	case "maintain":
		return runRAGMaintenance(argv[1:], true)
	default:
		fmt.Fprintf(os.Stderr, "rag: unknown subcommand %q\n", subcommand)
		printRAGCommandUsage(os.Stderr)
		return 2
	}
}

func printRAGCommandUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  novel-studio rag audit [--root data/runs] [--json]")
	fmt.Fprintln(w, "  novel-studio rag maintain [--root data/runs] --apply [--compact=true]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "audit scans every canonical, projected, retired and archived RAG snapshot without changing data.")
	fmt.Fprintln(w, "maintain repairs canonical indexes with recoverable backups and content-deduplicates byte-identical snapshots.")
}

func runRAGMaintenance(argv []string, maintain bool) int {
	fs := flag.NewFlagSet("rag", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	opts := ragMaintenanceOptions{Root: filepath.Join("data", "runs"), Compact: true}
	fs.StringVar(&opts.Root, "root", opts.Root, "root containing novel run directories")
	fs.BoolVar(&opts.Apply, "apply", false, "apply canonical repairs and snapshot compaction")
	fs.BoolVar(&opts.Compact, "compact", opts.Compact, "deduplicate byte-identical RAG snapshots during maintain")
	fs.BoolVar(&opts.JSON, "json", false, "print the complete machine-readable report")
	fs.StringVar(&opts.Report, "report", "", "report path; defaults to <root>/rag-maintenance-report.json when applying")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "rag: too many arguments: %v\n", fs.Args())
		return 2
	}
	if !maintain && opts.Apply {
		fmt.Fprintln(os.Stderr, "rag audit is read-only; use 'rag maintain --apply' to change data")
		return 2
	}
	if maintain && !opts.Apply {
		fmt.Fprintln(os.Stderr, "rag maintain defaults to a dry run; pass --apply after reviewing the report")
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rag: resolve root: %v\n", err)
		return 1
	}
	opts.Root = filepath.Clean(absRoot)
	if info, statErr := os.Stat(opts.Root); statErr != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "rag: root is not a directory: %s\n", opts.Root)
		return 1
	}

	report, err := auditRAGTree(opts.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rag: audit: %v\n", err)
		return 1
	}
	if maintain && opts.Apply {
		stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
		for _, dir := range report.Directories {
			if !dir.Canonical {
				continue
			}
			repair := repairCanonicalRAGDirectory(dir.Path, stamp)
			report.Repairs = append(report.Repairs, repair)
		}
		if opts.Compact {
			compaction, compactErr := compactRAGSnapshots(opts.Root, true)
			if compactErr != nil {
				fmt.Fprintf(os.Stderr, "rag: compact: %v\n", compactErr)
				return 1
			}
			report.Compaction = &compaction
		}
		final, finalErr := auditRAGTree(opts.Root)
		if finalErr != nil {
			fmt.Fprintf(os.Stderr, "rag: final audit: %v\n", finalErr)
			return 1
		}
		final.Applied = true
		final.Repairs = report.Repairs
		final.Compaction = report.Compaction
		report = final
		if err := writeRAGHealthSnapshots(report); err != nil {
			fmt.Fprintf(os.Stderr, "rag: write health snapshots: %v\n", err)
			return 1
		}
	} else if maintain && opts.Compact {
		compaction, compactErr := compactRAGSnapshots(opts.Root, false)
		if compactErr != nil {
			fmt.Fprintf(os.Stderr, "rag: compact audit: %v\n", compactErr)
			return 1
		}
		report.Compaction = &compaction
	}

	if opts.Apply {
		path := strings.TrimSpace(opts.Report)
		if path == "" {
			path = filepath.Join(opts.Root, "rag-maintenance-report.json")
		}
		if err := writeRAGMaintenanceReport(path, report); err != nil {
			fmt.Fprintf(os.Stderr, "rag: write report: %v\n", err)
			return 1
		}
	}
	if opts.JSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "rag: encode report: %v\n", err)
			return 1
		}
	} else {
		printRAGMaintenanceReport(report)
	}
	if !report.AllReadable || !report.CanonicalHealthy || hasRAGRepairErrors(report.Repairs) {
		return 1
	}
	return 0
}

func auditRAGTree(root string) (ragMaintenanceReport, error) {
	dirs, err := discoverRAGDirectories(root)
	if err != nil {
		return ragMaintenanceReport{}, err
	}
	report := ragMaintenanceReport{
		Version:          ragMaintenanceReportVersion,
		Root:             root,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		CanonicalHealthy: true,
		AllReadable:      true,
		IssueCounts:      map[string]int{},
		KindCounts:       map[string]int{},
	}
	cache := map[string]ragPairAnalysis{}
	fingerprints := map[string]ragArtifactFingerprint{}
	for _, dir := range dirs {
		audit := auditRAGDirectory(root, dir, cache, fingerprints)
		report.Directories = append(report.Directories, audit)
		report.DirectoryCount++
		report.KindCounts[audit.Kind]++
		report.LogicalBytes += audit.IndexBytes + audit.VectorBytes
		report.IndexChunks += audit.Chunks
		report.VectorPoints += audit.VectorPoints
		if audit.Canonical {
			report.CanonicalCount++
			if !audit.Readable || canonicalRAGHasBlockingIssues(audit.Issues) {
				report.CanonicalHealthy = false
			}
		} else {
			report.HistoricalCount++
		}
		if !audit.Readable {
			report.AllReadable = false
		}
		for issue, count := range audit.Issues {
			report.IssueCounts[issue] += count
		}
	}
	return report, nil
}

func discoverRAGDirectories(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && entry.Name() == "rag" && filepath.Base(filepath.Dir(path)) == "meta" {
			dirs = append(dirs, filepath.Clean(path))
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

func auditRAGDirectory(root, dir string, cache map[string]ragPairAnalysis, fingerprints map[string]ragArtifactFingerprint) ragDirectoryAudit {
	kind, canonical := classifyRAGDirectory(root, dir)
	audit := ragDirectoryAudit{Path: dir, Kind: kind, Canonical: canonical, Readable: true, Issues: map[string]int{}}
	indexPath := filepath.Join(dir, "index_state.json")
	vectorPath := filepath.Join(dir, "vector_store.json")
	indexFP := fingerprintRAGArtifact(indexPath, fingerprints)
	vectorFP := fingerprintRAGArtifact(vectorPath, fingerprints)
	indexHash, indexErr := indexFP.Hash, indexFP.Err
	vectorHash, vectorErr := vectorFP.Hash, vectorFP.Err
	audit.IndexBytes = indexFP.Size
	audit.VectorBytes = vectorFP.Size
	if indexErr != nil && !os.IsNotExist(indexErr) {
		audit.Readable = false
		audit.Errors = append(audit.Errors, "index_state: "+indexErr.Error())
	}
	if vectorErr != nil && !os.IsNotExist(vectorErr) {
		audit.Readable = false
		audit.Errors = append(audit.Errors, "vector_store: "+vectorErr.Error())
	}
	if os.IsNotExist(indexErr) {
		audit.Issues["missing_index_state"]++
	}
	key := indexHash + "\x00" + vectorHash
	analysis, cached := cache[key]
	if !cached {
		var indexRaw, vectorRaw []byte
		if indexErr == nil {
			indexRaw, indexErr = os.ReadFile(indexPath)
		}
		if vectorErr == nil {
			vectorRaw, vectorErr = os.ReadFile(vectorPath)
		}
		analysis = analyzeRAGArtifacts(indexRaw, indexErr, vectorRaw, vectorErr)
		cache[key] = analysis
	}
	audit.Readable = audit.Readable && analysis.Readable
	audit.SchemaVersion = analysis.SchemaVersion
	audit.Chunks = analysis.Chunks
	audit.FactChunks = analysis.FactChunks
	audit.DesignChunks = analysis.DesignChunks
	audit.VectorPoints = analysis.VectorPoints
	audit.SourceFiles = analysis.SourceFiles
	audit.Collection = analysis.Collection
	audit.EmbeddingModel = analysis.EmbeddingModel
	audit.VectorDimension = analysis.VectorDimension
	mergeIssueCounts(audit.Issues, analysis.Issues)
	audit.Errors = append(audit.Errors, analysis.Errors...)

	outputDir := filepath.Dir(filepath.Dir(dir))
	st := store.NewStore(outputDir)
	if pending, err := st.RAG.LoadPendingUpserts(); err == nil && pending != nil {
		audit.PendingChunks = len(pending.Chunks)
		if len(pending.Chunks) > 0 {
			audit.Issues["pending_upserts"] += len(pending.Chunks)
		}
	} else if err != nil {
		audit.Readable = false
		audit.Errors = append(audit.Errors, "pending_upserts: "+err.Error())
	}
	audit.RetrievalTraces, audit.Issues["invalid_retrieval_trace_rows"] = auditJSONLines(filepath.Join(dir, "retrieval_trace.jsonl"))
	audit.CraftRecallEvents, audit.Issues["invalid_craft_recall_rows"] = auditJSONLines(filepath.Join(dir, "craft_recall_log.jsonl"))
	audit.CraftReceipts = countJSONFiles(filepath.Join(dir, "craft_receipts"), &audit)
	audit.FactReceipts = countJSONFiles(filepath.Join(dir, "fact_receipts"), &audit)
	removeZeroIssues(audit.Issues)
	return audit
}

func analyzeRAGArtifacts(indexRaw []byte, indexErr error, vectorRaw []byte, vectorErr error) ragPairAnalysis {
	analysis := ragPairAnalysis{Readable: true, Issues: map[string]int{}}
	var state domain.RAGIndexState
	if indexErr == nil {
		if err := json.Unmarshal(indexRaw, &state); err != nil {
			analysis.Readable = false
			analysis.Errors = append(analysis.Errors, "index_state JSON: "+err.Error())
		} else {
			analyzeRAGIndexState(&analysis, &state)
		}
	} else if !os.IsNotExist(indexErr) {
		analysis.Readable = false
	}
	var vectors domain.RAGVectorStore
	if vectorErr == nil {
		if err := json.Unmarshal(vectorRaw, &vectors); err != nil {
			analysis.Readable = false
			analysis.Errors = append(analysis.Errors, "vector_store JSON: "+err.Error())
		} else {
			analyzeRAGVectorStore(&analysis, &state, &vectors)
		}
	} else if !os.IsNotExist(vectorErr) {
		analysis.Readable = false
	} else if state.Config.EmbeddingModel != "" || state.Config.VectorDimension > 0 {
		analysis.Issues["missing_vector_store"]++
	}
	removeZeroIssues(analysis.Issues)
	return analysis
}

func analyzeRAGIndexState(analysis *ragPairAnalysis, state *domain.RAGIndexState) {
	analysis.SchemaVersion = state.SchemaVersion
	analysis.Chunks = len(state.Chunks)
	analysis.Collection = state.Config.Collection
	analysis.EmbeddingModel = state.Config.EmbeddingModel
	analysis.VectorDimension = state.Config.VectorDimension
	if state.SchemaVersion != domain.CurrentRAGIndexSchemaVersion {
		analysis.Issues["legacy_schema"]++
	}
	seenIDs := map[string]struct{}{}
	seenHashes := map[string]struct{}{}
	sources := map[string]struct{}{}
	factHashes := map[string]struct{}{}
	for _, persisted := range state.Chunks {
		chunk := rag.NormalizeChunk(persisted)
		if strings.TrimSpace(persisted.ID) == "" || strings.TrimSpace(persisted.Hash) == "" || strings.TrimSpace(persisted.SourcePath) == "" {
			analysis.Issues["incomplete_chunks"]++
		}
		if rag.RehashChunk(persisted).Hash != strings.TrimSpace(persisted.Hash) {
			analysis.Issues["stale_chunk_hashes"]++
		}
		if _, duplicate := seenIDs[chunk.ID]; duplicate {
			analysis.Issues["duplicate_chunk_ids"]++
		}
		seenIDs[chunk.ID] = struct{}{}
		if _, duplicate := seenHashes[chunk.Hash]; duplicate {
			analysis.Issues["duplicate_chunk_hashes"]++
		}
		seenHashes[chunk.Hash] = struct{}{}
		if source := strings.TrimSpace(chunk.SourcePath); source != "" {
			sources[source] = struct{}{}
			if filepath.IsAbs(source) {
				analysis.Issues["absolute_source_paths"]++
			}
		}
		if rag.IsForbiddenChunk(chunk) {
			analysis.Issues["forbidden_chunks"]++
		}
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			analysis.DesignChunks++
		} else {
			analysis.FactChunks++
			factHashes[chunk.Hash] = struct{}{}
		}
	}
	analysis.SourceFiles = len(sources)
	declared := map[string]int{}
	for _, hash := range state.ChunkHashes {
		declared[strings.TrimSpace(hash)]++
	}
	for hash := range seenHashes {
		if declared[hash] == 0 {
			analysis.Issues["chunk_hash_list_missing"]++
		}
	}
	for hash, count := range declared {
		if hash == "" || count > 1 {
			analysis.Issues["chunk_hash_list_duplicates"] += max(1, count-1)
		}
		if _, exists := seenHashes[hash]; !exists {
			analysis.Issues["chunk_hash_list_orphans"]++
		}
	}
}

func analyzeRAGVectorStore(analysis *ragPairAnalysis, state *domain.RAGIndexState, vectors *domain.RAGVectorStore) {
	analysis.VectorPoints = len(vectors.Points)
	if analysis.Collection == "" {
		analysis.Collection = vectors.Config.Collection
	}
	if analysis.EmbeddingModel == "" {
		analysis.EmbeddingModel = vectors.Config.EmbeddingModel
	}
	if analysis.VectorDimension == 0 {
		analysis.VectorDimension = vectors.Config.VectorDimension
	}
	if state.Config.VectorDimension > 0 && vectors.Config.VectorDimension != state.Config.VectorDimension {
		analysis.Issues["vector_config_dimension_mismatch"]++
	}
	if state.Config.EmbeddingModel != "" && vectors.Config.EmbeddingModel != state.Config.EmbeddingModel {
		analysis.Issues["vector_config_model_mismatch"]++
	}
	if state.Config.EmbeddingProvider != "" && vectors.Config.EmbeddingProvider != state.Config.EmbeddingProvider {
		analysis.Issues["vector_config_provider_mismatch"]++
	}
	allowed := map[string]domain.RAGChunk{}
	for _, chunk := range state.Chunks {
		chunk = rag.NormalizeChunk(chunk)
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			allowed[chunk.Hash] = chunk
		}
	}
	seenIDs := map[string]struct{}{}
	seenHashes := map[string]struct{}{}
	for _, point := range vectors.Points {
		chunk := rag.NormalizeChunk(point.Chunk)
		id := strings.TrimSpace(point.ID)
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = chunk.Hash
		}
		if id == "" || hash == "" {
			analysis.Issues["incomplete_vector_points"]++
		}
		if _, duplicate := seenIDs[id]; duplicate {
			analysis.Issues["duplicate_vector_ids"]++
		}
		seenIDs[id] = struct{}{}
		if _, duplicate := seenHashes[hash]; duplicate {
			analysis.Issues["duplicate_vector_hashes"]++
		}
		seenHashes[hash] = struct{}{}
		want, exists := allowed[hash]
		if !exists {
			analysis.Issues["orphan_vector_points"]++
		} else if id != want.ID || chunk.ID != want.ID || chunk.Hash != want.Hash {
			analysis.Issues["vector_chunk_mismatch"]++
		}
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			analysis.Issues["design_only_vectors"]++
		}
		if vectors.Config.VectorDimension > 0 && len(point.Vector) != vectors.Config.VectorDimension {
			analysis.Issues["vector_dimension_mismatch"]++
		}
		if err := rag.ValidateVector(point.Vector); err != nil {
			analysis.Issues["invalid_vectors"]++
		}
		if payloadHash, _ := point.Payload["hash"].(string); payloadHash != "" && payloadHash != hash {
			analysis.Issues["stale_vector_payloads"]++
		}
	}
	for hash := range allowed {
		if _, exists := seenHashes[hash]; !exists {
			analysis.Issues["missing_vector_points"]++
		}
	}
}

func repairCanonicalRAGDirectory(ragDir, stamp string) ragDirectoryRepair {
	repair := ragDirectoryRepair{Path: ragDir}
	outputDir := filepath.Dir(filepath.Dir(ragDir))
	st := store.NewStore(outputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil {
		if err == nil {
			err = fmt.Errorf("index_state does not exist")
		}
		repair.Errors = append(repair.Errors, err.Error())
		return repair
	}
	vectors, err := st.RAG.LoadVectorStore()
	if err != nil {
		repair.Errors = append(repair.Errors, err.Error())
		return repair
	}
	beforeState := *state
	beforeState.Chunks = append([]domain.RAGChunk(nil), state.Chunks...)
	beforeState.ChunkHashes = append([]string(nil), state.ChunkHashes...)
	var beforeVectors *domain.RAGVectorStore
	if vectors != nil {
		copyValue := *vectors
		copyValue.Points = append([]domain.RAGVectorPoint(nil), vectors.Points...)
		beforeVectors = &copyValue
	}
	repair.MigratedSchema = migrateRAGIndexSchema(state)
	removed, rehashed, normalizeErr := normalizeRAGIndexForMaintenance(st, state)
	if normalizeErr != nil {
		repair.Errors = append(repair.Errors, normalizeErr.Error())
		return repair
	}
	repair.RemovedChunks = removed
	repair.RehashedChunks = rehashed
	if vectors != nil {
		repair.RemovedVectors, repair.RemappedVectors = sanitizeRAGVectorStore(st, vectors, state)
		repair.NormalizedPoints += normalizeRAGVectorPoints(vectors, state)
	}
	normalizedLogs := map[string][]byte{}
	for _, name := range []string{"retrieval_trace.jsonl", "craft_recall_log.jsonl"} {
		path := filepath.Join(ragDir, name)
		normalized, changed, normalizeErr := normalizedRAGJSONLines(path)
		if normalizeErr != nil {
			repair.Errors = append(repair.Errors, normalizeErr.Error())
			return repair
		}
		if changed {
			normalizedLogs[path] = normalized
			repair.NormalizedLogs++
		}
	}
	stateChanged := !reflect.DeepEqual(beforeState, *state)
	vectorChanged := !reflect.DeepEqual(beforeVectors, vectors)
	if stateChanged {
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if !stateChanged && !vectorChanged && len(normalizedLogs) == 0 {
		return repair
	}
	backup, backupErr := backupRAGCanonicalArtifacts(outputDir, stamp)
	if backupErr != nil {
		repair.Errors = append(repair.Errors, backupErr.Error())
		return repair
	}
	repair.Backup = backup
	if vectorChanged && vectors != nil {
		if err := st.RAG.SaveVectorStore(*vectors); err != nil {
			repair.Errors = append(repair.Errors, "save vector_store: "+err.Error())
			return repair
		}
	}
	if stateChanged {
		if err := st.RAG.SaveIndexState(*state); err != nil {
			repair.Errors = append(repair.Errors, "save index_state: "+err.Error())
			return repair
		}
	}
	for path, normalized := range normalizedLogs {
		if err := atomicWriteRewriteFile(path, normalized, 0o644); err != nil {
			repair.Errors = append(repair.Errors, "normalize JSONL: "+err.Error())
			return repair
		}
	}
	repair.Changed = true
	return repair
}

func normalizeRAGIndexForMaintenance(st *store.Store, state *domain.RAGIndexState) (removed, rehashed int, err error) {
	filtered := make([]domain.RAGChunk, 0, len(state.Chunks))
	seenHashes := make(map[string]struct{}, len(state.Chunks))
	seenIDs := make(map[string]string, len(state.Chunks))
	for _, persisted := range state.Chunks {
		chunk := rag.NormalizeChunk(persisted)
		current := rag.RehashChunk(chunk)
		if current.Hash != chunk.Hash {
			chunk = current
			rehashed++
		}
		if strings.TrimSpace(chunk.SourcePath) == "" || (strings.TrimSpace(chunk.Text) == "" && strings.TrimSpace(chunk.Summary) == "") ||
			rag.IsForbiddenChunk(chunk) || ragChunkHasProjectContamination(st, chunk) {
			removed++
			continue
		}
		if _, duplicate := seenHashes[chunk.Hash]; duplicate {
			removed++
			continue
		}
		if priorHash, duplicate := seenIDs[chunk.ID]; duplicate && priorHash != chunk.Hash {
			return 0, 0, fmt.Errorf("conflicting chunk id %s has multiple content hashes; rebuild the project RAG", chunk.ID)
		}
		seenHashes[chunk.Hash] = struct{}{}
		seenIDs[chunk.ID] = chunk.Hash
		filtered = append(filtered, chunk)
	}
	state.SchemaVersion = domain.CurrentRAGIndexSchemaVersion
	state.Chunks = filtered
	state.ChunkHashes = rebuildRAGChunkHashList(filtered)
	state.SanitizedDigest = ""
	_ = sanitizeRAGIndexState(st, state)
	if removed > 0 || rehashed > 0 {
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return removed, rehashed, nil
}

func normalizeRAGVectorPoints(vectors *domain.RAGVectorStore, state *domain.RAGIndexState) int {
	if vectors == nil || state == nil {
		return 0
	}
	allowed := map[string]domain.RAGChunk{}
	for _, chunk := range state.Chunks {
		chunk = rag.NormalizeChunk(chunk)
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			allowed[chunk.Hash] = chunk
		}
	}
	changed := 0
	for index := range vectors.Points {
		point := &vectors.Points[index]
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = strings.TrimSpace(point.Chunk.Hash)
		}
		chunk, ok := allowed[hash]
		if !ok {
			continue
		}
		payload := rag.PayloadForChunk(chunk)
		if point.ID != chunk.ID || point.Hash != chunk.Hash || !reflect.DeepEqual(point.Chunk, chunk) || !ragJSONValuesEqual(point.Payload, payload) {
			point.ID = chunk.ID
			point.Hash = chunk.Hash
			point.Chunk = chunk
			point.Payload = payload
			point.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			changed++
		}
	}
	if changed > 0 {
		vectors.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return changed
}

// Payload maps round-trip through encoding/json as map[string]any, so slices
// such as keywords load as []any even when the in-memory producer used
// []string. Compare their canonical JSON representation to keep maintenance
// idempotent instead of rewriting every vector point on every audit.
func ragJSONValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func backupRAGCanonicalArtifacts(outputDir, stamp string) (string, error) {
	backup := filepath.Join(outputDir, "meta", "rag-maintenance-backups", stamp)
	if err := os.MkdirAll(backup, 0o755); err != nil {
		return "", err
	}
	checksums := map[string]string{}
	for _, name := range []string{
		"index_state.json", "index_state.md", "vector_store.json", "vector_store.md", "pending_upserts.json",
		"retrieval_trace.jsonl", "craft_recall_log.jsonl",
	} {
		source := filepath.Join(outputDir, "meta", "rag", name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("refuse to back up non-regular RAG artifact %s", source)
		}
		target := filepath.Join(backup, name)
		if err := linkOrCopyRAGFile(source, target, info.Mode().Perm()); err != nil {
			return "", err
		}
		_, digest, _, err := readRAGArtifact(source)
		if err != nil {
			return "", err
		}
		checksums[name] = digest
	}
	manifest := map[string]any{
		"version": "rag-maintenance-backup.v1", "created_at": time.Now().UTC().Format(time.RFC3339Nano), "sha256": checksums,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := atomicWriteRewriteFile(filepath.Join(backup, "manifest.json"), append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

func compactRAGSnapshots(root string, apply bool) (ragCompactionReport, error) {
	files, err := discoverRAGSnapshotArtifacts(root)
	if err != nil {
		return ragCompactionReport{}, err
	}
	report := ragCompactionReport{FilesScanned: len(files)}
	groups := map[string][]ragCompactionCandidate{}
	fingerprints := map[string]ragArtifactFingerprint{}
	for _, path := range files {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return report, statErr
		}
		if !info.Mode().IsRegular() {
			report.IneligibleFiles++
			continue
		}
		fingerprint := fingerprintRAGArtifact(path, fingerprints)
		if fingerprint.Err != nil {
			return report, fingerprint.Err
		}
		digest, size := fingerprint.Hash, fingerprint.Size
		dev, ino := ragFileIdentity(info)
		key := fmt.Sprintf("%s:%d:%d:%d:%o", digest, size, info.ModTime().UnixNano(), dev, info.Mode().Perm())
		groups[key] = append(groups[key], ragCompactionCandidate{
			path: path, size: size, mode: info.Mode().Perm(), modNano: info.ModTime().UnixNano(), dev: dev, ino: ino, hash: digest,
		})
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].path < group[j].path })
		report.DuplicateGroups++
		leader := group[0]
		for _, item := range group[1:] {
			report.RedundantFiles++
			if leader.dev == item.dev && leader.ino == item.ino {
				report.AlreadyLinked++
				continue
			}
			report.ReclaimableBytes += item.size
			if !apply {
				continue
			}
			if err := replaceWithVerifiedHardLink(leader, item); err != nil {
				report.VerificationFails++
				return report, err
			}
			report.FilesLinked++
			report.BytesReclaimed += item.size
		}
	}
	return report, nil
}

func discoverRAGSnapshotArtifacts(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != "index_state.json" && entry.Name() != "vector_store.json" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "rag" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "meta" {
			return nil
		}
		files = append(files, filepath.Clean(path))
		return nil
	})
	sort.Strings(files)
	return files, err
}

func replaceWithVerifiedHardLink(leader, target ragCompactionCandidate) error {
	leaderInfo, err := os.Lstat(leader.path)
	if err != nil {
		return err
	}
	targetInfo, err := os.Lstat(target.path)
	if err != nil {
		return err
	}
	leaderDev, leaderIno := ragFileIdentity(leaderInfo)
	targetDev, targetIno := ragFileIdentity(targetInfo)
	if leaderDev != targetDev || leaderInfo.Size() != targetInfo.Size() ||
		leaderInfo.ModTime().UnixNano() != targetInfo.ModTime().UnixNano() || leaderInfo.Mode().Perm() != targetInfo.Mode().Perm() ||
		leaderDev != leader.dev || leaderIno != leader.ino || targetDev != target.dev || targetIno != target.ino {
		return fmt.Errorf("RAG artifact changed during compaction: %s", target.path)
	}
	temp, err := os.CreateTemp(filepath.Dir(target.path), ".rag-hardlink-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.Link(leader.path, tempPath); err != nil {
		return err
	}
	return os.Rename(tempPath, target.path)
}

func linkOrCopyRAGFile(source, target string, mode fs.FileMode) error {
	if err := os.Link(source, target); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func readRAGArtifact(path string) ([]byte, string, int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", 0, err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), int64(len(raw)), nil
}

func fingerprintRAGArtifact(path string, cache map[string]ragArtifactFingerprint) ragArtifactFingerprint {
	info, err := os.Lstat(path)
	if err != nil {
		return ragArtifactFingerprint{Err: err}
	}
	dev, ino := ragFileIdentity(info)
	key := fmt.Sprintf("%d:%d:%d:%d", dev, ino, info.Size(), info.ModTime().UnixNano())
	if prior, ok := cache[key]; ok {
		return prior
	}
	file, err := os.Open(path)
	if err != nil {
		return ragArtifactFingerprint{Err: err}
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return ragArtifactFingerprint{Err: copyErr}
	}
	if closeErr != nil {
		return ragArtifactFingerprint{Err: closeErr}
	}
	result := ragArtifactFingerprint{Hash: hex.EncodeToString(hasher.Sum(nil)), Size: info.Size()}
	cache[key] = result
	return result
}

func auditJSONLines(path string) (valid, invalid int) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		return 0, 1
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if json.Valid([]byte(line)) {
			valid++
		} else {
			invalid++
		}
	}
	if scanner.Err() != nil {
		invalid++
	}
	return valid, invalid
}

func normalizedRAGJSONLines(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var normalized strings.Builder
	rows := 0
	for {
		var value json.RawMessage
		err := decoder.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("invalid RAG JSONL %s after %d rows: %w", path, rows, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return nil, false, err
		}
		normalized.Write(compact.Bytes())
		normalized.WriteByte('\n')
		rows++
	}
	result := []byte(normalized.String())
	return result, !reflect.DeepEqual(raw, result), nil
}

func countJSONFiles(root string, audit *ragDirectoryAudit) int {
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		audit.Readable = false
		audit.Errors = append(audit.Errors, err.Error())
	}
	return count
}

func classifyRAGDirectory(root, dir string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	if err == nil {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 5 && parts[1] == "output" && parts[2] == "novel" && parts[3] == "meta" && parts[4] == "rag" {
			return "canonical", true
		}
	}
	path := "/" + strings.ToLower(filepath.ToSlash(dir)) + "/"
	switch {
	case strings.Contains(path, "/archives/") || strings.Contains(path, "/_archive/"):
		return "archive", false
	case strings.Contains(path, "/.render-candidates/"):
		return "render_candidate", false
	case strings.Contains(path, "/.project-all/"):
		return "project_all", false
	case strings.Contains(path, "/.outline-all/"):
		return "outline_all", false
	case strings.Contains(path, "/.canon-rebase/"):
		return "canon_rebase", false
	case strings.Contains(path, "/.brainstorm-staging/"):
		return "brainstorm_staging", false
	default:
		return "derived", false
	}
}

func canonicalRAGHasBlockingIssues(issues map[string]int) bool {
	for issue, count := range issues {
		if count == 0 {
			continue
		}
		switch issue {
		case "absolute_source_paths":
			continue
		default:
			return true
		}
	}
	return false
}

func mergeIssueCounts(target, source map[string]int) {
	for key, value := range source {
		target[key] += value
	}
}

func removeZeroIssues(issues map[string]int) {
	for key, value := range issues {
		if value == 0 {
			delete(issues, key)
		}
	}
}

func ragFileIdentity(info os.FileInfo) (dev, ino uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev), uint64(stat.Ino)
	}
	return 0, 0
}

func writeRAGMaintenanceReport(path string, report ragMaintenanceReport) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteRewriteFile(abs, append(raw, '\n'), 0o644)
}

func writeRAGHealthSnapshots(report ragMaintenanceReport) error {
	for _, dir := range report.Directories {
		if !dir.Canonical {
			continue
		}
		health := map[string]any{
			"version":          "rag-health.v1",
			"checked_at":       report.GeneratedAt,
			"healthy":          dir.Readable && !canonicalRAGHasBlockingIssues(dir.Issues),
			"readable":         dir.Readable,
			"schema_version":   dir.SchemaVersion,
			"chunks":           dir.Chunks,
			"fact_chunks":      dir.FactChunks,
			"design_chunks":    dir.DesignChunks,
			"vector_points":    dir.VectorPoints,
			"pending_chunks":   dir.PendingChunks,
			"collection":       dir.Collection,
			"embedding_model":  dir.EmbeddingModel,
			"vector_dimension": dir.VectorDimension,
			"issues":           dir.Issues,
		}
		raw, err := json.MarshalIndent(health, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWriteRewriteFile(filepath.Join(dir.Path, "health.json"), append(raw, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func printRAGMaintenanceReport(report ragMaintenanceReport) {
	fmt.Printf("RAG data audit: %s\n", report.Root)
	fmt.Printf("directories=%d canonical=%d historical_or_derived=%d logical=%s\n",
		report.DirectoryCount, report.CanonicalCount, report.HistoricalCount, formatRAGBytes(report.LogicalBytes))
	fmt.Printf("chunks=%d vector_points=%d canonical_healthy=%t all_readable=%t applied=%t\n",
		report.IndexChunks, report.VectorPoints, report.CanonicalHealthy, report.AllReadable, report.Applied)
	if len(report.IssueCounts) > 0 {
		keys := make([]string, 0, len(report.IssueCounts))
		for key := range report.IssueCounts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Println("issues:")
		for _, key := range keys {
			fmt.Printf("  %s=%d\n", key, report.IssueCounts[key])
		}
	}
	for _, dir := range report.Directories {
		if !dir.Canonical {
			continue
		}
		fmt.Printf("canonical: %s schema=%d chunks=%d facts=%d vectors=%d pending=%d issues=%d\n",
			dir.Path, dir.SchemaVersion, dir.Chunks, dir.FactChunks, dir.VectorPoints, dir.PendingChunks, sumIssueCounts(dir.Issues))
	}
	if report.Compaction != nil {
		fmt.Printf("compaction: duplicate_groups=%d redundant=%d linked=%d reclaimable=%s reclaimed=%s\n",
			report.Compaction.DuplicateGroups, report.Compaction.RedundantFiles, report.Compaction.FilesLinked,
			formatRAGBytes(report.Compaction.ReclaimableBytes), formatRAGBytes(report.Compaction.BytesReclaimed))
	}
	for _, repair := range report.Repairs {
		fmt.Printf("repair: %s changed=%t migrated=%t rehashed=%d removed_chunks=%d removed_vectors=%d remapped_vectors=%d normalized_logs=%d backup=%s errors=%d\n",
			repair.Path, repair.Changed, repair.MigratedSchema, repair.RehashedChunks, repair.RemovedChunks,
			repair.RemovedVectors, repair.RemappedVectors, repair.NormalizedLogs, repair.Backup, len(repair.Errors))
	}
}

func sumIssueCounts(issues map[string]int) int {
	total := 0
	for _, count := range issues {
		total += count
	}
	return total
}

func hasRAGRepairErrors(repairs []ragDirectoryRepair) bool {
	for _, repair := range repairs {
		if len(repair.Errors) > 0 {
			return true
		}
	}
	return false
}

func formatRAGBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for value := bytes / unit; value >= unit && exp < 4; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

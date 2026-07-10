package diag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func loadPipelineEvidence(s *store.Store) (*domain.PipelineState, map[string][]string, map[string][]string, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir(), "meta", "pipeline.json"))
	if err != nil {
		return nil, nil, nil, err
	}
	var state domain.PipelineState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, nil, nil, err
	}

	missingArtifacts := make(map[string][]string)
	missingCheckpoints := make(map[string][]string)
	checkpoints := s.Checkpoints.All()

	for stage, evidence := range state.Evidence {
		for _, artifact := range evidence.Artifacts {
			if strings.TrimSpace(artifact) == "" {
				continue
			}
			if !pipelineArtifactExists(s.Dir(), artifact) {
				missingArtifacts[stage] = append(missingArtifacts[stage], artifact)
				continue
			}
			if expected := strings.TrimSpace(evidence.ArtifactDigests[artifact]); expected != "" {
				if actual, err := pipelineArtifactDigest(s.Dir(), artifact); err != nil || actual != expected {
					missingArtifacts[stage] = append(missingArtifacts[stage], artifact+" (digest mismatch)")
				}
			}
		}
		for _, ref := range evidence.Checkpoints {
			if strings.TrimSpace(ref) == "" {
				continue
			}
			if !pipelineCheckpointExists(checkpoints, ref) {
				missingCheckpoints[stage] = append(missingCheckpoints[stage], ref)
			}
		}
	}

	return &state, missingArtifacts, missingCheckpoints, nil
}

func pipelineArtifactDigest(root, artifact string) (string, error) {
	path := artifact
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(artifact))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func pipelineArtifactExists(root, artifact string) bool {
	path := artifact
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(artifact))
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func pipelineCheckpointExists(checkpoints []domain.Checkpoint, ref string) bool {
	kind, chapter, step, seq, ok := parsePipelineCheckpointRef(ref)
	if !ok {
		return false
	}
	for _, checkpoint := range checkpoints {
		if seq > 0 && checkpoint.Seq != seq {
			continue
		}
		if checkpoint.Step != step {
			continue
		}
		switch kind {
		case string(domain.ScopeChapter):
			if checkpoint.Scope.Kind == domain.ScopeChapter && checkpoint.Scope.Chapter == chapter {
				return true
			}
		}
	}
	return false
}

func parsePipelineCheckpointRef(ref string) (kind string, chapter int, step string, seq int64, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", 0, "", 0, false
	}
	main := ref
	if before, after, found := strings.Cut(ref, "#"); found {
		main = before
		parsed, err := strconv.ParseInt(after, 10, 64)
		if err != nil || parsed <= 0 {
			return "", 0, "", 0, false
		}
		seq = parsed
	}
	parts := strings.Split(main, ":")
	if len(parts) != 3 {
		return "", 0, "", 0, false
	}
	kind = parts[0]
	step = parts[2]
	if kind != string(domain.ScopeChapter) || step == "" {
		return "", 0, "", 0, false
	}
	parsedChapter, err := strconv.Atoi(parts[1])
	if err != nil || parsedChapter <= 0 {
		return "", 0, "", 0, false
	}
	return kind, parsedChapter, step, seq, true
}

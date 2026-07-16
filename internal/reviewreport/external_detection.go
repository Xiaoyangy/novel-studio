package reviewreport

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
)

// RegisteredExternalDetection is a human-triggered result from a named
// detector such as Zhuque.  Unlike the independent model judge, it represents
// the exact payload submitted outside novel-studio and therefore participates
// in gates only when BodySHA256 exactly matches the current prose bytes.
type RegisteredExternalDetection struct {
	Chapter        int      `json:"chapter"`
	Detector       string   `json:"detector"`
	Mode           string   `json:"mode"`
	Score          float64  `json:"score"`
	ScoreScale     string   `json:"score_scale,omitempty"`
	ScorePercent   *float64 `json:"score_percent,omitempty"`
	Verdict        string   `json:"verdict"`
	Note           string   `json:"note,omitempty"`
	BodySHA256     string   `json:"body_sha256"`
	PayloadPath    string   `json:"payload_path,omitempty"`
	EvidencePath   string   `json:"evidence_path,omitempty"`
	EvidenceSHA256 string   `json:"evidence_sha256,omitempty"`
	CheckedAt      string   `json:"checked_at"`

	NormalizedScorePercent float64 `json:"-"`
}

// NormalizeRegisteredExternalScore accepts explicit probability/percent rows
// and remains compatible with historical rows that predate score_scale.
func NormalizeRegisteredExternalScore(row RegisteredExternalDetection) (float64, error) {
	scale := strings.ToLower(strings.TrimSpace(row.ScoreScale))
	var percent float64
	switch scale {
	case "probability":
		if row.Score < 0 || row.Score > 1 {
			return 0, fmt.Errorf("external probability %.4f outside [0,1]", row.Score)
		}
		percent = row.Score * 100
	case "percent":
		if row.Score < 0 || row.Score > 100 {
			return 0, fmt.Errorf("external percent %.4f outside [0,100]", row.Score)
		}
		percent = row.Score
	case "":
		if row.ScorePercent != nil {
			percent = *row.ScorePercent
			legacyFromScore := row.Score
			if legacyFromScore >= 0 && legacyFromScore <= 1 {
				legacyFromScore *= 100
			}
			if delta := legacyFromScore - percent; delta < -0.0001 || delta > 0.0001 {
				return 0, fmt.Errorf("legacy external score and score_percent disagree: %.4f vs %.4f", legacyFromScore, percent)
			}
		} else {
			// Historical protocol: values in [0,1] were probabilities.
			percent = row.Score
			if percent >= 0 && percent <= 1 {
				percent *= 100
			}
		}
	default:
		return 0, fmt.Errorf("unsupported external score_scale %q", row.ScoreScale)
	}
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("normalized external percent %.4f outside [0,100]", percent)
	}
	if row.ScorePercent != nil && (scale == "probability" || scale == "percent") {
		if delta := percent - *row.ScorePercent; delta < -0.0001 || delta > 0.0001 {
			return 0, fmt.Errorf("external score and score_percent disagree: %.4f vs %.4f", percent, *row.ScorePercent)
		}
	}
	return percent, nil
}

type registeredExternalDetectionEvent struct {
	row RegisteredExternalDetection
	seq int
}

// LatestRegisteredExternalDetections returns the current event for every
// detector/mode identity bound to one exact chapter body. A later result only
// supersedes an earlier result from the same identity; a low score from a
// different platform or mode must never hide a still-blocking result.
// SHA-less and stale-SHA historical rows are never promoted through mtime
// inference. Results are ordered by their last append position.
func LatestRegisteredExternalDetections(root string, chapter int, bodySHA256 string) ([]RegisteredExternalDetection, error) {
	bodySHA256 = strings.ToLower(strings.TrimSpace(bodySHA256))
	if chapter <= 0 || bodySHA256 == "" {
		return nil, nil
	}
	file, err := os.Open(filepath.Join(root, "meta", "external_detection_log.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	latestByIdentity := map[string]registeredExternalDetectionEvent{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	seq := 0
	for scanner.Scan() {
		seq++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			return nil, fmt.Errorf("parse external detection log line %d: %w", seq, err)
		}
		var row RegisteredExternalDetection
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse external detection log line %d: %w", seq, err)
		}
		if row.Chapter != chapter {
			continue
		}
		if strings.ToLower(strings.TrimSpace(row.BodySHA256)) != bodySHA256 {
			continue
		}
		scoreRaw, ok := fields["score"]
		if !ok {
			return nil, fmt.Errorf("external detection log line %d has no score", seq)
		}
		if strings.TrimSpace(string(scoreRaw)) == "null" {
			return nil, fmt.Errorf("external detection log line %d has null score", seq)
		}
		var scoreValue float64
		if err := json.Unmarshal(scoreRaw, &scoreValue); err != nil {
			return nil, fmt.Errorf("external detection log line %d has non-numeric score: %w", seq, err)
		}
		row.Score = scoreValue
		if scorePercentRaw, exists := fields["score_percent"]; exists {
			if strings.TrimSpace(string(scorePercentRaw)) == "null" {
				return nil, fmt.Errorf("external detection log line %d has null score_percent", seq)
			}
			var scorePercentValue float64
			if err := json.Unmarshal(scorePercentRaw, &scorePercentValue); err != nil {
				return nil, fmt.Errorf("external detection log line %d has non-numeric score_percent: %w", seq, err)
			}
			row.ScorePercent = &scorePercentValue
		}
		if strings.TrimSpace(row.Detector) == "" || strings.TrimSpace(row.Mode) == "" {
			return nil, fmt.Errorf("external detection log line %d has empty detector/mode", seq)
		}
		if !validRegisteredExternalSHA(row.BodySHA256) {
			return nil, fmt.Errorf("external detection log line %d has invalid body_sha256", seq)
		}
		percent, normalizeErr := NormalizeRegisteredExternalScore(row)
		if normalizeErr != nil {
			return nil, fmt.Errorf("external detection log line %d has invalid score: %w", seq, normalizeErr)
		}
		switch verdict := strings.ToLower(strings.TrimSpace(row.Verdict)); verdict {
		case "human_like":
			if percent >= aigc.PassExclusivePercent {
				return nil, fmt.Errorf("external detection log line %d has human_like verdict at %.4f%%", seq, percent)
			}
		case "ai_like":
			if percent < aigc.PassExclusivePercent {
				return nil, fmt.Errorf("external detection log line %d has ai_like verdict below %.0f%%", seq, aigc.PassExclusivePercent)
			}
		case "mixed":
		default:
			return nil, fmt.Errorf("external detection log line %d has invalid verdict %q", seq, row.Verdict)
		}
		row.NormalizedScorePercent = percent
		identity := strings.ToLower(strings.TrimSpace(row.Detector)) + "\x00" + strings.ToLower(strings.TrimSpace(row.Mode))
		latestByIdentity[identity] = registeredExternalDetectionEvent{row: row, seq: seq}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	events := make([]registeredExternalDetectionEvent, 0, len(latestByIdentity))
	for _, event := range latestByIdentity {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].seq < events[j].seq })
	rows := make([]RegisteredExternalDetection, 0, len(events))
	for _, event := range events {
		rows = append(rows, event.row)
	}
	return rows, nil
}

func validRegisteredExternalSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// LatestRegisteredExternalDetection returns the latest current identity that
// matches the optional detector/mode filters. Callers implementing a hard gate
// should normally inspect LatestRegisteredExternalDetections instead of using
// an unfiltered call, because every identity can independently block.
func LatestRegisteredExternalDetection(root string, chapter int, bodySHA256, detector, mode string) (*RegisteredExternalDetection, error) {
	rows, err := LatestRegisteredExternalDetections(root, chapter, bodySHA256)
	if err != nil {
		return nil, err
	}
	wantedDetector := strings.TrimSpace(detector)
	wantedMode := strings.TrimSpace(mode)
	var latest *RegisteredExternalDetection
	for i := range rows {
		row := rows[i]
		if wantedDetector != "" && !strings.EqualFold(strings.TrimSpace(row.Detector), wantedDetector) {
			continue
		}
		if wantedMode != "" && !strings.EqualFold(strings.TrimSpace(row.Mode), wantedMode) {
			continue
		}
		copy := row
		latest = &copy
	}
	return latest, nil
}

// RegisteredExternalDetectionDigest is a stable semantic identity suitable
// for checkpoints and freshness comparisons.
func RegisteredExternalDetectionDigest(row RegisteredExternalDetection) string {
	payload := struct {
		Chapter      int
		Detector     string
		Mode         string
		ScorePercent float64
		Verdict      string
		BodySHA256   string
		PayloadPath  string
		EvidencePath string
		EvidenceSHA  string
		CheckedAt    string
	}{
		Chapter: row.Chapter, Detector: strings.TrimSpace(row.Detector), Mode: strings.TrimSpace(row.Mode),
		ScorePercent: row.NormalizedScorePercent, Verdict: strings.TrimSpace(row.Verdict),
		BodySHA256: strings.ToLower(strings.TrimSpace(row.BodySHA256)), PayloadPath: strings.TrimSpace(row.PayloadPath),
		EvidencePath: strings.TrimSpace(row.EvidencePath), EvidenceSHA: strings.TrimSpace(row.EvidenceSHA256), CheckedAt: strings.TrimSpace(row.CheckedAt),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

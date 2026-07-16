package reviewreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRegisteredExternalScoreRequiresConsistentExplicitScale(t *testing.T) {
	percent86 := 86.0
	for _, tc := range []struct {
		name string
		row  RegisteredExternalDetection
		want float64
	}{
		{name: "probability", row: RegisteredExternalDetection{Score: 0.86, ScoreScale: "probability", ScorePercent: &percent86}, want: 86},
		{name: "percent", row: RegisteredExternalDetection{Score: 86, ScoreScale: "percent", ScorePercent: &percent86}, want: 86},
		{name: "legacy probability", row: RegisteredExternalDetection{Score: 0.83}, want: 83},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeRegisteredExternalScore(tc.row)
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeRegisteredExternalScore() = %.4f, %v; want %.4f", got, err, tc.want)
			}
		})
	}
	if _, err := NormalizeRegisteredExternalScore(RegisteredExternalDetection{Score: 86, ScoreScale: "probability"}); err == nil {
		t.Fatal("out-of-range probability was accepted")
	}
	wrong := 8.6
	if _, err := NormalizeRegisteredExternalScore(RegisteredExternalDetection{Score: 0.86, ScoreScale: "probability", ScorePercent: &wrong}); err == nil {
		t.Fatal("inconsistent score_percent was accepted")
	}
}

func TestLatestRegisteredExternalDetectionRequiresExactBodySHA(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, "meta")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	currentSHA := strings.Repeat("c", 64)
	log := fmt.Sprintf(""+
		`{"chapter":1,"detector":"zhuque","mode":"whole","score":0.99,"verdict":"ai_like","body_sha256":"","checked_at":"2026-07-15T10:00:00+08:00"}`+"\n"+
		`{"chapter":1,"detector":"zhuque","mode":"whole","score":0.78,"verdict":"ai_like","body_sha256":"old-sha","checked_at":"2026-07-15T10:01:00+08:00"}`+"\n"+
		`{"chapter":1,"detector":"other","mode":"whole","score":0.91,"verdict":"ai_like","body_sha256":"%s","checked_at":"2026-07-15T10:02:00+08:00"}`+"\n"+
		`{"chapter":1,"detector":"zhuque","mode":"whole","score":0.86,"score_scale":"probability","score_percent":86,"verdict":"ai_like","body_sha256":"%s","checked_at":"2026-07-15T10:03:00+08:00"}`+"\n", currentSHA, currentSHA)
	if err := os.WriteFile(filepath.Join(meta, "external_detection_log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	row, err := LatestRegisteredExternalDetection(dir, 1, currentSHA, "zhuque", "whole")
	if err != nil || row == nil {
		t.Fatalf("current exact detection missing: row=%+v err=%v", row, err)
	}
	if row.NormalizedScorePercent != 86 || row.Detector != "zhuque" {
		t.Fatalf("unexpected current detection: %+v", row)
	}
	if stale, err := LatestRegisteredExternalDetection(dir, 1, "different-sha", "zhuque", "whole"); err != nil || stale != nil {
		t.Fatalf("stale/SHA-less row leaked into current body: row=%+v err=%v", stale, err)
	}
}

func TestLatestRegisteredExternalDetectionsSupersedeOnlySameIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	percent86, percent3, percent2 := 86.0, 3.0, 2.0
	sameSHA := strings.Repeat("a", 64)
	rows := []RegisteredExternalDetection{
		{Chapter: 1, Detector: "zhuque", Mode: "whole", Score: 86, ScoreScale: "percent", ScorePercent: &percent86, Verdict: "ai_like", BodySHA256: sameSHA},
		{Chapter: 1, Detector: "other", Mode: "paragraph", Score: 3, ScoreScale: "percent", ScorePercent: &percent3, Verdict: "human_like", BodySHA256: sameSHA},
		{Chapter: 1, Detector: "ZHUQUE", Mode: "WHOLE", Score: 2, ScoreScale: "percent", ScorePercent: &percent2, Verdict: "human_like", BodySHA256: sameSHA},
	}
	file, err := os.Create(filepath.Join(dir, "meta", "external_detection_log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		raw, _ := json.Marshal(row)
		if _, err := fmt.Fprintln(file, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	latest, err := LatestRegisteredExternalDetections(dir, 1, sameSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest identities = %+v", latest)
	}
	if latest[0].Detector != "other" || latest[0].NormalizedScorePercent != 3 {
		t.Fatalf("different low identity should remain independently current: %+v", latest)
	}
	if !strings.EqualFold(latest[1].Detector, "zhuque") || latest[1].NormalizedScorePercent != 2 {
		t.Fatalf("same identity pass did not supersede its high result: %+v", latest)
	}
}

func TestLatestRegisteredExternalDetectionsRejectsMissingScore(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("b", 64)
	log := fmt.Sprintf(
		"{\"chapter\":1,\"detector\":\"zhuque\",\"mode\":\"whole\",\"score\":86,\"score_scale\":\"percent\",\"verdict\":\"ai_like\",\"body_sha256\":%q}\n"+
			"{\"chapter\":1,\"detector\":\"zhuque\",\"mode\":\"whole\",\"verdict\":\"human_like\",\"body_sha256\":%q}\n", sha, sha,
	)
	if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LatestRegisteredExternalDetections(dir, 1, sha); err == nil {
		t.Fatal("missing score silently superseded the blocking identity")
	}
}

func TestLatestRegisteredExternalDetectionsRejectsNullScore(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("c", 64)
	log := fmt.Sprintf(
		`{"chapter":1,"detector":"zhuque","mode":"whole","score":null,"score_scale":"percent","verdict":"ai_like","body_sha256":%q}`+"\n",
		sha,
	)
	if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LatestRegisteredExternalDetections(dir, 1, sha); err == nil || !strings.Contains(err.Error(), "null score") {
		t.Fatalf("null score did not fail closed: %v", err)
	}
}

func TestLatestRegisteredExternalDetectionsRejectsConflictingLegacyScorePercent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("d", 64)
	log := fmt.Sprintf(
		`{"chapter":1,"detector":"zhuque","mode":"whole","score":0.86,"score_percent":3,"verdict":"human_like","body_sha256":%q}`+"\n",
		sha,
	)
	if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LatestRegisteredExternalDetections(dir, 1, sha); err == nil || !strings.Contains(err.Error(), "score and score_percent disagree") {
		t.Fatalf("conflicting legacy score fields did not fail closed: %v", err)
	}
}

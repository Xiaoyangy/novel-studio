package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestStoryTimeContractRoundtripAndScheduleTamperDetection(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	contract := domain.StoryTimeContract{
		Source:                domain.StoryTimeSourceOutlineAll,
		TargetChapters:        4,
		DurationDaysMin:       7,
		DurationDaysMax:       9,
		NominalDaysPerChapter: 2,
		ArcSchedule: []domain.StoryTimeArcSchedule{
			{Volume: 1, Arc: 1, StartChapter: 1, EndChapter: 4, StartDay: 0, EndDay: 8},
		},
	}
	if err := st.WorldSim.SaveStoryTimeContract(contract); err != nil {
		t.Fatalf("SaveStoryTimeContract: %v", err)
	}
	loaded, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil {
		t.Fatalf("LoadStoryTimeContract: %v", err)
	}
	if loaded == nil || loaded.CoreDigest == "" || loaded.ScheduleDigest == "" ||
		loaded.TargetChapters != 4 {
		t.Fatalf("roundtrip = %+v", loaded)
	}

	loaded.ArcSchedule[0].EndDay = 7.5
	raw, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, storyTimeContractPath), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorldSim.LoadStoryTimeContract(); err == nil || !strings.Contains(err.Error(), "schedule_digest mismatch") {
		t.Fatalf("tampered schedule should be rejected, got %v", err)
	}
}

func TestLoadStoryTimeContractMissingIsNil(t *testing.T) {
	contract, err := NewStore(t.TempDir()).WorldSim.LoadStoryTimeContract()
	if err != nil || contract != nil {
		t.Fatalf("missing contract = %+v err=%v", contract, err)
	}
}

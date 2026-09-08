package domain

import (
	"math"
	"testing"
)

func TestStoryTimeContractPreservesSubdayDurations(t *testing.T) {
	for _, tc := range []struct {
		hint     string
		min, max float64
	}{
		{"90分钟", 90.0 / StoryMinutesPerDay, 90.0 / StoryMinutesPerDay},
		{"九十分钟", 90.0 / StoryMinutesPerDay, 90.0 / StoryMinutesPerDay},
		{"1.5小时", 1.5 / StoryHoursPerDay, 1.5 / StoryHoursPerDay},
		{"1-2 hours", 1.0 / StoryHoursPerDay, 2.0 / StoryHoursPerDay},
		{"45-90 minutes", 45.0 / StoryMinutesPerDay, 90.0 / StoryMinutesPerDay},
		{"三十到九十分钟", 30.0 / StoryMinutesPerDay, 90.0 / StoryMinutesPerDay},
		{"5400秒", 5400.0 / StorySecondsPerDay, 5400.0 / StorySecondsPerDay},
		{"30-60 seconds", 30.0 / StorySecondsPerDay, 60.0 / StorySecondsPerDay},
	} {
		t.Run(tc.hint, func(t *testing.T) {
			contract, err := DeriveStoryTimeContract("1-1卷、3-3章；全书故事跨度"+tc.hint, 3)
			if err != nil {
				t.Fatal(err)
			}
			if contract.Source == StoryTimeSourceFallbackNominal || math.Abs(contract.DurationDaysMin-tc.min) > 1e-12 || math.Abs(contract.DurationDaysMax-tc.max) > 1e-12 || math.Abs(contract.NominalDaysPerChapter-(tc.min+tc.max)/6) > 1e-12 {
				t.Fatalf("subday contract became nominal days: %+v", contract)
			}
			if err := contract.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOutlineScaleTargetRetainsSubdayStoryHint(t *testing.T) {
	for _, hint := range []string{"90分钟", "九十分钟", "1.5小时", "45-90 minutes", "5400 seconds"} {
		target, err := ResolveBookScaleTarget("1-1卷，3-3章；主线时间跨度"+hint, 0, 0)
		if err != nil || target.StoryTimeHint != hint {
			t.Fatalf("outline target lost %q: %+v %v", hint, target, err)
		}
	}
}

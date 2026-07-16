package domain

import (
	"reflect"
	"testing"
)

func TestFlattenOutlineReservesSkeletonArcChapterNumbers(t *testing.T) {
	volumes := []VolumeOutline{{
		Index: 1,
		Arcs: []ArcOutline{
			{
				Index: 1,
				Chapters: []OutlineEntry{
					{Title: "前一"},
					{Title: "前二"},
				},
			},
			{Index: 2, EstimatedChapters: 3},
			{
				Index: 3,
				Chapters: []OutlineEntry{
					{Title: "后一"},
					{Title: "后二"},
				},
			},
		},
	}}

	flat := FlattenOutline(volumes)
	if got, want := TotalChapters(volumes), 7; got != want {
		t.Fatalf("TotalChapters() = %d, want %d", got, want)
	}

	var chapters []int
	for _, entry := range flat {
		chapters = append(chapters, entry.Chapter)
	}
	if want := []int{1, 2, 6, 7}; !reflect.DeepEqual(chapters, want) {
		t.Fatalf("FlattenOutline chapter numbers = %v, want %v", chapters, want)
	}
}

package domain

import "testing"

func TestStoryCalendar(t *testing.T) {
	if !(StoryCalendar{}).IsEmpty() {
		t.Fatal("零值日历应为 empty")
	}
	c := StoryCalendar{Era: "架空王朝", StartDate: "天启三年三月初七", DaysPerChapter: 2}
	if c.IsEmpty() {
		t.Fatal("有内容日历不应为 empty")
	}
	if got := c.EstimateElapsedDays(10); got != 20 {
		t.Fatalf("10 章 ×2 天应为 20，实际 %v", got)
	}
	if got := (StoryCalendar{Era: "现代"}).EstimateElapsedDays(5); got != 5 {
		t.Fatalf("密度未配置应按 1 天/章兜底，实际 %v", got)
	}
	if (StoryCalendar{}).EstimateElapsedDays(0) != 0 {
		t.Fatal("0 章应为 0 天")
	}
}

func TestFactionClock(t *testing.T) {
	var nilClock *FactionClock
	if nilClock.Tick(2) || nilClock.IsComplete() {
		t.Fatal("nil 钟应安全 noop")
	}
	c := &FactionClock{Segments: 6, Progress: 3, Consequence: "盐帮完成火器走私"}
	if c.Tick(2) {
		t.Fatal("5/6 不应走满")
	}
	if !c.Tick(3) {
		t.Fatal("拨满应返回 true")
	}
	if c.Progress != 6 {
		t.Fatalf("进度应封顶 6，实际 %d", c.Progress)
	}
	if !c.IsComplete() {
		t.Fatal("走满后 IsComplete 应为 true")
	}
	c.Tick(-10)
	if c.Progress != 0 {
		t.Fatalf("负拨应钳制到 0，实际 %d", c.Progress)
	}
}

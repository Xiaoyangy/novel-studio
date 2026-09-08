package domain

// StoryTimeRenderContract is the prose-safe elapsed-time obligation. It carries
// no hidden character decisions or world rules, only the already adjudicated
// chapter window and the evidence the prose must make naturally observable.
type StoryTimeRenderContract struct {
	Chapter        int     `json:"chapter"`
	StartSeconds   float64 `json:"start_seconds"`
	EndSeconds     float64 `json:"end_seconds"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Guidance       string  `json:"guidance"`
}

func storyTimeRenderContract(storyTime *StoryTimeChapterSchedule) *StoryTimeRenderContract {
	if storyTime == nil {
		return nil
	}
	return &StoryTimeRenderContract{
		Chapter: storyTime.Chapter, StartSeconds: storyTime.StartDay * 86400,
		EndSeconds: storyTime.EndDay * 86400, ElapsedSeconds: (storyTime.EndDay - storyTime.StartDay) * 86400,
		Guidance: "本章从已确认的开始时点推进上述实际秒数。请在开场与收束处自然呈现可核对的时间：可用现实钟表两端读数、同一个具名公开截止点的剩余时间两端，或以正文开场动作和收束动作明确界定的整段持续时间。读数应能核对到本章所需精度；如有明确跨日也须交代。只需自然交代整段时间，不必每个动作报时；并行动作的时长不能相加。模糊的‘过了一会儿’、预期耗时、计划、回忆或旧影像不能代替本章已经过去的时间。不得把字段名或计算过程写进小说。",
	}
}

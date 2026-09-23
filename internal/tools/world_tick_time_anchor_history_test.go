package tools

import (
	"reflect"
	"testing"
)

func TestWorldTickTimeAnchorsDistinguishHistoricalDurations(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"当自己的追问开始偏向七年前时，她选择先陪陈砚联系已有警务接待入口。", nil},
		{"沈知微回忆七年之前的事故。", nil},
		{"过去三日没有收到消息。", nil},
		{"此前四十八小时的记录仍有缺口。", nil},
		{"她必须查清七年前的事故。", nil},
		{"七年前的事故仍未解释，三日内必须提交说明。", []string{"三日"}},
		{"给四十八小时保护，次日上午前必须完成书面披露。", []string{"四十八小时", "次日上午"}},
		{"必须在四十八小时之前提交。", []string{"四十八小时"}},
		{"请在四十八小时之前把材料交给警方。", []string{"四十八小时"}},
		{"于四十八小时之前把材料交给警方。", []string{"四十八小时"}},
		{"四十八小时之前，必须完成书面披露。", []string{"四十八小时"}},
		{"在七年前的港口，她第一次见到那人。", nil},
		{"三日前必须提交申请。", []string{"三日"}},
		{"七年前留下记录；七年后才能启封。", []string{"七年"}},
		{"预计七年完成调查。", []string{"七年"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := worldTickExtractChapterOneTimeAnchors(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}

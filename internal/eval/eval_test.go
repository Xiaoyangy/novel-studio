package eval

import (
	"path/filepath"
	"testing"
)

func TestRunDir(t *testing.T) {
	out := "/tmp/eval"
	cases := []struct {
		arm    string
		repeat int
		total  int
		want   string
	}{
		{ArmSingle, 1, 1, filepath.Join(out, "artifacts", "case1")},
		{ArmBaseline, 1, 1, filepath.Join(out, "artifacts", "case1", "baseline")},
		{ArmVariant, 2, 3, filepath.Join(out, "artifacts", "case1", "r2", "variant")},
		{ArmSingle, 2, 3, filepath.Join(out, "artifacts", "case1", "r2")},
	}
	for _, tc := range cases {
		if got := runDir(out, "case1", tc.arm, tc.repeat, tc.total); got != tc.want {
			t.Fatalf("runDir(%s,%d,%d) = %s want %s", tc.arm, tc.repeat, tc.total, got, tc.want)
		}
	}
}

func TestCommandRejectsInvalidRepeat(t *testing.T) {
	code := Command([]string{"--cases", "evals/cases/smoke", "--repeat", "0"})
	if code != 2 {
		t.Fatalf("repeat=0 应返回用法错误 2，得到 %d", code)
	}
}

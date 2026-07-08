package rules

import "testing"

func TestLimitValueFormatting(t *testing.T) {
	cases := []struct {
		name    string
		limit   any
		present bool
		text    string
	}{
		{name: "nil", present: false},
		{name: "empty string", limit: "", present: false},
		{name: "zero int", limit: 0, present: true, text: "0"},
		{name: "range", limit: "2800-3400", present: true, text: "2800-3400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasLimitValue(tc.limit); got != tc.present {
				t.Fatalf("HasLimitValue() = %v, want %v", got, tc.present)
			}
			if got := FormatLimitValue(tc.limit); got != tc.text {
				t.Fatalf("FormatLimitValue() = %q, want %q", got, tc.text)
			}
		})
	}
}

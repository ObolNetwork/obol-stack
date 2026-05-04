package helmcmd

import "testing"

func TestParseMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"v4.1.3+gc94d381", 4, true},
		{"v3.20.1+g4d04eef", 3, true},
		{"v3.20.1\n", 3, true},
		{"v10.0.0", 10, true},
		{"helm v4.0.0", 0, false},
		{"", 0, false},
		{"v.1.2", 0, false},
	}
	for _, tc := range cases {
		got, err := parseMajor(tc.in)
		if tc.ok && err != nil {
			t.Errorf("parseMajor(%q) errored: %v", tc.in, err)
			continue
		}
		if !tc.ok && err == nil {
			t.Errorf("parseMajor(%q) = %d, want error", tc.in, got)
			continue
		}
		if got != tc.want {
			t.Errorf("parseMajor(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

package connector

import "testing"

func TestMentionRegex(t *testing.T) {
	cases := []struct {
		in        string
		full      string // expected @!nodeid capture, or ""
		short     string // expected four-hex capture, or ""
		wantMatch bool
	}{
		{"Mention from app @!7d753ae3", "7d753ae3", "", true},
		{"@!ABCDEF01 upper", "ABCDEF01", "", true},
		{"ping @3ae3 pls", "", "3ae3", true},
		{"ping 3ae3 pls", "", "3ae3", true},
		{"no id here", "", "", false},
		{"@!7d753ae3f too long", "", "", false},
	}
	for _, tc := range cases {
		m := mentionRegex.FindStringSubmatchIndex(tc.in)
		if (m != nil) != tc.wantMatch {
			t.Errorf("%q: match=%v, want %v", tc.in, m != nil, tc.wantMatch)
			continue
		}
		if m == nil {
			continue
		}
		full, short := "", ""
		if m[2] >= 0 {
			full = tc.in[m[2]:m[3]]
		}
		if m[4] >= 0 {
			short = tc.in[m[4]:m[5]]
		}
		if full != tc.full || short != tc.short {
			t.Errorf("%q: full=%q short=%q, want %q %q", tc.in, full, short, tc.full, tc.short)
		}
	}
}

package daemon

import "testing"

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		text    string
		id      string
		allow   bool
		matches bool
	}{
		{"y abcde", "abcde", true, true},
		{"yes abcde", "abcde", true, true},
		{"n abcde", "abcde", false, true},
		{"no abcde", "abcde", false, true},
		{"  Yes  ABCDE  ", "abcde", true, true}, // a phone capitalized it
		// A bare yes is one autocorrect away from approving a command the
		// user never read, and is an ordinary thing to say to Claude.
		{"yes", "", false, false},
		{"no", "", false, false},
		{"yes please go ahead", "", false, false},
		{"y abcdef", "", false, false}, // ids are five letters
		{"y abcd", "", false, false},
		{"y abcle", "", false, false}, // l is never in an id; this is a typo
		{"y 12345", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			id, allow, ok := parseVerdict(tc.text)
			if ok != tc.matches {
				t.Fatalf("parseVerdict(%q) matched = %v, want %v", tc.text, ok, tc.matches)
			}
			if ok && (id != tc.id || allow != tc.allow) {
				t.Errorf("parseVerdict(%q) = %q, %v; want %q, %v", tc.text, id, allow, tc.id, tc.allow)
			}
		})
	}
}

func TestParsePick(t *testing.T) {
	cases := []struct {
		text   string
		listed int
		index  int
		ok     bool
	}{
		{"1", 3, 0, true},
		{"3", 3, 2, true},
		{"2.", 3, 1, true},
		{"4", 3, 0, false}, // past the end of what was offered
		{"0", 3, 0, false},
		{"1", 0, 0, false}, // nothing was offered
		{"payments-api", 3, 0, false},
		{"1 also check the tests", 3, 0, false}, // an instruction, not a choice
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			index, ok := parsePick(tc.text, tc.listed)
			if ok != tc.ok || (ok && index != tc.index) {
				t.Errorf("parsePick(%q, %d) = %d, %v; want %d, %v", tc.text, tc.listed, index, ok, tc.index, tc.ok)
			}
		})
	}
}

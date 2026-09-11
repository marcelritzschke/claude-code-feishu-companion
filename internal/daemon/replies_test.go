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

// interrupt is a command only when it is the whole message: "interrupt the
// build if it hangs" is an instruction for Claude.
func TestParseInterrupt(t *testing.T) {
	for _, text := range []string{"interrupt", "/interrupt", " Interrupt ", "INTERRUPT"} {
		if !parseInterrupt(text) {
			t.Errorf("parseInterrupt(%q) = false, want a command", text)
		}
	}
	for _, text := range []string{
		"interrupt the build if it hangs",
		"can you interrupt",
		"interrupt 2", // numbers named sessions in a list that no longer exists
	} {
		if parseInterrupt(text) {
			t.Errorf("parseInterrupt(%q) = true, want it left for Claude", text)
		}
	}
}

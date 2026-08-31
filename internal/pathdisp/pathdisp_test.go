package pathdisp

import "testing"

func TestBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/u/repo/auth/session.go", "session.go"},
		{"/home/u/repo", "repo"},
		{"session.go", "session.go"},
		{"", ""},
		{"/", "/"},
		{"/home/u/repo/", "repo"},
		// Windows paths must not read as one long name.
		{`C:\work\payments-api\auth\session.go`, "session.go"},
		{`C:\work\payments-api`, "payments-api"},
		{`C:\work\payments-api\`, "payments-api"},
	}
	for _, tc := range cases {
		if got := Base(tc.in); got != tc.want {
			t.Errorf("Base(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShort(t *testing.T) {
	cases := []struct{ path, dir, want string }{
		{"/home/u/repo/auth/session.go", "/home/u/repo", "auth/session.go"},
		{"/elsewhere/session.go", "/home/u/repo", "session.go"},
		{"/home/u/repo-other/x.go", "/home/u/repo", "x.go"}, // prefix but not a child
		{"/home/u/repo/x.go", "", "x.go"},
		{"/home/u/repo/x.go", "/home/u/repo/", "x.go"}, // trailing separator on dir
		{`C:\work\api\auth\session.go`, `C:\work\api`, "auth/session.go"},
		{`C:\other\session.go`, `C:\work\api`, "session.go"},
	}
	for _, tc := range cases {
		if got := Short(tc.path, tc.dir); got != tc.want {
			t.Errorf("Short(%q, %q) = %q, want %q", tc.path, tc.dir, got, tc.want)
		}
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"/home/u/payments-api", "payments-api", true},
		{`C:\work\payments-api`, "payments-api", true},
		{"payments-api", "payments-api", true},
		{"/", "", false},
		{".", "", false},
		{"", "", false},
		{`C:\`, "", false}, // a volume root has no project name
	}
	for _, tc := range cases {
		got, ok := Label(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("Label(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

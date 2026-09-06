package channel

import "testing"

// A channel must only believe a CLAUDE_PID that belongs to its own
// session. Every process a Claude Code session starts inherits the
// variable, so a session started from inside another one would otherwise
// be judged by that other session's command line - and judged wrong.
func TestSessionPID(t *testing.T) {
	cases := []struct {
		name   string
		pid    string
		socket string
		want   int
	}{
		{
			name:   "the session's own pid",
			pid:    "4242",
			socket: "/run/user/1000/cc-socks/4242.sock",
			want:   4242,
		},
		{
			name:   "inherited from another session",
			pid:    "4242",
			socket: "/run/user/1000/cc-socks/9999.sock",
			want:   0,
		},
		{
			name:   "nothing to check it against",
			pid:    "4242",
			socket: "",
			want:   0,
		},
		{
			name:   "no pid at all",
			pid:    "",
			socket: "/run/user/1000/cc-socks/4242.sock",
			want:   0,
		},
		{
			name:   "a socket path of another shape",
			pid:    "4242",
			socket: "/tmp/claude.sock",
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_PID", tc.pid)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", tc.socket)
			if got := sessionPID(); got != tc.want {
				t.Errorf("sessionPID() = %d, want %d", got, tc.want)
			}
		})
	}
}

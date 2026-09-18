package channel

import "testing"

// A channel is started by its session, so its parent is the session - but
// only the messaging socket, which Claude Code names after the session's
// own process, can confirm that the parent is Claude Code and not a
// wrapper in between. CLAUDE_PID plays no part: Claude Code does not hand
// it to MCP servers, and where a channel does see it, it was inherited from
// some other session.
func TestSessionPID(t *testing.T) {
	cases := []struct {
		name      string
		parent    int
		claudePID string
		socket    string
		want      int
	}{
		{
			name:   "started by its session, as Claude Code starts a channel",
			parent: 4242,
			socket: "/run/user/1000/cc-socks/4242.sock",
			want:   4242,
		},
		{
			name:      "a CLAUDE_PID inherited from an outer session is ignored",
			parent:    4242,
			claudePID: "9999",
			socket:    "/run/user/1000/cc-socks/4242.sock",
			want:      4242,
		},
		{
			name:   "started through a wrapper",
			parent: 777,
			socket: "/run/user/1000/cc-socks/4242.sock",
			want:   0,
		},
		{
			name:   "nothing to check the parent against",
			parent: 4242,
			socket: "",
			want:   0,
		},
		{
			name:   "a socket path of another shape",
			parent: 4242,
			socket: "/tmp/claude.sock",
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_PID", tc.claudePID)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", tc.socket)
			if got := sessionPID(tc.parent); got != tc.want {
				t.Errorf("sessionPID(%d) = %d, want %d", tc.parent, got, tc.want)
			}
		})
	}
}

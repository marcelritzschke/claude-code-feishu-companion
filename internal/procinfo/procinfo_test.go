package procinfo

import (
	"os"
	"strings"
	"testing"
)

func TestClassifyArgv(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{
			name: "development flag naming claude-companion",
			argv: []string{"claude", "--dangerously-load-development-channels", "server:claude-companion"},
			want: true,
		},
		{
			name: "channels flag naming claude-companion",
			argv: []string{"claude", "--channels", "server:claude-companion"},
			want: true,
		},
		{
			name: "claude-companion among several entries",
			argv: []string{"claude", "--channels", "plugin:telegram@claude-plugins-official", "server:claude-companion"},
			want: true,
		},
		{
			name: "equals spelling",
			argv: []string{"claude", "--channels=server:claude-companion"},
			want: true,
		},
		{
			name: "bare name is tolerated",
			argv: []string{"claude", "--channels", "claude-companion"},
			want: true,
		},
		{
			name: "claude-companion as a plugin entry",
			argv: []string{"claude", "--channels", "plugin:claude-companion@acme"},
			want: true,
		},
		{
			name: "no channels flag at all",
			argv: []string{"claude"},
			want: false,
		},
		{
			name: "channels flag naming someone else",
			argv: []string{"claude", "--channels", "server:webhook"},
			want: false,
		},
		{
			name: "claude-companion is the prompt, not a channel entry",
			argv: []string{"claude", "--model", "opus", "claude-companion"},
			want: false,
		},
		{
			name: "entry list ends at the next flag",
			argv: []string{"claude", "--channels", "server:webhook", "--agent", "claude-companion"},
			want: false,
		},
		{
			name: "equals spelling does not open the list for later args",
			argv: []string{"claude", "--channels=server:webhook", "server:claude-companion"},
			want: false,
		},
		{
			name: "a similarly named server is not claude-companion",
			argv: []string{"claude", "--channels", "server:claude-companion-dev"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyArgv(tc.argv, "claude-companion"); got != tc.want {
				t.Errorf("ClassifyArgv(%q) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}

// A process that is gone (or a platform that cannot be asked) must read as
// unconfirmed, never as a session that is definitely unreachable.
func TestEnabledUnknownForMissingProcess(t *testing.T) {
	enabled, known := Enabled(-1, "claude-companion")
	if enabled {
		t.Error("a process that cannot be read must not report as channel-enabled")
	}
	if known {
		t.Error("a process that cannot be read must report its readiness as unknown")
	}
}

// The self-check keeps the platform reader honest: whatever this test
// binary was started as, its own argv must come back.
func TestCommandLineReadsOwnProcess(t *testing.T) {
	argv, err := commandLine(os.Getpid())
	if err != nil {
		t.Skipf("this platform cannot read a process command line: %v", err)
	}
	if len(argv) == 0 {
		t.Fatal("commandLine returned no arguments for this process")
	}
	if !strings.Contains(argv[0], "procinfo") {
		t.Errorf("commandLine()[0] = %q, want the test binary's path", argv[0])
	}
}

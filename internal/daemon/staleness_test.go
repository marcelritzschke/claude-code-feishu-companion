package daemon

import (
	"testing"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/buildid"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// An install stops the daemon before replacing the file, and a hook firing
// in that window starts a fresh one from the file that is about to go. No
// hook or channel afterwards has any reason to doubt the daemon answering
// it, so the daemon is the only thing that can notice - and if it does not,
// the user gets an install that changed the binary, the version command and
// nothing else.
func TestDaemonRetiresWhenItsProgramIsReplaced(t *testing.T) {
	d, _, _ := fixture(t, session.Ready)
	d.replaced = func() bool { return true }

	d.retireIfReplaced()

	select {
	case <-d.stop:
	default:
		t.Error("the daemon kept serving code that is no longer installed")
	}
}

// The daemon serves every session on the machine. It gives that up only on
// a certain answer, never on a stat that happened to fail.
func TestDaemonStaysWhileItIsStillTheInstalledProgram(t *testing.T) {
	d, _, _ := fixture(t, session.Ready)
	d.replaced = func() bool { return false }

	d.retireIfReplaced()

	select {
	case <-d.stop:
		t.Error("the daemon retired without being replaced")
	default:
	}
}

// olderBuild is what a caller outside the daemon decides on, and it has the
// same rule: two answers, both certain, and different.
func TestOlderBuildNeedsTwoCertainAnswers(t *testing.T) {
	mine := buildid.Stamp()
	if mine == "" {
		t.Skip("this process cannot identify its own image")
	}
	cases := map[string]struct {
		st   ipc.Status
		want bool
	}{
		"a different build":          {ipc.Status{Build: mine + "-and-then-some"}, true},
		"the same build":             {ipc.Status{Build: mine}, false},
		"a daemon that does not say": {ipc.Status{}, false},
	}
	for name, c := range cases {
		if got := olderBuild(c.st); got != c.want {
			t.Errorf("%s: olderBuild = %v, want %v", name, got, c.want)
		}
	}
}

// The daemon answers what it is running, so that setup - and anyone asking
// after an install - can tell a daemon from the right daemon.
func TestStatusCarriesTheBuild(t *testing.T) {
	private(t)
	d, _, _ := fixture(t, session.Ready)
	go func() { _ = d.Serve(t.Context()) }()
	t.Cleanup(d.halt)

	deadline := time.Now().Add(2 * time.Second)
	for !ipc.Ping(200*time.Millisecond) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st, ok := status()
	if !ok {
		t.Fatal("the daemon did not answer a status request")
	}
	if st.Build != buildid.Stamp() {
		t.Errorf("status build = %q, want this process's own %q", st.Build, buildid.Stamp())
	}
}

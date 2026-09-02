package session

import (
	"os"
	"testing"
	"time"
)

// ownPID is a process that is certainly alive: this one.
func ownPID() int { return os.Getpid() }

// fakeChannel stands in for a channel's live connection.
type fakeChannel struct {
	injected []string
	verdicts []string
}

func (f *fakeChannel) Inject(content string, _ map[string]string) error {
	f.injected = append(f.injected, content)
	return nil
}

func (f *fakeChannel) Verdict(requestID, behavior string) error {
	f.verdicts = append(f.verdicts, requestID+":"+behavior)
	return nil
}

func TestStateFollowsHookEvents(t *testing.T) {
	r := NewRegistry()
	steps := []struct {
		event string
		want  State
	}{
		{"SessionStart", Idle},
		{"UserPromptSubmit", Working},
		{"PostToolUse", Working},
		{"PermissionRequest", Waiting},
		{"PostToolUse", Working},
		{"Stop", Idle},
	}
	for _, step := range steps {
		s := r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/payments-api", HookEvent: step.event})
		if s.State != step.want {
			t.Errorf("after %s state = %q, want %q", step.event, s.State, step.want)
		}
	}
}

// An event that says nothing about what the session is doing must not
// disturb the state a previous event established.
func TestUnknownEventLeavesStateAlone(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/api", HookEvent: "PermissionRequest"})
	s := r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/api", HookEvent: "SomethingNewClaudeCodeAdded"})
	if s.State != Waiting {
		t.Errorf("state = %q, want it left at %q", s.State, Waiting)
	}
}

// /clear gives the session a new id. It is still the same terminal, the
// same work, and the same thing the user selected - so it must stay one
// session, and stay selected.
func TestClearedSessionStaysOneSession(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-old", PID: 100, Dir: "/work/payments-api", Title: "Fix token refresh", HookEvent: "SessionStart"})
	if _, ok := r.Select("sess-old"); !ok {
		t.Fatal("could not select the session")
	}

	r.Observe(Observation{ID: "sess-new", PID: 100, Dir: "/work/payments-api", HookEvent: "UserPromptSubmit"})

	if got := len(r.List()); got != 1 {
		t.Fatalf("registry holds %d sessions, want the cleared one to have replaced the old", got)
	}
	sel, ok := r.Selected()
	if !ok {
		t.Fatal("the selection was lost across /clear")
	}
	if sel.ID != "sess-new" {
		t.Errorf("selected id = %q, want the new one", sel.ID)
	}
	if sel.Title != "Fix token refresh" {
		t.Errorf("title = %q, want the known title carried over", sel.Title)
	}
}

// The defining safety rule: a message must never reach a session the user
// did not pick. When the selection ends, there is simply no selection.
func TestSelectionIsNeverSubstituted(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/payments-api", HookEvent: "SessionStart"})
	r.Observe(Observation{ID: "sess-2", PID: 200, Dir: "/work/frontend", HookEvent: "SessionStart"})
	if _, ok := r.Select("sess-1"); !ok {
		t.Fatal("could not select sess-1")
	}

	r.Remove("sess-1")

	if s, ok := r.Selected(); ok {
		t.Errorf("Selected() = %q after its session ended, want no selection", s.ID)
	}
}

func TestSelectUnknownSessionChangesNothing(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/api", HookEvent: "SessionStart"})
	r.Select("sess-1")

	if _, ok := r.Select("sess-gone"); ok {
		t.Fatal("selecting a session that does not exist reported success")
	}
	s, ok := r.Selected()
	if !ok || s.ID != "sess-1" {
		t.Errorf("selection = %+v, want it left on sess-1", s)
	}
}

// Losing the channel means the Claude Code process is gone: the channel
// outlives every turn, so it only ends when the session does.
func TestDetachEndsTheSession(t *testing.T) {
	r := NewRegistry()
	ch := &fakeChannel{}
	r.Attach("sess-1", 100, "/work/api", Ready, ch)
	r.Select("sess-1")

	r.Detach(ch)

	if len(r.List()) != 0 {
		t.Error("the session outlived its channel")
	}
	if _, ok := r.Selected(); ok {
		t.Error("the selection outlived its session")
	}
}

// A channel that reconnected has already replaced the old one; the old
// one's disconnect must not take the new session down with it.
func TestDetachIgnoresAReplacedChannel(t *testing.T) {
	r := NewRegistry()
	old, current := &fakeChannel{}, &fakeChannel{}
	r.Attach("sess-1", 100, "/work/api", Ready, old)
	r.Attach("sess-1", 100, "/work/api", Ready, current)

	r.Detach(old)

	if len(r.List()) != 1 {
		t.Error("a stale channel's disconnect removed the live session")
	}
}

// The overview leads with whatever needs the user.
func TestListLeadsWithAttention(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "idle", PID: 1, Dir: "/work/wirelark", HookEvent: "Stop"})
	r.Observe(Observation{ID: "busy", PID: 2, Dir: "/work/payments-api", HookEvent: "UserPromptSubmit"})
	r.Observe(Observation{ID: "blocked", PID: 3, Dir: "/work/frontend", HookEvent: "PermissionRequest"})

	got := r.List()
	want := []string{"blocked", "busy", "idle"}
	if len(got) != len(want) {
		t.Fatalf("List returned %d sessions, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("List()[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestDowngradeStopsClaimingReachable(t *testing.T) {
	r := NewRegistry()
	r.Attach("sess-1", 100, "/work/api", Ready, &fakeChannel{})

	r.Downgrade("sess-1")

	s, _ := r.Get("sess-1")
	if s.Remote.Continuable() {
		t.Errorf("remote = %q, want a session that refused a message to stop being offered", s.Remote)
	}
}

// Unconfirmed must still be offered: refusing to try would strand every
// user on a platform Wirelark cannot inspect.
func TestUnconfirmedIsStillOffered(t *testing.T) {
	if !Unconfirmed.Continuable() {
		t.Error("an unconfirmed session must still be offered, then corrected honestly if it fails")
	}
	if Notifications.Continuable() {
		t.Error("a notifications-only session must never be offered")
	}
}

func TestSnapshotRestoresTheSelectedSession(t *testing.T) {
	t.Setenv("WIRELARK_STATE_DIR", t.TempDir())
	r := NewRegistry()
	r.Attach("sess-1", ownPID(), "/work/payments-api", Ready, &fakeChannel{})
	r.Observe(Observation{ID: "sess-1", PID: ownPID(), Dir: "/work/payments-api", Title: "Fix token refresh", HookEvent: "UserPromptSubmit"})
	r.Select("sess-1")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	restored := Load()
	s, ok := restored.Selected()
	if !ok {
		t.Fatal("the selection did not survive the daemon restart")
	}
	if s.Title != "Fix token refresh" {
		t.Errorf("title = %q, want it restored", s.Title)
	}
	if s.Remote.Continuable() {
		t.Error("a restored session must wait for its channel to reconnect before being offered again")
	}
}

// A session whose process is gone must not come back from the snapshot as
// something the user can talk to.
func TestSnapshotDropsDeadSessions(t *testing.T) {
	t.Setenv("WIRELARK_STATE_DIR", t.TempDir())
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-dead", PID: 0x7fffffff, Dir: "/work/api", HookEvent: "SessionStart"})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	if got := len(Load().List()); got != 0 {
		t.Errorf("Load restored %d dead sessions, want 0", got)
	}
}

func TestStaleSessionsExpire(t *testing.T) {
	r := NewRegistry()
	r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/api", HookEvent: "Stop"})
	r.age("sess-1", staleAfter+time.Minute)

	if got := len(r.List()); got != 0 {
		t.Errorf("List returned %d stale sessions, want 0", got)
	}
}

func TestDescribeUsesTitleAndProject(t *testing.T) {
	cases := []struct {
		name string
		s    Session
		want string
	}{
		{"titled", Session{Title: "Fix token refresh", Dir: "/work/payments-api"}, "Fix token refresh · payments-api"},
		{"untitled", Session{Dir: "/work/payments-api"}, "payments-api"},
		{"no directory", Session{}, "unknown project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Describe(); got != tc.want {
				t.Errorf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}

// After /clear the channel is unchanged but the session has a new id, and
// naming it by the id the channel registered under would name nothing.
func TestSessionOfFindsTheRenamedSession(t *testing.T) {
	r := NewRegistry()
	ch := &fakeChannel{}
	r.Attach("sess-old", 100, "/work/payments-api", Ready, ch)
	r.Observe(Observation{ID: "sess-new", PID: 100, Dir: "/work/payments-api", HookEvent: "UserPromptSubmit"})

	s, ok := r.SessionOf(ch)
	if !ok {
		t.Fatal("the channel's session could not be found after /clear")
	}
	if s.ID != "sess-new" {
		t.Errorf("SessionOf gave %q, want the current id", s.ID)
	}
}

// age backdates a session's last activity, so a test can reach a state
// that would otherwise take hours.
func (r *Registry) age(id string, by time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		s.LastSeen = s.LastSeen.Add(-by)
	}
}

// A session handed out for reading must not be a window into the registry's
// own state: the daemon reads sessions from several goroutines while hook
// events are changing them.
func TestReadersGetCopies(t *testing.T) {
	r := NewRegistry()
	got := r.Observe(Observation{ID: "sess-1", PID: 100, Dir: "/work/api", Title: "Fix token refresh", HookEvent: "UserPromptSubmit"})

	got.Title = "rewritten by the caller"
	got.State = Idle

	current, _ := r.Get("sess-1")
	if current.Title != "Fix token refresh" || current.State != Working {
		t.Errorf("the registry was changed through a returned session: %+v", current)
	}
}

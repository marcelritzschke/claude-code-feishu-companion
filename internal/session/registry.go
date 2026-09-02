package session

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/marcelritzschke/wirelark/internal/debuglog"
	"github.com/marcelritzschke/wirelark/internal/paths"
	"github.com/marcelritzschke/wirelark/internal/secfile"
)

// snapshotFile keeps the registry across a daemon restart, so the overview
// is not blank until the next hook fires.
const snapshotFile = "sessions.json"

// staleAfter drops a session nothing has been heard from. A session that
// ended without a SessionEnd hook - a killed terminal, a crashed daemon -
// must not linger in the overview as something the user can talk to.
const staleAfter = 12 * time.Hour

// Registry is the live set of sessions and the one the user is talking to.
// Every method takes the lock: the daemon touches it from the Feishu
// reader, from each channel's reader, and from hook connections at once.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
	// selected is the session Feishu messages go to. Sticky on purpose:
	// the user must always know which session they are talking to, so it
	// only ever changes when they choose.
	selected string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sessions: map[string]*Session{}}
}

// Observation is everything one hook event says about a session. Every
// field but ID is optional: a hook reports what it could see, and a field
// it left empty leaves what the registry already knew standing.
type Observation struct {
	ID  string
	PID int
	// Dir is the project directory the session belongs to.
	Dir string
	// Title is the session's own name for its work.
	Title string
	// Transcript is where Claude Code is writing this session's transcript.
	Transcript string
	// HookEvent is the Claude Code event this observation came from.
	HookEvent string
}

// Observe records what a hook event says about a session and returns the
// session it belongs to.
func (r *Registry) Observe(o Observation) Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.resolve(o.ID, o.PID)
	if o.Dir != "" {
		s.Dir = o.Dir
	}
	if o.Title != "" {
		s.Title = o.Title
	}
	if o.Transcript != "" {
		s.Transcript = o.Transcript
	}
	if o.PID != 0 {
		s.PID = o.PID
	}
	if state, ok := StateFor(o.HookEvent); ok {
		s.State = state
	}
	s.LastSeen = time.Now()
	return *s
}

// Attach records that a channel connected for a session, which is both how
// a session becomes continuable and how Wirelark learns it is alive.
func (r *Registry) Attach(id string, pid int, dir string, remote Remote, ch Channel) Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.resolve(id, pid)
	if dir != "" {
		s.Dir = dir
	}
	if pid != 0 {
		s.PID = pid
	}
	s.Remote = remote
	s.channel = ch
	s.LastSeen = time.Now()
	return *s
}

// Detach records that a channel's connection ended. The session goes with
// it: the channel outlives every turn, so losing it means the Claude Code
// process is gone.
//
// It finds the session by its channel rather than by the id the channel
// registered under, because /clear renames a session while its channel
// keeps running.
func (r *Registry) Detach(ch Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, s := range r.sessions {
		if s.channel != ch {
			continue // a later channel already replaced this one
		}
		delete(r.sessions, id)
		r.clearSelectionOf(id)
		return
	}
}

// SessionOf returns the session a channel currently belongs to, which is
// the only reliable way to name it after a /clear.
func (r *Registry) SessionOf(ch Channel) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.channel == ch {
			return *s, true
		}
	}
	return Session{}, false
}

// Remove forgets a session that ended.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
	r.clearSelectionOf(id)
}

// Downgrade records that a session did not accept a message Wirelark sent
// it, so the overview stops claiming it can be continued.
func (r *Registry) Downgrade(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		s.Remote = Notifications
	}
}

// Get returns a copy of one session.
func (r *Registry) Get(id string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// List returns every live session, ordered as the overview reads them.
func (r *Registry) List() []Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.expire()
	live := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		live = append(live, s)
	}
	byAttention(live)

	out := make([]Session, len(live))
	for i, s := range live {
		out[i] = *s
	}
	return out
}

// Select points Feishu messages at one session and reports whether it
// exists. A selection that fails changes nothing: the user is asked to pick
// again rather than having their message sent somewhere they did not choose.
func (r *Registry) Select(id string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return Session{}, false
	}
	r.selected = id
	return *s, true
}

// Selected returns the session the user is talking to. It reports false
// when nothing is selected or the selection has since ended - and it never
// substitutes another session for it.
func (r *Registry) Selected() (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selected == "" {
		return Session{}, false
	}
	s, ok := r.sessions[r.selected]
	if !ok {
		r.selected = ""
		return Session{}, false
	}
	return *s, true
}

// MarkWorking records that a message was pushed into a session. A channel
// event fires no UserPromptSubmit hook, so without this the overview would
// call a session idle at the very moment the user set it working.
func (r *Registry) MarkWorking(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		s.State = Working
		s.LastSeen = time.Now()
	}
}

// resolve finds the session an event belongs to, creating it if new.
//
// The pid lookup is what makes /clear a continuation rather than a second
// session: Claude Code issues a fresh session id, but the process, the
// terminal, and the user's sense of "that session" are all unchanged.
func (r *Registry) resolve(id string, pid int) *Session {
	if s, ok := r.sessions[id]; ok {
		return s
	}
	if pid != 0 {
		for oldID, s := range r.sessions {
			if s.PID != pid {
				continue
			}
			delete(r.sessions, oldID)
			s.ID = id
			r.sessions[id] = s
			if r.selected == oldID {
				r.selected = id // the user is still talking to this session
			}
			debuglog.Printf("session %s continues as %s (pid %d)", oldID, id, pid)
			return s
		}
	}
	s := &Session{ID: id, PID: pid, State: Idle, Remote: Notifications}
	r.sessions[id] = s
	return s
}

// expire drops sessions nothing has been heard from in far too long.
func (r *Registry) expire() {
	cutoff := time.Now().Add(-staleAfter)
	for id, s := range r.sessions {
		if s.Attached() || s.LastSeen.After(cutoff) {
			continue
		}
		delete(r.sessions, id)
		r.clearSelectionOf(id)
	}
}

// clearSelectionOf drops the selection when the session it named is gone.
func (r *Registry) clearSelectionOf(id string) {
	if r.selected == id {
		r.selected = ""
	}
}

// snapshot is the registry as it survives a daemon restart. Channels are
// deliberately absent: a live link cannot be restored from a file, and the
// channels reconnect on their own.
type snapshot struct {
	Sessions []snapshotSession `json:"sessions"`
	Selected string            `json:"selected,omitempty"`
}

type snapshotSession struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	Dir        string    `json:"dir"`
	Title      string    `json:"title,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
	State      State     `json:"state"`
	LastSeen   time.Time `json:"last_seen"`
}

// Save writes the registry to disk.
func (r *Registry) Save() error {
	r.mu.Lock()
	snap := snapshot{Selected: r.selected}
	for _, s := range r.sessions {
		snap.Sessions = append(snap.Sessions, snapshotSession{
			ID: s.ID, PID: s.PID, Dir: s.Dir, Title: s.Title,
			Transcript: s.Transcript, State: s.State, LastSeen: s.LastSeen,
		})
	}
	r.mu.Unlock()

	p, err := paths.File(snapshotFile)
	if err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return secfile.WriteAtomic(p, data, 0o600)
}

// Load restores the registry a previous daemon left behind. Every restored
// session is Notifications-only until its channel reconnects and proves
// otherwise, and one whose process is gone is dropped rather than offered.
func Load() *Registry {
	r := NewRegistry()
	p, err := paths.File(snapshotFile)
	if err != nil {
		return r
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return r
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return r // a corrupt snapshot is not worth a broken start
	}
	cutoff := time.Now().Add(-staleAfter)
	for _, s := range snap.Sessions {
		if s.LastSeen.Before(cutoff) || !processAlive(s.PID) {
			continue
		}
		r.sessions[s.ID] = &Session{
			ID: s.ID, PID: s.PID, Dir: s.Dir, Title: s.Title,
			Transcript: s.Transcript, State: s.State,
			Remote: Notifications, LastSeen: s.LastSeen,
		}
	}
	if _, ok := r.sessions[snap.Selected]; ok {
		r.selected = snap.Selected
	}
	return r
}

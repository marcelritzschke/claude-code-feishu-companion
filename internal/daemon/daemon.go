// Package daemon is the one persistent Claude Companion role. It owns the Feishu
// connection in both directions and everything that depends on knowing more
// than one moment: which sessions exist, which one the user is talking to,
// and which card on their phone still stands for something unanswered.
//
// The hook processes and the channels are deliberately thin around it. A
// hook lives for one event and a channel holds no credentials, so the
// judgement about what the user sees happens in exactly one place.
package daemon

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/debuglog"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/update"
)

// snapshotEvery is how often the registry is written out, so a daemon that
// is killed rather than stopped loses at most this much of its picture.
const snapshotEvery = 30 * time.Second

// Daemon is the running bridge.
type Daemon struct {
	cfg *config.Config
	out sender
	in  inbound
	reg *session.Registry

	mu sync.Mutex
	// prompts tracks every permission card that still stands, by request id
	// and by session, so a decision answered anywhere settles the card
	// everywhere.
	byRequest map[string]*prompt
	bySession map[string]*prompt
	// awaiting holds messages pushed into a session that have not yet
	// proved they arrived.
	awaiting map[string]*delivery
	// lastOverview is the sessions the last overview offered, in the order
	// it numbered them, so a typed "2" means the second one the user saw.
	lastOverview []string
	// watches are the sessions the user asked to see live, by session id.
	watches map[string]*watch
	// pace is how often a watch looks and how often it may rewrite its
	// card. Set once at construction and read-only thereafter.
	pace pace
	// interrupt delivers a turn interrupt to a session. It is a field so
	// tests can interrupt without signalling a real process.
	interrupt func(session.Session) error

	// version is the running binary's own version, "dev" if unlinked. It
	// is what checkForUpdate compares GitHub's latest release against.
	version string
	// fetchRelease fetches the latest release. A field so tests can stand
	// in for GitHub.
	fetchRelease func(context.Context) (update.Release, error)

	// inboundWaiters are one-shot callers watching for proof that Feishu
	// can reach this machine. Setup uses it; nothing else does.
	inboundWaiters []chan inboundProof

	// configStamp is when the config file this daemon read was last
	// written. It is reported on status so that a caller holding a newer
	// config knows this daemon predates it - the Feishu connection is made
	// once, at startup, from whatever the file said then.
	configStamp time.Time

	stop chan struct{}
	once sync.Once
}

// sender is the outbound Feishu side, narrowed to what the daemon does with
// it so tests can record instead of send.
type sender interface {
	SendCard(ctx context.Context, cardJSON string) (string, error)
	UpdateCard(ctx context.Context, messageID, cardJSON string) error
	SendText(ctx context.Context, text string) (string, error)
	DeleteMessage(ctx context.Context, messageID string) error
}

// inbound is the return path from Feishu.
type inbound interface {
	Run(ctx context.Context) error
	Messages() <-chan feishu.Message
	Actions() <-chan feishu.CardAction
	// Strangers reports events dropped for coming from another account.
	Strangers() <-chan string
}

// prompt is one attention card still standing: a permission decision the
// user can still make, or a question they were shown.
type prompt struct {
	sessionID string
	messageID string
	// kind says what the card asks for: promptPermission or promptQuestion.
	kind string
	req  mcp.PermissionRequest
	// relayed is false while only the hook-driven card stands, which is
	// what a session Claude Companion cannot reach ever gets.
	relayed bool
	settled bool
}

// prompt kinds.
const (
	promptPermission = "permission"
	promptQuestion   = "question"
)

// inboundProof is what actually arrived from Feishu, for a caller waiting
// to find out whether the return path works.
type inboundProof struct {
	// stranger is true when the event came from an account other than the
	// configured owner: Feishu reached this computer, and this computer
	// answers to somebody else.
	stranger bool
	// from is the sender id of a stranger's event, empty otherwise.
	from string
}

// delivery is a message pushed into a session, waiting for proof it landed.
type delivery struct {
	sessionID string
	sentAt    time.Time
}

// Run starts the daemon and blocks until it is stopped or ctx ends. Only
// one daemon may run at a time; a second one exits with ErrAlreadyRunning
// rather than competing for the socket. version identifies the running
// binary, "dev" if it carries no linked version.
func Run(ctx context.Context, version string) error {
	lock, err := acquire()
	if err != nil {
		return err
	}
	defer lock.Close()

	// The daemon is usually started by a hook or a channel and inherits
	// that session's environment. None of it describes the daemon, and
	// CLAUDE_PROJECT_DIR left standing would label every session's cards
	// with the project of whichever one happened to start it.
	for _, v := range []string{"CLAUDE_PROJECT_DIR", "CLAUDE_CODE_SESSION_ID", "CLAUDE_PID"} {
		os.Unsetenv(v)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	stamp, err := config.Stamp()
	if err != nil {
		return err
	}
	out, err := feishu.New(cfg)
	if err != nil {
		return err
	}

	d := New(cfg, out, nil, version)
	d.configStamp = stamp
	if cfg.RemoteEnabled() {
		d.in = feishu.NewInbound(cfg)
	}
	return d.Serve(ctx)
}

// New builds a daemon around an already-made Feishu side, which is what
// lets a test drive the whole thing without an account.
func New(cfg *config.Config, out sender, in inbound, version string) *Daemon {
	return &Daemon{
		cfg:          cfg,
		out:          out,
		in:           in,
		reg:          session.Load(),
		byRequest:    map[string]*prompt{},
		bySession:    map[string]*prompt{},
		awaiting:     map[string]*delivery{},
		watches:      map[string]*watch{},
		pace:         defaultPace,
		interrupt:    func(s session.Session) error { return s.Interrupt() },
		version:      version,
		fetchRelease: update.Fetch,
		stop:         make(chan struct{}),
	}
}

// Serve runs the daemon's loops until it is stopped.
func (d *Daemon) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	listener, err := ipc.Listen()
	if err != nil {
		return err
	}
	defer listener.Close()
	debuglog.Printf("daemon listening")

	var wg sync.WaitGroup
	if d.in != nil {
		wg.Add(2)
		go func() { defer wg.Done(); d.runInbound(ctx) }()
		go func() { defer wg.Done(); d.readInbound(ctx) }()
	}
	wg.Add(3)
	go func() { defer wg.Done(); d.acceptLoop(ctx, listener) }()
	go func() { defer wg.Done(); d.housekeep(ctx) }()
	go func() { defer wg.Done(); d.watchForUpdates(ctx) }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-ctx.Done():
	case <-d.stop:
	case sig := <-signals:
		debuglog.Printf("daemon stopping on %s", sig)
	}

	cancel()
	listener.Close() // unblocks the accept loop
	wg.Wait()
	// The live cards go last and on a context of their own: the one thing
	// a stopping daemon still owes the user is that nothing it left on
	// their phone claims to be watching something.
	shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	d.closeAllWatches(shutdown)
	done()
	if err := d.reg.Save(); err != nil {
		debuglog.Printf("save sessions: %v", err)
	}
	debuglog.Printf("daemon stopped")
	return nil
}

// halt ends the daemon. Safe to call more than once, from any goroutine.
func (d *Daemon) halt() { d.once.Do(func() { close(d.stop) }) }

// runInbound keeps the Feishu connection up. A failure here is not fatal:
// notifications still flow outward, so the daemon keeps running and keeps
// trying rather than taking the whole bridge down.
func (d *Daemon) runInbound(ctx context.Context) {
	for ctx.Err() == nil {
		if err := d.in.Run(ctx); err != nil && ctx.Err() == nil {
			debuglog.Printf("feishu inbound: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

// readInbound turns what the user did in Feishu into what happens here.
func (d *Daemon) readInbound(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-d.in.Messages():
			d.notifyInboundWaiters(inboundProof{})
			d.onMessage(ctx, msg)
		case action := <-d.in.Actions():
			d.notifyInboundWaiters(inboundProof{})
			d.onCardAction(ctx, action)
		case from := <-d.in.Strangers():
			// Nothing is done with a stranger's event, but somebody
			// waiting to find out whether Feishu can reach this computer
			// has their answer: it can.
			d.notifyInboundWaiters(inboundProof{stranger: true, from: from})
		}
	}
}

// acceptLoop serves channels, hooks, and tooling.
func (d *Daemon) acceptLoop(ctx context.Context, l *ipc.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() == nil {
				debuglog.Printf("accept: %v", err)
			}
			return
		}
		go d.serve(ctx, conn)
	}
}

// serve handles one peer. The first message says which role it is: a
// channel stays for the life of its session, everything else is one
// exchange and gone.
func (d *Daemon) serve(ctx context.Context, conn *ipc.Conn) {
	defer conn.Close()

	env, err := conn.Read()
	if err != nil {
		return
	}
	switch env.Type {
	case ipc.TypeRegister:
		d.serveChannel(ctx, conn, env)
	case ipc.TypeHook:
		d.serveHook(ctx, conn, env)
	case ipc.TypeStatus:
		d.replyStatus(conn)
	case ipc.TypeStop:
		d.reply(conn, ipc.Ack{OK: true})
		d.halt()
	case ipc.TypeAwaitInbound:
		d.serveAwaitInbound(ctx, conn)
	default:
		d.reply(conn, ipc.Ack{Err: "unknown request " + env.Type})
	}
}

// replyStatus says that this daemon is answering, and which configuration
// it is answering with.
func (d *Daemon) replyStatus(conn *ipc.Conn) {
	if err := conn.Write(ipc.TypeAck, ipc.Status{OK: true, ConfigStamp: d.configStamp}); err != nil {
		debuglog.Printf("reply: %v", err)
	}
}

func (d *Daemon) reply(conn *ipc.Conn, ack ipc.Ack) {
	if err := conn.Write(ipc.TypeAck, ack); err != nil {
		debuglog.Printf("reply: %v", err)
	}
}

// housekeep writes the registry out and gives up on messages that never
// proved they arrived.
func (d *Daemon) housekeep(ctx context.Context) {
	ticker := time.NewTicker(snapshotEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.reg.Save(); err != nil {
				debuglog.Printf("save sessions: %v", err)
			}
			d.expireDeliveries(ctx)
		}
	}
}

// serveAwaitInbound answers once something reaches this machine from
// Feishu, which is the only proof that the return path really works. Setup
// uses it to check the connection while the user is still there to fix it.
func (d *Daemon) serveAwaitInbound(ctx context.Context, conn *ipc.Conn) {
	if d.in == nil {
		d.replyProof(conn, ipc.InboundProof{Err: "remote continuation is switched off"})
		return
	}
	waiter := make(chan inboundProof, 1)
	d.mu.Lock()
	d.inboundWaiters = append(d.inboundWaiters, waiter)
	d.mu.Unlock()

	select {
	case proof := <-waiter:
		if proof.stranger {
			d.replyProof(conn, ipc.InboundProof{Stranger: true, Err: strangerReason(proof.from, d.cfg.OpenID)})
			return
		}
		d.replyProof(conn, ipc.InboundProof{OK: true})
	case <-ctx.Done():
		d.replyProof(conn, ipc.InboundProof{Err: "the daemon stopped"})
	case <-time.After(ipc.InboundProbeWait):
		d.replyProof(conn, ipc.InboundProof{Err: "no message arrived"})
	}
}

// strangerReason names both accounts, because the fix is to set this
// computer up for the one the user actually messages from - and Feishu ids
// are per app, so the configured one can be a real id from the wrong app.
func strangerReason(from, want string) string {
	if from == "" {
		return "a message arrived, but from a Feishu account other than " + want
	}
	return "a message arrived from " + from + ", but this computer answers only to " + want
}

func (d *Daemon) replyProof(conn *ipc.Conn, proof ipc.InboundProof) {
	if err := conn.Write(ipc.TypeAck, proof); err != nil {
		debuglog.Printf("reply: %v", err)
	}
}

func (d *Daemon) notifyInboundWaiters(proof inboundProof) {
	d.mu.Lock()
	waiters := d.inboundWaiters
	d.inboundWaiters = nil
	d.mu.Unlock()
	for _, w := range waiters {
		select {
		case w <- proof:
		default:
		}
	}
}

// sendTimeout bounds one Feishu call.
const sendTimeout = 15 * time.Second

// sendCard delivers a card and reports the message it became.
func (d *Daemon) sendCard(ctx context.Context, cardJSON string, err error) string {
	if err != nil {
		debuglog.Printf("build card: %v", err)
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	id, err := d.out.SendCard(ctx, cardJSON)
	if err != nil {
		debuglog.Printf("send card: %v", err)
		return ""
	}
	return id
}

// updateCard rewrites a card that is already standing.
func (d *Daemon) updateCard(ctx context.Context, messageID, cardJSON string, err error) {
	if err != nil {
		debuglog.Printf("build card: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := d.out.UpdateCard(ctx, messageID, cardJSON); err != nil {
		debuglog.Printf("update card: %v", err)
	}
}

// deleteCard recalls a card the bot sent and reports whether it is gone.
// Feishu refuses recalls past its window, so a false return is ordinary
// and the caller settles the card in place instead.
func (d *Daemon) deleteCard(ctx context.Context, messageID string) bool {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := d.out.DeleteMessage(ctx, messageID); err != nil {
		debuglog.Printf("delete card %s: %v", messageID, err)
		return false
	}
	return true
}

// say sends a short confirmation. These are answers to something the user
// just did, not notifications, so they are plain messages rather than cards.
func (d *Daemon) say(ctx context.Context, text string) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if _, err := d.out.SendText(ctx, text); err != nil {
		debuglog.Printf("send text: %v", err)
	}
}

// errNoChannel reports that a session has no live link to push into.
var errNoChannel = errors.New("this session has no live connection to Claude Companion")

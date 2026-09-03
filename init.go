package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/channel"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/daemon"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/deliver"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hook"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/hooksreg"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/ipc"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/register"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/transcript"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/tui"
)

const initTimeout = 15 * time.Second

// inboundCheckTimeout is how long setup waits for the user to send the bot
// a message. Generous: they have to pick up their phone.
const inboundCheckTimeout = 2 * time.Minute

// registerTimeout bounds the QR flow generously. The code Feishu issues
// lives about ten minutes and reports its own expiry, so this is only a
// backstop against a poll that never returns.
const registerTimeout = 15 * time.Minute

// setupPath is how the Feishu app Claude Companion talks through came to exist. It
// is remembered only so that the advice printed when something does not
// work matches what the user actually did: telling someone to tick a box
// in a console they never opened is worse than saying nothing.
type setupPath int

const (
	// pathScanned means Feishu created the app from the QR registration,
	// with Claude Companion's permissions and subscriptions already requested.
	pathScanned setupPath = iota
	// pathExisting means the user brought their own app, so nothing is
	// known about how it is configured.
	pathExisting
)

// runInit is interactive and allowed to fail loudly - it is never run by a
// hook. Order matters: validate credentials with a test card before saving
// the config or touching settings.json.
func runInit() error {
	// Setup holds the terminal in raw mode so its questions can read
	// arrow keys. Close gives it back, and must run whichever way this
	// returns - including through a failure partway down.
	defer tui.Close()
	tui.Title("Claude Companion", "Claude Code, on your phone")

	cfg, client, how, err := connectFeishu()
	if err != nil {
		return err
	}
	if err := askBehavior(cfg); err != nil {
		return err
	}
	if err := sendTestCard(cfg, client, how); err != nil {
		return err
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	path, _ := config.Path()
	tui.Done("Settings saved")
	tui.Detail(path)

	cmd, err := executableCommand()
	if err != nil {
		return err
	}
	if err := registerHooks(cfg, cmd); err != nil {
		return err
	}
	if !cfg.RemoteEnabled() {
		return nil
	}
	if err := registerChannel(); err != nil {
		return err
	}
	checkReturnPath(how)
	explainLaunch()
	return nil
}

// connectFeishu gets Claude Companion a working Feishu app and the identity of the
// person it belongs to.
//
// It opens straight into the QR code rather than asking which way the user
// wants to do this. Almost nobody arrives holding a Feishu app, and asking
// everyone a question that only matters to a few is how setup used to
// start with a request for an App Secret. The existing-app path is offered
// on the scan screen instead, and again if the scan does not work out.
func connectFeishu() (*config.Config, *feishu.Client, setupPath, error) {
	ctx, cancel := context.WithTimeout(context.Background(), registerTimeout)
	defer cancel()

	res, outcome, err := tui.Scan(ctx)
	switch outcome {
	case tui.Scanned:
		cfg, client, err := fromScan(res)
		return cfg, client, pathScanned, err

	case tui.Cancelled:
		return nil, nil, pathScanned, err

	case tui.Failed:
		// The scan ended somewhere the user cannot fix by scanning again.
		// Offering the other path beats sending them away with an error.
		tui.Fail("Registration did not complete")
		tui.Detail(err.Error())
		tui.Blank()
		fallback, ferr := tui.Confirm("Use an existing Feishu app instead?",
			"Someone with a Feishu app already created - an administrator, usually - can enter it by hand.")
		if ferr != nil {
			return nil, nil, pathExisting, ferr
		}
		if !fallback {
			return nil, nil, pathScanned, err
		}
	}

	cfg, client, err := askCredentials()
	return cfg, client, pathExisting, err
}

// fromScan turns a completed registration into a configuration.
// Everything the manual path has to ask for - the app id, the secret, who
// the user is, which Feishu they are on - comes back from the one scan.
func fromScan(res *register.Result) (*config.Config, *feishu.Client, error) {
	if res.OwnerOpenID == "" {
		// Without an identity there is no owner, and Claude Companion would have
		// nobody to send to and nobody to accept messages from.
		return nil, nil, errors.New("feishu did not report who scanned the code")
	}
	cfg := &config.Config{
		AppID:      res.AppID,
		AppSecret:  res.AppSecret,
		OpenID:     res.OwnerOpenID,
		OpenIDKind: config.OpenIDTypeOpenID,
		Brand:      res.Brand,
	}
	client, err := feishu.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	tui.Done("Connected to Feishu")
	tui.Detail("the account you scanned with is this computer's Claude Companion owner")
	tui.Blank()
	return cfg, client, nil
}

// askCredentials collects the Feishu app identity by hand, for a managed
// environment where an administrator creates the app and hands it over.
func askCredentials() (*config.Config, *feishu.Client, error) {
	tui.Step("Use an existing Feishu app")
	tui.Blank()

	appID, err := tui.Ask("App ID", "From the Feishu developer console, under Credentials.", "cli_...", true)
	if err != nil {
		return nil, nil, err
	}
	appSecret, err := tui.AskSecret("App Secret", "Not echoed, and stored with the config file at 0600.")
	if err != nil {
		return nil, nil, err
	}
	brand, err := tui.Choose("Which Feishu is the app registered on?", "", []tui.Choice[config.Brand]{
		{Label: "Feishu", Note: "open.feishu.cn", Value: config.BrandFeishu},
		{Label: "Lark", Note: "open.larksuite.com", Value: config.BrandLark},
	})
	if err != nil {
		return nil, nil, err
	}
	ident, err := tui.Ask("Who is Claude Companion for?",
		"Your Feishu user id, or the email address on your Feishu account.", "ou_... or you@company.com", true)
	if err != nil {
		return nil, nil, err
	}

	cfg := &config.Config{AppID: appID, AppSecret: appSecret, Brand: brand}
	client, err := feishu.New(cfg)
	if err != nil {
		return nil, nil, err
	}

	if strings.HasPrefix(ident, "ou_") || strings.HasPrefix(ident, "on_") || !strings.Contains(ident, "@") {
		// Looks like a Feishu id: keep it verbatim and record its kind so
		// SendCard uses the matching ReceiveIdType.
		cfg.OpenID = ident
		cfg.OpenIDKind = config.DetectOpenIDType(ident)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
		openID, err := client.ResolveOpenID(ctx, ident)
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("resolving email: %w", err)
		}
		cfg.OpenID = openID
		tui.Done("Found your Feishu account")
		tui.Detail(openID)
	}
	tui.Blank()
	return cfg, client, nil
}

// askBehavior asks the four questions that describe what Claude Companion does,
// in the user's terms rather than in hook events.
func askBehavior(cfg *config.Config) error {
	notify, err := tui.Choose("What should Claude Companion tell you about?", "", []tui.Choice[config.NotifyLevel]{
		{Label: "Important only", Note: "attention, failures, completion", Value: config.NotifyImportant},
		{Label: "Important and progress", Note: "also pings once a task runs long", Value: config.NotifyProgress},
	})
	if err != nil {
		return err
	}
	cfg.Notify = notify

	detail, err := tui.Choose("How much should a finished turn say?", "", []tui.Choice[config.DetailLevel]{
		{Label: "Normal", Note: "summary, validation, and Claude's answer", Value: config.DetailNormal},
		{Label: "Compact", Note: "one-glance summary", Value: config.DetailCompact},
	})
	if err != nil {
		return err
	}
	cfg.Detail = detail

	remote, err := tui.Choose("Continue sessions from Feishu?",
		"Pick one of the Claude Code sessions running here, send it a follow-up, or watch it work.",
		[]tui.Choice[config.Switch]{
			{Label: "Yes", Note: "reply from your phone", Value: config.On},
			{Label: "No", Note: "notifications only", Value: config.Off},
		})
	if err != nil {
		return err
	}
	cfg.Remote = remote

	cfg.RemotePermissions = config.Off
	if !cfg.RemoteEnabled() {
		return nil
	}
	perms, err := tui.Choose("Approve permission requests from Feishu?",
		"Anyone who can message your Claude Companion bot can allow or deny a command in your session while this is on.",
		[]tui.Choice[config.Switch]{
			{Label: "Yes", Note: "cards get Allow and Deny buttons", Value: config.On},
			{Label: "No", Note: "notifications only; answer in Claude Code", Value: config.Off},
		})
	if err != nil {
		return err
	}
	cfg.RemotePermissions = perms
	return nil
}

// sendTestCard proves the credentials before anything is written
// anywhere. The card is a real completion card, so setup exercises the
// exact path a finished turn takes.
func sendTestCard(cfg *config.Config, client *feishu.Client, how setupPath) error {
	cwd, _ := os.Getwd()
	testTurn := &transcript.Turn{Start: time.Now().Add(-42 * time.Second)}
	card, err := notify.CompletionCard(&hook.Payload{
		HookEventName:        hook.EventStop,
		Cwd:                  cwd,
		LastAssistantMessage: "Claude Companion is connected. You will get a message here when Claude finishes, hits a problem, or needs a decision from you.",
	}, testTurn, notify.Options{Detail: deliver.DetailOf(cfg)})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	if _, err := client.SendCard(ctx, card); err != nil {
		tui.Fail("Test card would not send")
		tui.Detail(err.Error())
		tui.Detail(whyNoCard(how))
		return errors.New("Feishu rejected the test card")
	}
	tui.Done("Test card delivered - check your Feishu DM")
	return nil
}

// registerHooks puts Claude Companion's hooks in the user's Claude Code settings.
func registerHooks(cfg *config.Config, cmd string) error {
	settingsPath, err := hooksreg.SettingsPath()
	if err != nil {
		return err
	}
	ok, err := tui.Confirm("Register Claude Companion's hooks with Claude Code?",
		"Adds Claude Companion to "+settingsPath+", keeping a backup alongside.")
	if err != nil {
		return err
	}
	if !ok {
		tui.Warn("Skipped hook registration - Claude Companion will stay quiet until it is done")
		return nil
	}
	changed, err := hooksreg.Register(settingsPath, cmd, hooksreg.Settings{
		Progress: cfg.ProgressEnabled(),
		Remote:   cfg.RemoteEnabled(),
	})
	if err != nil {
		return fmt.Errorf("registering hooks (a backup was written if the file changed): %w", err)
	}
	if changed {
		tui.Done("Hooks registered")
	} else {
		tui.Done("Hooks already registered")
	}
	tui.Detail(settingsPath)
	return nil
}

// registerChannel registers the channel server with Claude Code, through
// Claude Code's own CLI. ~/.claude.json is Claude Code's state file, not a
// configuration format Claude Companion should be editing behind its back.
func registerChannel() error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	addArgs := []string{"mcp", "add", "-s", "user", channel.ServerName, "--", exe, "channel"}

	ok, err := tui.Confirm("Register the Claude Companion channel with Claude Code?",
		"This is what carries your replies into a running session.")
	if err != nil {
		return err
	}
	if !ok {
		tui.Warn("Skipped - register it yourself with:")
		tui.Detail("claude " + strings.Join(addArgs, " "))
		return nil
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		tui.Warn("Could not find the claude command - register it yourself with:")
		tui.Detail("claude " + strings.Join(addArgs, " "))
		return nil
	}

	// Remove first so a Claude Companion installed at a different path, or
	// registered under the project's former name (wirelark), is replaced
	// rather than left alongside this one. Only Claude Companion's own
	// entries are touched, and their absence is not an error.
	_ = exec.Command(claude, "mcp", "remove", "-s", "user", channel.ServerName).Run()
	_ = exec.Command(claude, "mcp", "remove", "-s", "user", channel.LegacyServerName).Run()

	if out, err := exec.Command(claude, addArgs...).CombinedOutput(); err != nil {
		tui.Warn("claude mcp add failed - register it yourself with:")
		tui.Detail(strings.TrimSpace(string(out)))
		tui.Detail("claude " + strings.Join(addArgs, " "))
		return nil
	}
	tui.Done("Channel registered with Claude Code")
	return nil
}

// checkReturnPath proves Feishu can reach this machine while the user is
// still here to fix it if it cannot. Sending is not evidence: the test card
// already went out over a path that has nothing to do with this one.
func checkReturnPath(how setupPath) {
	tui.Blank()
	tui.Step("Checking that Feishu can reach this computer")
	tui.Blank()
	if err := daemon.EnsureRunning(); err != nil {
		tui.Fail("Could not start the Claude Companion daemon")
		tui.Detail(err.Error())
		return
	}
	tui.Info("Send any message to the Claude Companion bot in Feishu now.")
	tui.Detail(fmt.Sprintf("waiting up to %s", inboundCheckTimeout))

	env, err := ipc.Request(ipc.TypeAwaitInbound, nil, inboundCheckTimeout)
	if err != nil {
		explainNoInbound(how, err)
		return
	}
	var ack ipc.Ack
	if err := env.Into(&ack); err != nil {
		explainNoInbound(how, err)
		return
	}
	if !ack.OK {
		explainNoInbound(how, errors.New(ack.Err))
		return
	}
	tui.Done("Message received - Feishu can reach this computer")
}

// explainNoInbound names what to do about a return path that stayed
// silent. A scanned app was asked for all of this during registration, so
// the useful advice there is about approval and about the app being
// released, not about a console the user never opened.
func explainNoInbound(how setupPath, err error) {
	tui.Warn("No message reached Claude Companion")
	tui.Detail(err.Error())
	tui.Blank()
	if how == pathScanned {
		tui.Detail("The registration asked Feishu for everything Claude Companion needs, so this\n" +
			"usually means the app is waiting on someone:\n" +
			"  - the permissions may still need your administrator's approval\n" +
			"  - the app version may need releasing before subscriptions take effect")
	} else {
		tui.Detail("In the Feishu developer console, check that the app has:\n" +
			"  - event subscription set to long connection, not a webhook URL\n" +
			needsList())
	}
	tui.Blank()
	tui.Info("Notifications already work. Re-run %s once that is fixed.", tui.Code("claude-companion init"))
}

// needsList renders what Claude Companion asks a Feishu app for, from the same
// list the registration requests, so console instructions and the QR flow
// can never drift apart.
func needsList() string {
	needs := register.Needs()
	var b strings.Builder
	for _, s := range needs.Scopes {
		fmt.Fprintf(&b, "  - the scope  %s\n", s)
	}
	for _, e := range needs.Events {
		fmt.Fprintf(&b, "  - the event  %s\n", e)
	}
	for _, c := range needs.Callbacks {
		fmt.Fprintf(&b, "  - the callback  %s  , for the Allow and Deny buttons\n", c)
	}
	return strings.TrimRight(b.String(), "\n")
}

// whyNoCard explains a test card that would not send. It is the first
// thing that talks to Feishu with the new credentials, so it is where a
// half-finished app shows up.
func whyNoCard(how setupPath) string {
	if how == pathScanned {
		return "The app was created, so the credentials are real - something is holding it back.\n" +
			"Most often the permissions are waiting on an administrator's approval, or the\n" +
			"app has not been released to you yet. Re-run claude-companion init after that."
	}
	return "Check the app credentials, that the app has the bot capability, and that\n" +
		"you are inside the app's availability scope."
}

// explainLaunch states the one thing the user has to do differently, and
// why it is temporary. Wrapping it in a launcher would make a research
// preview's restriction into a permanent part of the product.
func explainLaunch() {
	tui.Blank()
	tui.Step("One more thing")
	tui.Blank()
	tui.Info("Claude Code channels are a research preview, and a channel that is not on")
	tui.Info("Anthropic's allowlist has to be opted in per session. Until Claude Companion is on")
	tui.Info("that list, start sessions you want to continue from Feishu with:")
	tui.Blank()
	tui.Detail("claude --dangerously-load-development-channels server:" + channel.ServerName)
	tui.Blank()
	tui.Info("Sessions started with a plain %s still send you notifications;", tui.Code("claude"))
	tui.Info("Feishu will show them as \"Notifications only\".")
	tui.Blank()
}

// executablePath returns the absolute path of this binary.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// executableCommand returns the absolute path of this binary plus "send",
// ready to be written into settings.json.
func executableCommand() (string, error) {
	exe, err := executablePath()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%q send", exe), nil
}

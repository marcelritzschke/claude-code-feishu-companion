package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/channel"
	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/daemon"
	"github.com/marcelritzschke/wirelark/internal/deliver"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/hooksreg"
	"github.com/marcelritzschke/wirelark/internal/ipc"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

const initTimeout = 15 * time.Second

// inboundCheckTimeout is how long setup waits for the user to send the bot
// a message. Generous: they have to pick up their phone.
const inboundCheckTimeout = 2 * time.Minute

// runInit is interactive and allowed to fail loudly - it is never run by a
// hook. Order matters: validate credentials with a test card before saving
// the config or touching settings.json.
func runInit() error {
	reader := bufio.NewReader(os.Stdin)

	cfg, client, err := askCredentials(reader)
	if err != nil {
		return err
	}
	askBehavior(reader, cfg)

	// The test card is a real completion card, so setup exercises the exact
	// path a finished turn takes.
	cwd, _ := os.Getwd()
	testTurn := &transcript.Turn{Start: time.Now().Add(-42 * time.Second)}
	testCard, err := notify.CompletionCard(&hook.Payload{
		HookEventName:        hook.EventStop,
		Cwd:                  cwd,
		LastAssistantMessage: "Wirelark is connected. You will get a message here when Claude finishes, hits a problem, or needs a decision from you.",
	}, testTurn, notify.Options{Detail: deliver.DetailOf(cfg)})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	if _, err := client.SendCard(ctx, testCard); err != nil {
		return fmt.Errorf("test card: %w (check app credentials, bot capability, and that you are in the app's availability scope)", err)
	}
	fmt.Println("test card delivered - check your Feishu DM")

	if err := cfg.Save(); err != nil {
		return err
	}
	path, _ := config.Path()
	fmt.Printf("config saved to %s\n", path)

	cmd, err := executableCommand()
	if err != nil {
		return err
	}
	if err := registerHooks(reader, cfg, cmd); err != nil {
		return err
	}
	if !cfg.RemoteEnabled() {
		return nil
	}
	if err := registerChannel(reader); err != nil {
		return err
	}
	checkReturnPath()
	explainLaunch()
	return nil
}

// askCredentials collects the Feishu app identity and confirms it can be
// used, before anything is written anywhere.
func askCredentials(reader *bufio.Reader) (*config.Config, *feishu.Client, error) {
	appID := prompt(reader, "Feishu app_id (cli_...)")
	if appID == "" {
		return nil, nil, errors.New("app_id is required")
	}
	appSecret := prompt(reader, "Feishu app_secret (input is visible; run in a private terminal)")
	if appSecret == "" {
		return nil, nil, errors.New("app_secret is required")
	}
	ident := prompt(reader, "Your Feishu user id (ou_... / on_... / user_id) or the email of your Feishu account")
	if ident == "" {
		return nil, nil, errors.New("user id or email is required")
	}

	cfg := &config.Config{AppID: appID, AppSecret: appSecret}
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
		fmt.Printf("resolved open_id: %s\n", openID)
	}
	return cfg, client, nil
}

// askBehavior asks the four questions that describe what Wirelark does,
// in the user's terms rather than in hook events.
func askBehavior(reader *bufio.Reader, cfg *config.Config) {
	fmt.Println("\nNotification level")
	fmt.Println("  1) Important only - attention, failures, completion (default)")
	fmt.Println("  2) Important + progress - also pings once a task runs long")
	cfg.Notify = config.NotifyLevel(promptChoice(reader, "Choice",
		[]string{string(config.NotifyImportant), string(config.NotifyProgress)}))

	fmt.Println("\nCompletion detail")
	fmt.Println("  1) Normal - summary, validation, and Claude's answer (default)")
	fmt.Println("  2) Compact - one-glance summary")
	cfg.Detail = config.DetailLevel(promptChoice(reader, "Choice",
		[]string{string(config.DetailNormal), string(config.DetailCompact)}))

	fmt.Println("\nContinue sessions from Feishu")
	fmt.Println("  1) Yes - pick a running Claude Code session and send it a message (default)")
	fmt.Println("  2) No  - notifications only, as before")
	cfg.Remote = config.Switch(promptChoice(reader, "Choice",
		[]string{string(config.On), string(config.Off)}))

	cfg.RemotePermissions = config.Off
	if !cfg.RemoteEnabled() {
		return
	}
	fmt.Println("\nApprove permission requests from Feishu")
	fmt.Println("  Anyone who can message your Wirelark bot can allow or deny a command")
	fmt.Println("  in your session while this is on.")
	fmt.Println("  1) Yes - relay permission prompts with Allow and Deny buttons (default)")
	fmt.Println("  2) No  - permission notifications only; answer in Claude Code")
	cfg.RemotePermissions = config.Switch(promptChoice(reader, "Choice",
		[]string{string(config.On), string(config.Off)}))
}

// registerHooks puts Wirelark's hooks in the user's Claude Code settings.
func registerHooks(reader *bufio.Reader, cfg *config.Config, cmd string) error {
	if !confirm(reader, "Register hooks in ~/.claude/settings.json? [Y/n] ") {
		fmt.Println("skipped hook registration")
		return nil
	}
	settingsPath, err := hooksreg.SettingsPath()
	if err != nil {
		return err
	}
	changed, err := hooksreg.Register(settingsPath, cmd, hooksreg.Settings{
		Progress: cfg.ProgressEnabled(),
		Remote:   cfg.RemoteEnabled(),
	})
	if err != nil {
		return fmt.Errorf("registering hooks (a backup was written if the file changed): %w", err)
	}
	if changed {
		fmt.Printf("hooks registered in %s (backup alongside)\n", settingsPath)
	} else {
		fmt.Printf("hooks already registered in %s\n", settingsPath)
	}
	return nil
}

// registerChannel registers the channel server with Claude Code, through
// Claude Code's own CLI. ~/.claude.json is Claude Code's state file, not a
// configuration format Wirelark should be editing behind its back.
func registerChannel(reader *bufio.Reader) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	addArgs := []string{"mcp", "add", "-s", "user", channel.ServerName, "--", exe, "channel"}

	if !confirm(reader, "Register the Wirelark channel with Claude Code? [Y/n] ") {
		printChannelCommand(addArgs)
		return nil
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("\ncould not find the claude command; register the channel yourself:")
		printChannelCommand(addArgs)
		return nil
	}

	// Remove first so a Wirelark installed at a different path is replaced
	// rather than left alongside this one. Only Wirelark's own entry is
	// touched, and its absence is not an error.
	_ = exec.Command(claude, "mcp", "remove", "-s", "user", channel.ServerName).Run()

	if out, err := exec.Command(claude, addArgs...).CombinedOutput(); err != nil {
		fmt.Printf("\nclaude mcp add failed: %v\n%s\nregister the channel yourself:\n", err, strings.TrimSpace(string(out)))
		printChannelCommand(addArgs)
		return nil
	}
	fmt.Println("channel registered with Claude Code (user scope)")
	return nil
}

func printChannelCommand(args []string) {
	fmt.Printf("  claude %s\n", strings.Join(args, " "))
}

// checkReturnPath proves Feishu can reach this machine while the user is
// still here to fix it if it cannot. Sending is not evidence: the test card
// already went out over a path that has nothing to do with this one.
func checkReturnPath() {
	fmt.Println("\nChecking that Feishu can reach this computer.")
	if err := daemon.EnsureRunning(); err != nil {
		fmt.Printf("could not start the Wirelark daemon: %v\n", err)
		return
	}
	fmt.Printf("Send any message to the Wirelark bot in Feishu now (waiting up to %s).\n", inboundCheckTimeout)

	env, err := ipc.Request(ipc.TypeAwaitInbound, nil, inboundCheckTimeout)
	if err != nil {
		explainNoInbound(err)
		return
	}
	var ack ipc.Ack
	if err := env.Into(&ack); err != nil {
		explainNoInbound(err)
		return
	}
	if !ack.OK {
		explainNoInbound(errors.New(ack.Err))
		return
	}
	fmt.Println("message received - Feishu can reach this computer")
}

// explainNoInbound names what to switch on in the Feishu console, rather
// than reporting that something did not work.
func explainNoInbound(err error) {
	fmt.Printf("no message reached Wirelark (%v)\n", err)
	fmt.Println("In the Feishu developer console, check that the app has:")
	fmt.Println("  - Event subscription set to long connection (not a webhook URL)")
	fmt.Println("  - The event  im.message.receive_v1  subscribed")
	fmt.Println("  - Card callbacks set to long connection, for the Allow and Deny buttons")
	fmt.Println("  - The scopes  im:message  and  im:message:send_as_bot")
	fmt.Println("Notifications already work. Re-run  wirelark init  once that is fixed.")
}

// explainLaunch states the one thing the user has to do differently, and
// why it is temporary. Wrapping it in a launcher would make a research
// preview's restriction into a permanent part of the product.
func explainLaunch() {
	fmt.Println("\nOne more thing. Claude Code channels are a research preview, and a")
	fmt.Println("channel that is not on Anthropic's allowlist has to be opted in per")
	fmt.Println("session. Until Wirelark is on that list, start sessions you want to")
	fmt.Println("continue from Feishu with:")
	fmt.Println("\n  claude --dangerously-load-development-channels server:" + channel.ServerName)
	fmt.Println("\nSessions started with a plain  claude  still send you notifications;")
	fmt.Println("Feishu will show them as \"Notifications only\".")
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

func prompt(reader *bufio.Reader, label string) string {
	fmt.Printf("%s: ", label)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

// promptChoice asks for a numbered choice and returns the chosen value.
func promptChoice(reader *bufio.Reader, label string, options []string) string {
	fmt.Printf("%s: ", label)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(options) {
		return options[n-1]
	}
	return options[0]
}

func confirm(reader *bufio.Reader, question string) bool {
	fmt.Print(question)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "" || line == "y" || line == "yes"
}

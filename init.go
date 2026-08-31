package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/feishu"
	"github.com/marcelritzschke/wirelark/internal/hook"
	"github.com/marcelritzschke/wirelark/internal/hooksreg"
	"github.com/marcelritzschke/wirelark/internal/notify"
	"github.com/marcelritzschke/wirelark/internal/transcript"
)

const initTimeout = 15 * time.Second

// runInit is interactive and allowed to fail loudly - it is never run by a
// hook. Order matters: validate credentials with a test card before saving
// the config or touching settings.json.
func runInit() error {
	reader := bufio.NewReader(os.Stdin)

	appID := prompt(reader, "Feishu app_id (cli_...)")
	if appID == "" {
		return errors.New("app_id is required")
	}
	appSecret := prompt(reader, "Feishu app_secret (input is visible; run in a private terminal)")
	if appSecret == "" {
		return errors.New("app_secret is required")
	}
	ident := prompt(reader, "Your Feishu user id (ou_... / on_... / user_id) or the email of your Feishu account")
	if ident == "" {
		return errors.New("user id or email is required")
	}

	cfg := &config.Config{AppID: appID, AppSecret: appSecret}
	client, err := feishu.New(cfg)
	if err != nil {
		return err
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
			return fmt.Errorf("resolving email: %w", err)
		}
		cfg.OpenID = openID
		fmt.Printf("resolved open_id: %s\n", openID)
	}

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

	// The test card is a real completion card, so setup exercises the exact
	// path a finished turn takes.
	cwd, _ := os.Getwd()
	testTurn := &transcript.Turn{Start: time.Now().Add(-42 * time.Second)}
	testCard, err := notify.CompletionCard(&hook.Payload{
		HookEventName:        hook.EventStop,
		Cwd:                  cwd,
		LastAssistantMessage: "Wirelark is connected. You will get a message here when Claude finishes, hits a problem, or needs a decision from you.",
	}, testTurn, detailOf(cfg))
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

	if !confirm(reader, "Register hooks in ~/.claude/settings.json? [Y/n] ") {
		fmt.Println("skipped hook registration")
		return nil
	}

	cmd, err := executableCommand()
	if err != nil {
		return err
	}
	settingsPath, err := hooksreg.SettingsPath()
	if err != nil {
		return err
	}
	changed, err := hooksreg.Register(settingsPath, cmd, cfg.ProgressEnabled())
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

// executableCommand returns the absolute path of this binary plus "send",
// ready to be written into settings.json.
func executableCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
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

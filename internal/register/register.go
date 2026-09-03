// Package register creates the Feishu app Claude Companion talks through,
// by showing a QR code and letting the person who scans it approve what
// Claude Companion is asking for.
//
// The alternative it replaces is a developer console: create a self-built
// app, tick eight boxes across four pages, publish a version, then copy an
// id and a secret into a terminal. That is a developer's task, and Claude
// Companion is not a developer tool. The registration flow moves the same
// decisions to a permission sheet on the user's phone, where they are
// stated in the platform's own words and answered with one tap.
//
// It is Feishu's own device-authorization flow (RFC 8628), through the
// official SDK, so what the user approves is a page Feishu wrote and the
// credentials are minted by Feishu rather than handled by anything here.
package register

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
)

// Requirements is everything Claude Companion needs a Feishu app to grant, in the
// platform's own vocabulary.
//
// One list, used twice: the registration asks for exactly this, and the
// instructions printed when a hand-made app turns out not to work name
// exactly this. They cannot drift apart, which matters because the
// symptom of a missing subscription is silence rather than an error.
type Requirements struct {
	// Scopes are app-identity permissions. Between them these two cover
	// sending a card, updating one, and reading the direct messages the
	// user sends back.
	Scopes []string
	// Events are the subscriptions that open the return path. Without
	// this one Claude Companion can talk but never listen.
	Events []string
	// Callbacks are what make a card's buttons work. Cards send fine
	// without it, which is why it is the easiest thing to leave out by
	// hand and the easiest to forget when nothing appears to be wrong.
	Callbacks []string
}

// Needs returns what Claude Companion asks a Feishu app for.
func Needs() Requirements {
	return Requirements{
		Scopes:    []string{"im:message", "im:message:send_as_bot"},
		Events:    []string{"im.message.receive_v1"},
		Callbacks: []string{"card.action.trigger"},
	}
}

// Result is a completed registration: the credentials Claude Companion will use,
// and the account that approved them.
type Result struct {
	AppID     string
	AppSecret string
	// OwnerOpenID identifies the person who scanned. They approved the
	// app, so they are the one account Claude Companion answers to - which is the
	// question the old setup had to ask for as an email address.
	OwnerOpenID string
	// Brand is the deployment the registration happened in. Feishu
	// reports it during the flow, so it is observed rather than asked.
	Brand config.Brand
}

// appName and appDescription pre-fill the creation page. "{user}" is a
// placeholder Feishu resolves to the scanning user's name, so the app
// reads as theirs in a list of apps rather than as an anonymous entry.
//
// The description is kept short deliberately. Everything here is encoded
// into the URL the QR code carries, and that URL's length is what decides
// how many modules the symbol needs - which is to say how much of the
// terminal it covers. The wordier description this replaced cost two
// whole QR versions, eight columns and four rows, for text nobody reads
// twice.
//
// At the time of writing this lands the URL on 321 bytes, one under the
// boundary, and so on a 69-column symbol. That is a size, not a
// guarantee: Feishu decides most of the URL, and a byte more from its end
// puts the symbol back up a version. Nothing breaks if it does.
const (
	appName        = "Claude Companion"
	appDescription = "Claude Code on your phone"
)

// source identifies Claude Companion in Feishu's own registration telemetry.
const source = "claude-companion"

// Events are the moments a caller may want to show. Registration is a
// wait with two visible states - here is the code, and it has not been
// scanned yet - and this package reports them rather than drawing them,
// so what the user sees is the front end's decision.
//
// Both are optional and are called from Run's goroutine.
type Events struct {
	// OnQRCode fires once, with the URL to encode and how long it lives.
	OnQRCode func(url string, expiresIn time.Duration)
	// OnPoll fires each time Feishu is asked whether the code has been
	// scanned yet. It carries nothing: that a poll happened is the only
	// part of it that means anything to a person waiting.
	OnPoll func()
}

// Run blocks until the code is scanned and approved, the code expires, or
// ctx ends.
func Run(ctx context.Context, ev Events) (*Result, error) {
	needs := Needs()
	var result *registration.RegisterAppResult

	// The SDK writes a stray progress line to os.Stdout in the middle of
	// this call; see quietly. Everything Claude Companion itself prints goes to
	// out, which is captured before the swap and so is unaffected.
	err := quietly(func() error {
		var err error
		result, err = registration.RegisterApp(ctx, &registration.Options{
			Source: source,
			AppPreset: &registration.AppPreset{
				Name: appName,
				Desc: appDescription,
			},
			Addons: &registration.AppAddons{
				Scopes: registration.AppAddonsScopes{Tenant: needs.Scopes},
				Events: registration.AppAddonsEvents{
					Items: registration.AppAddonsEventItems{Tenant: needs.Events},
				},
				Callbacks: registration.AppAddonsCallbacks{Items: needs.Callbacks},
			},
			OnQRCode: func(info *registration.QRCodeInfo) {
				if ev.OnQRCode != nil {
					ev.OnQRCode(info.URL, expiry(info.ExpireIn))
				}
			},
			OnStatusChange: func(*registration.StatusChangeInfo) {
				// Which status it was is the SDK's business. To someone
				// waiting on their phone, the only meaning is that the
				// wait is still alive.
				if ev.OnPoll != nil {
					ev.OnPoll()
				}
			},
		})
		return err
	})
	if err != nil {
		return nil, explain(err)
	}
	if result.ClientID == "" || result.ClientSecret == "" {
		return nil, errors.New("feishu approved the registration but returned no credentials")
	}

	res := &Result{
		AppID:     result.ClientID,
		AppSecret: result.ClientSecret,
		Brand:     config.BrandFeishu,
	}
	if result.UserInfo != nil {
		res.OwnerOpenID = result.UserInfo.OpenID
		if result.UserInfo.TenantBrand == string(config.BrandLark) {
			res.Brand = config.BrandLark
		}
	}
	return res, nil
}

// explain turns a registration failure into the thing the user can do
// about it. Every one of these ends somewhere the user has to act, so
// none of them may end at a bare error code.
func explain(err error) error {
	var denied *registration.AccessDeniedError
	if errors.As(err, &denied) {
		return errors.New("the registration was declined in Feishu.\n" +
			"If you did not decline it yourself, your Feishu administrator may not\n" +
			"allow members to create apps. Ask them to create one for Claude\n" +
			"Companion and re-run  claude-companion init  with the existing-app option")
	}
	var expired *registration.ExpiredError
	if errors.As(err, &expired) {
		return errors.New("the QR code expired before it was scanned; re-run  claude-companion init  for a new one")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.New("setup stopped waiting for the scan; re-run  claude-companion init  for a new code")
	}
	var regErr *registration.RegisterAppError
	if errors.As(err, &regErr) {
		return fmt.Errorf("feishu refused the registration: %s (%s)", regErr.Description, regErr.Code)
	}
	return fmt.Errorf("registration: %w", err)
}

// expiry turns the SDK's seconds into a duration, defaulting to the ten
// minutes Feishu issues when it does not say.
func expiry(seconds int) time.Duration {
	if seconds <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}

// sdkChatter is a debugging line the SDK's registration package prints to
// os.Stdout on its own, mid-flow. See quietly.
const sdkChatter = "tenant brand: "

// quietly runs fn with os.Stdout replaced, dropping the SDK's stray
// progress line and passing everything else through unchanged.
//
// The line is upstream's, printed by scene/registration with no way to
// silence it - the package takes no logger. It lands in the middle of the
// one screen this whole feature exists to make clean, so it is filtered
// here rather than lived with. If a later SDK stops printing it, this
// keeps working and the constant simply stops matching anything.
//
// Swapping os.Stdout is process-wide, and this runs while setup is
// drawing. That is safe only because the terminal front end holds the
// real handle and does not write through os.Stdout - it must not start,
// or its frames will be buffered here until one happens to contain a
// newline. Forwarded lines end in CRLF because the terminal is in raw
// mode by the time any of them could appear.
func quietly(fn func() error) error {
	saved := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return fn() // a stray line is better than no registration
	}
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		defer close(done)
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), sdkChatter) {
				continue
			}
			fmt.Fprint(saved, scan.Text()+"\r\n")
		}
	}()

	defer func() {
		os.Stdout = saved
		_ = w.Close()
		<-done
		_ = r.Close()
	}()
	return fn()
}

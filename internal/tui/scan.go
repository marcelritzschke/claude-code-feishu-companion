package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/marcelritzschke/wirelark/internal/register"
	"github.com/marcelritzschke/wirelark/internal/termqr"
)

// Outcome is how the scan screen ended.
type Outcome int

const (
	// Scanned means the registration completed and a Result came back.
	Scanned Outcome = iota
	// Manual means the user asked for the existing-app path instead.
	Manual
	// Cancelled means they quit.
	Cancelled
	// Failed means Feishu ended the registration - declined, expired, or
	// unreachable. It is separated from Cancelled because there is
	// somewhere useful to go from here and nowhere to go from a quit.
	Failed
)

// frameRate is how often the waiting line is redrawn. Fast enough to look
// alive, slow enough that a terminal over ssh is not being asked to
// repaint for nothing.
const frameRate = 120 * time.Millisecond

// spinnerFrames is a braille spinner, the one piece of motion on the
// screen. Everything above it is static: the QR code is drawn once and
// never redrawn, so nothing can make it flicker while a phone camera is
// pointed at it.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Scan shows the QR code and waits, live: the code appears as soon as
// Feishu issues it, a countdown runs against its expiry, and the whole
// screen is replaced when it resolves.
//
// The escape hatch is a keypress rather than a question asked first. Most
// people have no Feishu app and no reason to be asked whether they do;
// the few who do can see the offer while the code is on screen and take
// it without having answered anything.
func Scan(ctx context.Context) (*register.Result, Outcome, error) {
	in, err := interactive()
	if err != nil {
		return nil, Cancelled, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type done struct {
		res *register.Result
		err error
	}
	qrs := make(chan qrInfo, 1)
	fin := make(chan done, 1)
	go func() {
		res, err := register.Run(ctx, register.Events{
			OnQRCode: func(url string, expiresIn time.Duration) {
				qrs <- qrInfo{url: url, deadline: time.Now().Add(expiresIn)}
			},
		})
		fin <- done{res, err}
	}()

	emit(hideCursor)
	defer emit(showCursor)

	var (
		d      redrawer
		info   *qrInfo
		frame  int
		ticker = time.NewTicker(frameRate)
	)
	defer ticker.Stop()
	// Only the footer is ever taken back. The code above it stays as a
	// record of what happened, and setup prints its result underneath.
	defer d.clear()

	// The code and the link are printed once and then left alone; only
	// the two lines under them are redrawn.
	//
	// Redrawing the whole screen instead would be tidier - the code could
	// be erased on success - and it does not work: this block is around
	// forty rows, taller than plenty of terminals, and once printing it
	// has scrolled the window there is no way to rewind above the top of
	// the screen to repaint it. A footer is always two lines, and two
	// lines always fit.
	out("\n%s%s\n\n", indent, styles().step.Render("Connect Wirelark to Feishu"))
	for {
		select {
		case got := <-qrs:
			info = &got
			d.clear()
			emit(info.body())

		case res := <-fin:
			switch {
			case res.err != nil:
				return nil, Failed, res.err
			case res.res == nil:
				return nil, Failed, errors.New("registration returned nothing")
			}
			return res.res, Scanned, nil

		case k, ok := <-in.keys:
			if !ok || k.kind == keyInterrupt || (k.kind == keyRune && k.r == 'q') {
				return nil, Cancelled, ErrAborted
			}
			if k.kind == keyRune && (k.r == 'e' || k.r == 'E') {
				return nil, Manual, nil
			}

		case <-ticker.C:
			frame++
		}

		spin := styles().key.Render(spinnerFrames[frame%len(spinnerFrames)])
		if info == nil {
			d.draw(indent + spin + " " + styles().muted.Render("asking Feishu for a code…") + "\n")
		} else {
			d.draw(footer(spin, info.deadline))
		}
	}
}

type qrInfo struct {
	url      string
	deadline time.Time
	cached   string
}

func waitingForCode(spin string) string {
	return "\n" + indent + styles().step.Render("Connect Wirelark to Feishu") + "\n\n" +
		indent + spin + " " + styles().muted.Render("asking Feishu for a code…") + "\n"
}

// body is everything that never changes once the code arrives. It is
// rendered once and cached, because it is the expensive part of the
// frame and re-encoding a QR code ten times a second to get the same
// bytes back would be silly.
func (q *qrInfo) body() string {
	if q.cached != "" {
		return q.cached
	}
	var b strings.Builder
	b.WriteString(qrBlock(q.url))
	b.WriteString(indent + "Scan with Feishu, then approve what Wirelark asks for.\n")
	b.WriteString(indent + styles().muted.Render("The account you scan with becomes this computer's Wirelark owner.") + "\n\n")
	b.WriteString(indent + styles().muted.Render("Can't scan? ") + styles().link.Render(q.url) + "\n\n")
	q.cached = b.String()
	return q.cached
}

// footer is the two lines that move: what we are waiting for, and the way
// out of waiting for it.
func footer(spin string, deadline time.Time) string {
	return indent + spin + " " + waitLine(deadline) + "\n" +
		indent + hint("e", "use an existing Feishu app") + "     " + hint("ctrl+c", "cancel") + "\n"
}

// qrBlock is the code itself, or the reason it is not being drawn. A
// wrapped QR code is not a smaller QR code, it is a picture of nothing,
// and someone staring at one has no way to know that is what happened -
// so below the width it needs, the link carries the flow instead.
func qrBlock(url string) string {
	code, err := termqr.Encode(url)
	if err != nil {
		return indent + styles().warn.Render("!") + " could not draw a QR code; use the link below\n\n"
	}
	if width := screenWidth(); width > 0 && width < code.Width()+len(indent) {
		return indent + styles().warn.Render("!") +
			fmt.Sprintf(" this window is %d columns and a scannable code needs %d,\n", width, code.Width()+len(indent)) +
			indent + styles().muted.Render("  so use the link below, or widen the window and start again") + "\n\n"
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(code.String(useColor()), "\n"), "\n") {
		b.WriteString(indent + line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// waitLine is the countdown. It is stated as time left rather than as a
// deadline because the only question it answers is whether there is still
// time to find your phone.
func waitLine(deadline time.Time) string {
	left := time.Until(deadline).Round(time.Second)
	if left <= 0 {
		return styles().muted.Render("the code has expired")
	}
	return "Waiting for the scan " + styles().muted.Render("· "+compactDuration(left)+" left")
}

func hint(k, what string) string {
	return styles().key.Render(k) + " " + styles().muted.Render(what)
}

// compactDuration renders a countdown the way a clock would.
func compactDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// useColor reports whether the QR may state its own colours. It draws
// black-on-white explicitly so that it scans on a light terminal as well
// as a dark one; NO_COLOR is the one convention that overrides that, and
// on such a terminal the code still scans if the background is dark.
func useColor() bool {
	_, set := os.LookupEnv("NO_COLOR")
	return !set
}

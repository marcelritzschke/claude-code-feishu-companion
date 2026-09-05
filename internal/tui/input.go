package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// keyKind is what a keystroke meant, once the escape sequences are out of
// the way.
type keyKind int

const (
	keyRune keyKind = iota
	keyUp
	keyDown
	keyEnter
	keyBackspace
	// keyInterrupt is ctrl+c: the one answer every question accepts.
	keyInterrupt
)

type key struct {
	kind keyKind
	r    rune
}

// input owns the terminal for the length of setup.
//
// One reader, started once, for every question setup asks. The obvious
// alternative - each prompt reading stdin for itself - has each of them
// leaving a blocked read behind when it finishes, and the next prompt
// then loses its first keystroke to the previous prompt's ghost. Reading
// in one place is what makes that impossible rather than unlikely.
type input struct {
	keys  chan key
	fd    int
	state *term.State
}

var (
	inputOnce sync.Once
	inputErr  error
	shared    *input
)

// Interactive reports whether there is a terminal to ask questions in. A
// command that can go on without asking - by being told to assume the
// answer - checks this first, so it can say which flag to pass rather than
// failing on a question it could not put.
func Interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// interactive returns the terminal session, opening it on first use.
//
// Raw mode stays on from the first question to the end of setup, so that
// arrow keys arrive as arrow keys rather than after an Enter. That is why
// everything this package prints ends in \r\n: with the line discipline
// off, a bare newline drops a line without returning to the left margin.
func interactive() (*input, error) {
	inputOnce.Do(func() {
		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			inputErr = fmt.Errorf("claude-companion needs a terminal to ask questions in")
			return
		}
		state, err := term.MakeRaw(fd)
		if err != nil {
			inputErr = fmt.Errorf("put terminal in raw mode: %w", err)
			return
		}
		in := &input{keys: make(chan key, 16), fd: fd, state: state}
		go in.read()
		shared = in
	})
	return shared, inputErr
}

// Close gives the terminal back. Setup defers it; nothing else needs to
// care, because a program that never asked a question never took it.
func Close() {
	if shared != nil && shared.state != nil {
		_ = term.Restore(shared.fd, shared.state)
		shared.state = nil
		emit("\n")
	}
}

// read decodes keystrokes until stdin ends. It is deliberately the only
// goroutine that ever touches stdin.
func (in *input) read() {
	r := bufio.NewReader(os.Stdin)
	for {
		b, err := r.ReadByte()
		if err != nil {
			close(in.keys)
			return
		}
		switch b {
		case 3: // ctrl+c
			in.keys <- key{kind: keyInterrupt}
		case '\r', '\n':
			in.keys <- key{kind: keyEnter}
		case 127, 8:
			in.keys <- key{kind: keyBackspace}
		case 27: // an escape sequence, or a lone escape we ignore
			if r.Buffered() < 2 {
				continue
			}
			if next, _ := r.ReadByte(); next != '[' && next != 'O' {
				continue
			}
			final, _ := r.ReadByte()
			switch final {
			case 'A':
				in.keys <- key{kind: keyUp}
			case 'B':
				in.keys <- key{kind: keyDown}
			}
		default:
			if b >= 32 {
				in.keys <- key{kind: keyRune, r: rune(b)}
			}
		}
	}
}

// next blocks for one keystroke.
func (in *input) next() (key, bool) {
	k, ok := <-in.keys
	return k, ok
}

// Terminal-control sequences. These are the whole vocabulary needed to
// redraw a prompt in place.
const (
	cursorUp   = "\x1b[%dA"
	clearBelow = "\x1b[0J"
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

// redrawer rewrites a block of lines in place, so a moving selection or a
// running countdown replaces itself instead of scrolling the screen.
//
// Two rules keep it honest, and both were learned the hard way:
//
// It never prints a newline after its last line. A newline on the bottom
// row of a terminal scrolls the whole screen; the next redraw then rewinds
// by the number of lines it wrote, lands one row too high because
// everything moved, and prints its block again slightly lower. That is not
// a cosmetic bug - it walks the screen, one frame at a time, until the
// thing you were meant to be scanning has scrolled away.
//
// It repaints only the lines that changed. A frame here can be forty lines
// of QR code with a spinner underneath; repainting all of it ten times a
// second is a visible flicker over exactly the image a phone camera is
// trying to hold focus on. Comparing against the previous frame means the
// code is written once and only the two lines below it are ever rewritten.
type redrawer struct {
	// prev is what is currently on screen, one entry per logical line.
	prev []string
}

// draw replaces the previously drawn block with s. A trailing newline is
// ignored, because the invariant is that the cursor rests at the end of
// the last line rather than below it.
func (d *redrawer) draw(s string) {
	next := splitFrame(s)
	if len(next) == 0 {
		d.clear()
		return
	}

	common := 0
	for common < len(d.prev) && common < len(next) && d.prev[common] == next[common] {
		common++
	}
	if common == len(d.prev) && common == len(next) {
		return // nothing moved
	}

	// Repaint from the last line the two frames still share, rather than
	// from the first that differs. One extra line is rewritten, and in
	// exchange the cursor always lands on a row that already exists -
	// which a block that has grown since the last frame does not.
	if common > len(next)-1 {
		common = len(next) - 1
	}
	if len(d.prev) > 0 && common > len(d.prev)-1 {
		common = len(d.prev) - 1
	}
	if common < 0 {
		common = 0
	}

	var b strings.Builder
	b.WriteString(rewind(d.prev, common))
	b.WriteString(clearBelow)
	for i, line := range next[common:] {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(line)
	}
	// One write, so a frame never reaches the terminal half-drawn.
	emit(b.String())
	d.prev = next
}

// clear removes the block entirely.
func (d *redrawer) clear() {
	if len(d.prev) == 0 {
		return
	}
	emit(rewind(d.prev, 0) + clearBelow)
	d.prev = nil
}

// finish leaves the block on screen and moves below it, so whatever setup
// prints next starts on a line of its own.
func (d *redrawer) finish() {
	if len(d.prev) > 0 {
		emit("\n")
	}
	d.prev = nil
}

// rewind returns the sequence that moves the cursor from the end of the
// last drawn line to the start of logical line n.
func rewind(prev []string, n int) string {
	if len(prev) == 0 {
		return ""
	}
	up := rows(prev) - 1 - rows(prev[:n])
	if up <= 0 {
		return "\r"
	}
	return "\r" + fmt.Sprintf(cursorUp, up)
}

// rows is how many physical terminal rows a set of logical lines occupies.
// A line wider than the window wraps onto further rows, and a redraw that
// did not count them would rewind into the middle of its own output.
func rows(lines []string) int {
	width := screenWidth()
	total := 0
	for _, line := range lines {
		n := 1
		if width > 0 {
			if w := lipgloss.Width(line); w > width {
				n = (w + width - 1) / width
			}
		}
		total += n
	}
	return total
}

// screen is the terminal this package draws on, captured the first time
// anything is printed.
//
// It is held as a file rather than read from os.Stdout at each write
// because os.Stdout does not stay put: the Feishu SDK prints a stray
// debug line mid-registration, and the only way to suppress it is to
// point os.Stdout at a pipe for the duration - which is exactly the
// window in which this package is animating a screen. Writing through
// that pipe meant frames were held until they contained a newline, and
// frames here deliberately never end in one. Owning the real handle keeps
// the two entirely separate.
// It is a plain lazy variable rather than a synchronised one because
// exactly one goroutine ever draws: the input reader only reads.
var screenFile *os.File

func screen() *os.File {
	if screenFile == nil {
		screenFile = os.Stdout
	}
	return screenFile
}

// emit is the one way this package writes to the terminal.
//
// Every write goes through here for a second reason too: with the line
// discipline off, a bare newline moves down a row without returning to
// the left margin, so one unconverted \n anywhere sends every line after
// it marching diagonally across the screen. One converter makes that
// impossible rather than a thing to remember at each call site.
func emit(s string) {
	_, _ = screen().WriteString(crlf(s))
}

// crlf turns the newlines in rendered text into the carriage-return pairs
// a raw-mode terminal needs.
func crlf(s string) string {
	out := make([]byte, 0, len(s)+16)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// splitFrame turns rendered text into logical lines, dropping the
// trailing newline callers naturally write.
func splitFrame(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

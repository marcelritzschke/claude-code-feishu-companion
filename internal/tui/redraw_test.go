package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// tinyVT is just enough terminal to check the arithmetic: a cursor, a
// grid, and the four control sequences the redrawer emits. It exists
// because the bug it guards against is invisible in the byte stream -
// every frame looks correct on its own, and only a terminal applying them
// in order shows the block marching down the screen.
type tinyVT struct {
	lines []string
	row   int // cursor row, an index into lines
	col   int
	rows  int // visible height; writing past it scrolls
}

func newVT(height int) *tinyVT {
	return &tinyVT{lines: []string{""}, rows: height}
}

func (v *tinyVT) write(raw string) {
	// Over runes, not bytes: everything this UI draws - box glyphs, the
	// selection caret, the spinner - is multi-byte, and a byte-wise
	// terminal would report corruption that is its own.
	s := []rune(raw)
	at := func(i int) string { return string(s[i:]) }
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(at(i), "\x1b[0J"):
			// Clear from the cursor to the end of the screen.
			v.lines[v.row] = truncate(v.lines[v.row], v.col)
			v.lines = v.lines[:v.row+1]
			i += 4
		case strings.HasPrefix(at(i), "\x1b[?25l"), strings.HasPrefix(at(i), "\x1b[?25h"):
			i += 6
		case strings.HasPrefix(at(i), "\x1b["):
			j := i + 2
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j < len(s) && s[j] == 'A' {
				var n int
				fmt.Sscanf(string(s[i+2:j]), "%d", &n)
				v.row -= n
				if v.row < 0 {
					v.row = 0
				}
			}
			i = j + 1
		case s[i] == '\r':
			v.col = 0
			i++
		case s[i] == '\n':
			v.row++
			v.col = 0
			for len(v.lines) <= v.row {
				v.lines = append(v.lines, "")
			}
			i++
		default:
			for len(v.lines) <= v.row {
				v.lines = append(v.lines, "")
			}
			line := v.lines[v.row]
			for runeLen(line) < v.col {
				line += " "
			}
			v.lines[v.row] = truncate(line, v.col) + string(s[i])
			v.col++
			i++
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func runeLen(s string) int { return len([]rune(s)) }

func (v *tinyVT) screen() []string {
	out := make([]string, 0, len(v.lines))
	for _, l := range v.lines {
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

// capture runs fn with stdout redirected, and feeds what it wrote into a
// terminal.
func capture(t *testing.T, fn func()) *tinyVT {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := screenFile
	screenFile = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	screenFile = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()

	vt := newVT(50)
	vt.write(out)
	return vt
}

// TestRedrawStaysPut is the regression test for the bug that made setup
// unusable: an animated block that walked down the screen, one frame per
// tick, until the QR code it was under had scrolled out of sight.
func TestRedrawStaysPut(t *testing.T) {
	const frames = 60
	vt := capture(t, func() {
		var d redrawer
		for i := 0; i < frames; i++ {
			d.draw(fmt.Sprintf("static line one\nstatic line two\nspinner %d\n", i))
		}
	})

	screen := vt.screen()
	if len(screen) != 3 {
		t.Fatalf("after %d frames the block occupies %d rows, want 3:\n%s",
			frames, len(screen), strings.Join(screen, "\n"))
	}
	if got := countOccurrences(screen, "spinner"); got != 1 {
		t.Errorf("the moving line appears %d times on screen, want 1", got)
	}
	if screen[2] != fmt.Sprintf("spinner %d", frames-1) {
		t.Errorf("last row = %q, want the final frame", screen[2])
	}
}

// TestRedrawOnlyRepaintsWhatMoved is the flicker guard. Rewriting forty
// lines of QR code ten times a second is visible, and it is visible over
// precisely the image a camera is trying to focus on.
func TestRedrawOnlyRepaintsWhatMoved(t *testing.T) {
	static := strings.Repeat("an unchanging line of qr code\n", 30)

	var d redrawer
	// The first frame goes nowhere; only the second is measured.
	writtenBytes(t, func() { d.draw(static + "spinner a\n") })
	before := writtenBytes(t, func() { d.draw(static + "spinner b\n") })

	if strings.Contains(before, "unchanging") {
		t.Errorf("a redraw repainted the static block; it wrote %d bytes:\n%q", len(before), before)
	}
	if !strings.Contains(before, "spinner b") {
		t.Errorf("the redraw did not write the line that changed: %q", before)
	}
}

// TestRedrawGrowsAndShrinks covers a block that changes height, which is
// what an input field does when a validation message appears under it.
func TestRedrawGrowsAndShrinks(t *testing.T) {
	vt := capture(t, func() {
		var d redrawer
		d.draw("title\n› typed\n")
		d.draw("title\n› typed\nthis one is required\n")
		d.draw("title\n› typed more\n")
	})
	screen := vt.screen()
	if len(screen) != 2 {
		t.Fatalf("block is %d rows, want 2 after shrinking:\n%s", len(screen), strings.Join(screen, "\n"))
	}
	if countOccurrences(screen, "required") != 0 {
		t.Errorf("the validation message survived the shrink:\n%s", strings.Join(screen, "\n"))
	}
	if screen[1] != "› typed more" {
		t.Errorf("last row = %q", screen[1])
	}
}

// TestClearRemovesTheBlock checks that the scan screen can get out of the
// way when the code has been scanned.
func TestClearRemovesTheBlock(t *testing.T) {
	vt := capture(t, func() {
		var d redrawer
		d.draw("line one\nline two\nline three\n")
		d.clear()
	})
	for _, l := range vt.screen() {
		if strings.TrimSpace(l) != "" {
			t.Errorf("clear left %q on screen", l)
		}
	}
}

func writtenBytes(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	saved := screenFile
	screenFile = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	screenFile = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func countOccurrences(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// TestFramesNeverEmitABareNewline is the guard for the failure that made
// setup unusable. In raw mode a newline without a carriage return moves
// down a row but not back to the margin, so a single one anywhere walks
// every later line sideways across the screen.
func TestFramesNeverEmitABareNewline(t *testing.T) {
	written := writtenBytes(t, func() {
		var d redrawer
		d.draw("first\nsecond\n")
		d.draw("first\nchanged\n")
		d.draw("first\nchanged\nthird\n")
		d.draw("first\n")
		d.finish()
		out("a plain line\n")
		Done("a result line")
		Detail("a detail\nover two lines")
	})
	for i := 0; i < len(written); i++ {
		if written[i] == '\n' && (i == 0 || written[i-1] != '\r') {
			t.Fatalf("a bare newline was written at byte %d: %q", i, written[max(0, i-40):i+5])
		}
	}
}

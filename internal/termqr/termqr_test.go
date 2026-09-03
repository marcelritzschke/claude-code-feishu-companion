package termqr

import (
	"bytes"
	"strings"
	"testing"

	"rsc.io/qr"
)

// registrationURL is the shape of URL this package exists to draw: a real
// Feishu app-registration link, with the gzipped addons payload that makes
// it long. If a change pushes this past a terminal's width the feature is
// broken, so its size is asserted rather than assumed.
const registrationURL = "https://open.feishu.cn/page/launcher?addons=H4sIAAAAAAAA_2TLQQrDIBCF4bu8dRC69SolhMnkEaRRizO4Ee9eCl0Euvzh-wdUrmsXfRniQHJmQ3xCpR1B1FMtwVs6TzascwE7i9_pgLNI8e-Ucsg0k5OhUZk6t_7AOucC0_rmH48_juUW0ViOTWzbq2Od8zMAZr3Wk6MAAAA&createOnly=true&desc=Claude+Code+notifications+and+session+control+for+%7Buser%7D&from=sdk&name=Claude+Companion&source=go-sdk%2Fclaude-companion&tp=sdk&user_code=UKS9-4VQY"

func TestRegistrationURLFitsAnEightyColumnTerminal(t *testing.T) {
	c, err := Encode(registrationURL)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := c.Width(); got > 80 {
		t.Errorf("width = %d columns, want <= 80 so the code does not wrap", got)
	}
}

// TestRenderMatchesTheModuleGrid checks the one thing this package
// actually decides: which glyph stands for which pair of modules. A wrong
// mapping still draws something QR-shaped, so comparing against the
// encoder's own grid is the only check that would catch it.
func TestRenderMatchesTheModuleGrid(t *testing.T) {
	const text = "https://example.com/claude-companion"
	c, err := Encode(text)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var buf bytes.Buffer
	if err := c.Render(&buf, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")

	raw, err := qr.Encode(text, qr.L)
	if err != nil {
		t.Fatalf("qr.Encode: %v", err)
	}
	width := raw.Size + 2*quietZone

	if want := (width + 1) / 2; len(lines) != want {
		t.Fatalf("got %d lines, want %d", len(lines), want)
	}
	for row, line := range lines {
		glyphs := []rune(line)
		if len(glyphs) != width {
			t.Fatalf("line %d is %d columns, want %d", row, len(glyphs), width)
		}
		for col, got := range glyphs {
			x, y := col-quietZone, row*2-quietZone
			want := glyph(black(raw, x, y), black(raw, x, y+1))
			if got != want {
				t.Fatalf("glyph at row %d col %d = %q, want %q", row, col, got, want)
			}
		}
	}
}

// TestQuietZoneIsLight guards the margin scanners look for: the outermost
// rows and columns must be unbroken light, or the symbol has no edge.
func TestQuietZoneIsLight(t *testing.T) {
	c, err := Encode("https://example.com/claude-companion")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var buf bytes.Buffer
	if err := c.Render(&buf, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")

	// A half-block line covers two module rows, so the first quietZone/2
	// lines are wholly inside the top margin.
	for _, row := range []int{0, 1} {
		if want := strings.Repeat(string(bothLight), c.Width()); lines[row] != want {
			t.Errorf("line %d is not an unbroken light margin: %q", row, lines[row])
		}
	}
	for row, line := range lines {
		glyphs := []rune(line)
		for _, col := range []int{0, 1, 2, 3, c.Width() - 4, c.Width() - 3, c.Width() - 2, c.Width() - 1} {
			if glyphs[col] != bothLight {
				t.Errorf("row %d col %d is inside the side margin but not light: %q", row, col, glyphs[col])
			}
		}
	}
}

func TestRenderColorWrapsEveryLine(t *testing.T) {
	c, err := Encode("https://example.com/claude-companion")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var buf bytes.Buffer
	if err := c.Render(&buf, true); err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, colorOn) || !strings.HasSuffix(line, colorOff) {
			t.Fatalf("line %d is not wrapped in the explicit colours: %q", i, line)
		}
	}
}

func black(c *qr.Code, x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.Black(x, y)
}

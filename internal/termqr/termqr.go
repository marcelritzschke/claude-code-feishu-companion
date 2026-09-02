// Package termqr draws a QR code into a terminal, small enough to fit an
// 80-column window and oriented so a phone camera can read it whatever
// colour the terminal happens to be.
package termqr

import (
	"fmt"
	"io"
	"strings"

	"rsc.io/qr"
)

// quietZone is the light margin the QR specification requires around the
// symbol. Scanners use it to find the edge; without it a code drawn
// flush against surrounding text often will not read at all.
const quietZone = 4

// Half-block glyphs. One character carries two vertically stacked modules,
// which is what keeps a 69-module symbol inside an 80-column terminal: the
// alternative, two spaces per module, is twice as wide as any terminal.
//
// The foreground draws the light modules and the background the dark ones,
// so a light module is "ink" as far as the terminal is concerned.
const (
	bothLight = '█' // full block
	topLight  = '▀' // upper half block
	botLight  = '▄' // lower half block
	bothDark  = ' '
)

// colored wraps a line in bright white on black. A QR code is only
// readable the right way round - dark modules dark, light modules light -
// and a terminal's own colours are not knowable, so the rendering states
// both explicitly rather than inheriting a scheme that may be inverted.
const (
	colorOn  = "\x1b[97;40m"
	colorOff = "\x1b[0m"
)

// Code is an encoded QR symbol, ready to be drawn.
type Code struct {
	code *qr.Code
}

// Encode builds the symbol for text.
//
// The error correction level is the lowest one: the symbol is displayed on
// a screen a few centimetres from the camera rather than printed on
// something that can be smudged or torn, and every level above L makes the
// symbol wider, which is the one thing that can genuinely break it.
func Encode(text string) (*Code, error) {
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, fmt.Errorf("encode qr code: %w", err)
	}
	return &Code{code: c}, nil
}

// Width is how many terminal columns the drawing needs. Callers compare it
// with the real terminal width, because a QR code that wraps is not a
// degraded QR code - it is an unreadable one, and it is better to offer
// the link than to draw it.
func (c *Code) Width() int { return c.code.Size + 2*quietZone }

// Height is how many terminal lines the drawing occupies. Each line
// carries two module rows.
func (c *Code) Height() int { return (c.Width() + 1) / 2 }

// Render draws the code to w. With color false the drawing relies on the
// terminal's own foreground and background, which reads correctly on a
// dark terminal and inverted on a light one; it is the fallback for
// somewhere ANSI colour is unwelcome, not the preferred rendering.
func (c *Code) Render(w io.Writer, color bool) error {
	_, err := io.WriteString(w, c.String(color))
	return err
}

// String is the drawing as text, for a caller composing a screen around
// it rather than writing straight out. Every line ends in a newline.
func (c *Code) String(color bool) string {
	width := c.Width()
	// Rows are consumed in pairs, so an odd count would leave the last
	// row without a partner. Padding with a light row simply makes the
	// bottom margin one module deeper than the top.
	height := width
	if height%2 != 0 {
		height++
	}

	var b strings.Builder
	for y := 0; y < height; y += 2 {
		if color {
			b.WriteString(colorOn)
		}
		for x := 0; x < width; x++ {
			b.WriteRune(glyph(c.dark(x, y), c.dark(x, y+1)))
		}
		if color {
			b.WriteString(colorOff)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// glyph picks the half-block that draws one column of two modules.
func glyph(top, bottom bool) rune {
	switch {
	case !top && !bottom:
		return bothLight
	case !top && bottom:
		return topLight
	case top && !bottom:
		return botLight
	default:
		return bothDark
	}
}

// dark reports whether the module at (x, y) of the drawing - quiet zone
// included - is dark. Everything outside the symbol is light, which is
// what makes the quiet zone a quiet zone.
func (c *Code) dark(x, y int) bool {
	x -= quietZone
	y -= quietZone
	if x < 0 || y < 0 || x >= c.code.Size || y >= c.code.Size {
		return false
	}
	return c.code.Black(x, y)
}

// Package tui is how setup looks and how it asks. It exists so that every
// question Claude Companion puts to a user is asked the same way, and so that the
// setup command's appearance is one decision made in one place rather
// than a print statement per prompt.
//
// Nothing else in Claude Companion draws anything. The hook entrypoint and the
// channel share stdio with a Claude Code session and must stay silent;
// the daemon writes to a log. Setup is the one role with a screen.
package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// The palette is adaptive: lipgloss picks per entry depending on whether
// the terminal is light or dark, so the same setup reads correctly in
// both instead of being tuned for whichever one it was written on.
var (
	accent = lipgloss.AdaptiveColor{Light: "#2B5FD9", Dark: "#7AA2F7"}
	good   = lipgloss.AdaptiveColor{Light: "#1F7A44", Dark: "#7BD88F"}
	warn   = lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E0AF68"}
	bad    = lipgloss.AdaptiveColor{Light: "#B32D2D", Dark: "#F7768E"}
	muted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B93A7"}
	strong = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E6E9F0"}
)

type palette struct {
	title, step, muted, good, warn, bad, key, code, link lipgloss.Style
	selected, cursor                                     lipgloss.Style
}

// styles builds the palette on first use, and never before.
//
// This laziness is load-bearing, not tidiness. All three Claude Companion roles
// are one binary, so this package is linked into the hook entrypoint too
// - and building a lipgloss style constructs lipgloss's default renderer,
// which interrogates the terminal for its colour scheme. As package-level
// variables that happened during startup of every `claude-companion send`: escape
// sequences written into the Claude Code session's terminal, and a
// quarter-second of waiting for a reply, on every single hook event. A
// hook that costs that much and is visible while doing it is the one
// thing this program must never be.
var styles = sync.OnceValue(func() *palette {
	return &palette{
		title: lipgloss.NewStyle().Bold(true).Foreground(accent),
		step:  lipgloss.NewStyle().Bold(true).Foreground(strong),
		muted: lipgloss.NewStyle().Foreground(muted),
		good:  lipgloss.NewStyle().Foreground(good),
		warn:  lipgloss.NewStyle().Foreground(warn),
		bad:   lipgloss.NewStyle().Foreground(bad),
		key:   lipgloss.NewStyle().Bold(true).Foreground(accent),
		// code marks something the user types or pastes, so a command in
		// a sentence is visibly not part of the sentence.
		code: lipgloss.NewStyle().Foreground(accent),
		link: lipgloss.NewStyle().Foreground(accent).Underline(true),
		// selected is the option the cursor is on; cursor is the caret
		// standing where typed text will land.
		selected: lipgloss.NewStyle().Bold(true).Foreground(accent),
		cursor:   lipgloss.NewStyle().Foreground(accent),
	}
})

// indent is the left margin every line of setup shares, so the QR code,
// the questions, and the results all hang off the same edge.
const indent = "  "

// Title prints the banner setup opens with.
func Title(name, tagline string) {
	out("\n%s%s  %s\n\n", indent, styles().title.Render(name), styles().muted.Render(tagline))
}

// Step announces a phase of setup.
func Step(text string) {
	out("%s%s\n", indent, styles().step.Render(text))
}

// Done, Warn, and Fail report an outcome. The marks carry the meaning at
// a glance; the colour only reinforces it, so the transcript still reads
// correctly where colour was stripped.
func Done(format string, a ...any) { mark(styles().good, "✓", format, a...) }
func Warn(format string, a ...any) { mark(styles().warn, "!", format, a...) }
func Fail(format string, a ...any) { mark(styles().bad, "✗", format, a...) }

// Info is a line that reports nothing, it just says what is happening.
func Info(format string, a ...any) {
	out("%s  %s\n", indent, fmt.Sprintf(format, a...))
}

// Detail is secondary text under a result: the reason, the path, the
// thing to go fix. Multi-line text stays aligned under its parent.
func Detail(text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out("%s    %s\n", indent, styles().muted.Render(line))
	}
}

// Blank separates sections.
func Blank() { out("\n") }

// Code renders something the user types, for use inside a sentence.
func Code(s string) string { return styles().code.Render(s) }

func mark(style lipgloss.Style, glyph, format string, a ...any) {
	out("%s%s %s\n", indent, style.Render(glyph), fmt.Sprintf(format, a...))
}

// out writes a line the way a raw-mode terminal needs it. Every print in
// this package goes through here, because the terminal spends setup with
// its line discipline off and a bare newline would stagger the output
// diagonally down the screen.
func out(format string, a ...any) {
	emit(fmt.Sprintf(format, a...))
}

// screenWidth is how many columns there are to draw in, or 0 when that
// cannot be known.
func screenWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

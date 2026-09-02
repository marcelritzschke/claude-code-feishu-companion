package tui

import (
	"errors"
	"strings"
)

// ErrAborted is returned when the user quits a question rather than
// answering it. Setup treats that as "stop", not as a failure to report:
// someone who pressed ctrl+c knows what they did.
var ErrAborted = errors.New("setup cancelled")

// Choice is one option in a Choose.
type Choice[T comparable] struct {
	// Label is the line the user reads.
	Label string
	// Note is the shorter line beside it, saying what the choice means.
	Note string
	// Value is what the caller gets back.
	Value T
}

// Choose asks a single-select question. The first choice is the one the
// cursor starts on, so callers order them with the recommended answer
// first rather than annotating a default in the text.
//
// Arrow keys move, Enter accepts, and a digit picks that option outright
// - the last of those because setup used to be a numbered menu, and
// someone who already knows they want the second option should not have
// to arrow down to it.
func Choose[T comparable](title, description string, choices []Choice[T]) (T, error) {
	var zero T
	in, err := interactive()
	if err != nil {
		return zero, err
	}

	cursor := 0
	var d redrawer
	emit(hideCursor)
	defer emit(showCursor)

	for {
		d.draw(renderChoices(title, description, choices, cursor))

		k, ok := in.next()
		if !ok {
			d.finish()
			return zero, ErrAborted
		}
		switch {
		case k.kind == keyInterrupt:
			d.finish()
			return zero, ErrAborted
		case k.kind == keyUp || (k.kind == keyRune && k.r == 'k'):
			cursor = (cursor - 1 + len(choices)) % len(choices)
		case k.kind == keyDown || (k.kind == keyRune && k.r == 'j'):
			cursor = (cursor + 1) % len(choices)
		case k.kind == keyRune && k.r >= '1' && k.r <= '9':
			if n := int(k.r - '1'); n < len(choices) {
				cursor = n
				d.draw(renderChoices(title, description, choices, cursor))
				d.finish()
				return choices[cursor].Value, nil
			}
		case k.kind == keyEnter:
			d.finish()
			return choices[cursor].Value, nil
		}
	}
}

func renderChoices[T comparable](title, description string, choices []Choice[T], cursor int) string {
	var b strings.Builder
	b.WriteString(indent + styles().step.Render(title) + "\n")
	if description != "" {
		b.WriteString(indent + styles().muted.Render(description) + "\n")
	}
	for i, c := range choices {
		label, marker := c.Label, "  "
		if i == cursor {
			label, marker = styles().selected.Render(c.Label), styles().key.Render("› ")
		}
		line := indent + marker + label
		if c.Note != "" {
			line += "  " + styles().muted.Render("· "+c.Note)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Ask asks for a line of text. required rejects an empty answer rather
// than accepting one and failing later with something less specific.
func Ask(title, description, placeholder string, required bool) (string, error) {
	return readLine(title, description, placeholder, required, false)
}

// AskSecret is Ask for something that should not be left on screen.
func AskSecret(title, description string) (string, error) {
	return readLine(title, description, "", true, true)
}

func readLine(title, description, placeholder string, required, secret bool) (string, error) {
	in, err := interactive()
	if err != nil {
		return "", err
	}

	var typed []rune
	var problem string
	var d redrawer

	for {
		d.draw(renderInput(title, description, placeholder, string(typed), problem, secret))

		k, ok := in.next()
		if !ok {
			d.finish()
			return "", ErrAborted
		}
		switch k.kind {
		case keyInterrupt:
			d.finish()
			return "", ErrAborted
		case keyBackspace:
			if len(typed) > 0 {
				typed = typed[:len(typed)-1]
			}
		case keyRune:
			typed = append(typed, k.r)
		case keyEnter:
			answer := strings.TrimSpace(string(typed))
			if answer == "" && required {
				problem = "this one is required"
				continue
			}
			// Redraw once more so the finished answer, not the cursor,
			// is what stays on screen above the next question.
			d.draw(renderInput(title, description, placeholder, string(typed), "", secret))
			d.finish()
			return answer, nil
		}
		problem = ""
	}
}

func renderInput(title, description, placeholder, typed, problem string, secret bool) string {
	var b strings.Builder
	b.WriteString(indent + styles().step.Render(title) + "\n")
	if description != "" {
		b.WriteString(indent + styles().muted.Render(description) + "\n")
	}

	shown := typed
	if secret {
		shown = strings.Repeat("•", len([]rune(typed)))
	}
	if typed == "" && placeholder != "" {
		shown = styles().muted.Render(placeholder)
	}
	b.WriteString(indent + styles().key.Render("› ") + shown + styles().cursor.Render("▏") + "\n")
	if problem != "" {
		b.WriteString(indent + "  " + styles().bad.Render(problem) + "\n")
	}
	return b.String()
}

// Confirm asks a yes/no question, starting on yes.
func Confirm(title, description string) (bool, error) {
	return Choose(title, description, []Choice[bool]{
		{Label: "Yes", Value: true},
		{Label: "No", Value: false},
	})
}

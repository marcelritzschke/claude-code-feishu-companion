// Package pathdisp renders filesystem paths for a human reading a phone.
//
// It treats "/" and "\" as separators regardless of the OS Claude Companion runs
// on: paths reach Claude Companion from Claude Code payloads and transcripts, so
// the separator is a property of the recorded path, not of this process.
package pathdisp

import (
	"os"
	"strings"
)

// homeDir is a variable so a test can render a path against a home
// directory other than the one it happens to run under.
var homeDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// Base returns the final element of p, or p itself when p is a filesystem
// root or has no separator.
func Base(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	if trimmed == "" {
		return p // p was nothing but separators: a filesystem root
	}
	if i := strings.LastIndexAny(trimmed, `/\`); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// Short renders p relative to dir when p lives inside it, and as the bare
// file name otherwise, so a card shows "auth/session.go" rather than a
// full home-directory path.
func Short(p, dir string) string {
	if rel, ok := within(p, dir); ok {
		return rel
	}
	return Base(p)
}

// Label returns a name for a directory suitable as a project label, and
// reports whether one could be derived. Filesystem roots ("/", "C:\") have
// no meaningful name.
func Label(dir string) (string, bool) {
	base := Base(dir)
	switch base {
	case "", ".", "..", "/", `\`:
		return "", false
	}
	if strings.HasSuffix(base, ":") {
		return "", false // a Windows volume root such as "C:"
	}
	return base, true
}

// within reports whether p sits inside dir, and with what relative path.
func within(p, dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	np := slashed(p)
	nd := strings.TrimRight(slashed(dir), "/")
	if nd == "" || !strings.HasPrefix(np, nd+"/") {
		return "", false
	}
	return np[len(nd)+1:], true
}

// slashed rewrites "\" to "/" so a prefix comparison works whichever
// separator the two paths were recorded with.
func slashed(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// Home renders an absolute path the way a person writes it, with the home
// directory as "~". A card that shows a full home path spends half its
// width on the same prefix every time.
func Home(p string) string {
	home := homeDir()
	if home == "" {
		return p
	}
	nh := strings.TrimRight(slashed(home), "/")
	np := slashed(p)
	if nh == "" || np != nh && !strings.HasPrefix(np, nh+"/") {
		return p
	}
	return "~" + np[len(nh):]
}

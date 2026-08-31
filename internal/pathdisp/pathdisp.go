// Package pathdisp renders filesystem paths for a human reading a phone.
//
// It treats "/" and "\" as separators regardless of the OS Wirelark runs
// on: paths reach Wirelark from Claude Code payloads and transcripts, so
// the separator is a property of the recorded path, not of this process.
package pathdisp

import "strings"

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

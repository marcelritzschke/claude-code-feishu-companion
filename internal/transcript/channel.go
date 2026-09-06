package transcript

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// A channel event is the one thing Claude Code never acknowledges: a
// session that did not register a channel server drops its events without
// a word, so writing one out successfully proves nothing about it being
// read. The transcript is where that gap closes - a channel event Claude
// actually received is written into it like any other user message.

// channelTag is how Claude Code renders a channel event's opening tag.
// The source is followed by at least one meta attribute, which is what
// tells a delivered message apart from the server's own instructions:
// those name the same source and close the tag immediately after it.
const channelTag = `<channel source=\"%s\" `

// Size is how far a transcript has been written, which is where a caller
// that wants to know what happens next starts reading. It is zero for a
// transcript that cannot be read, so the whole file is read instead of
// silently skipping past what it already holds.
func Size(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Delivered reports whether a channel event from source appears in the
// transcript at path beyond offset bytes - that is, whether a message
// pushed into that session when the transcript stood that long was read
// by Claude rather than dropped on the way in.
//
// A transcript that cannot be read reports false, and callers must treat
// that as "no proof yet" rather than as proof of loss: it is also what a
// session that has not written its first line yet looks like.
func Delivered(path string, offset int64, source string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return false
		}
	}
	want := strings.Replace(channelTag, "%s", source, 1)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if strings.Contains(sc.Text(), want) {
			return true
		}
	}
	return false
}

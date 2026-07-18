// Package clipboard copies text to the user's local clipboard using the OSC 52
// terminal escape sequence.
//
// OSC 52 is the primary mechanism because it is OS-independent (the terminal
// emulator does the work, not this binary), needs no external helpers such as
// xclip/xsel/wl-copy/pbcopy, and — crucially for OpenStack tooling — works over
// SSH and through tmux, targeting the operator's *local* clipboard rather than a
// bastion's. Native clipboard libraries are unsuitable as the default: on Linux
// they shell out to helpers absent on minimal servers, and over SSH they target
// the wrong machine.
//
// Caveats surfaced to the user by the caller: OSC 52 sometimes needs enabling
// (a terminal setting, or tmux `set-clipboard on`), and some terminals cap the
// payload size — fine for IDs and names, but a large raw-object dump may be
// truncated, which is why copying an ID/name is more reliable than copying a
// whole object.
package clipboard

import (
	"io"
	"os"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// Sequence returns the OSC 52 escape sequence that hands text to the terminal
// emulator for the local clipboard. Under tmux ($TMUX) the sequence is wrapped
// so tmux forwards it to the outer terminal (requires `set-clipboard on`);
// under GNU screen ($STY) the screen DCS wrapper is used.
func Sequence(text string) string {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case os.Getenv("STY") != "":
		seq = seq.Screen()
	}
	return seq.String()
}

// Emit writes the OSC 52 sequence for text to w (typically os.Stdout). The
// sequence is non-visual, so it can be written to the terminal a TUI owns
// without disturbing the rendered frame.
func Emit(w io.Writer, text string) error {
	_, err := io.WriteString(w, Sequence(text))
	return err
}

// LargePayload reports whether text is big enough that some terminals may
// truncate the OSC 52 copy, so the caller can warn the user.
func LargePayload(text string) bool {
	// Conservative threshold; the common ~100 KiB terminal cap is well above
	// this, but many older terminals cap far lower.
	const warnBytes = 8 * 1024
	return len(text) > warnBytes
}

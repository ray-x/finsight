package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// copyToClipboard copies s to the OS clipboard. It tries platform-native
// helpers first (pbcopy on macOS, wl-copy / xclip / xsel on Linux,
// clip.exe on Windows / WSL) and falls back to an OSC 52 terminal escape
// which most modern terminals (iTerm2, WezTerm, kitty, Alacritty, tmux
// with set-clipboard on) honour — including over SSH.
//
// Returns (helperName, nil) when one of the native helpers succeeds, or
// ("osc52", nil) when the OSC 52 fallback is emitted. Returns a non-nil
// error only when no strategy produced output.
func copyToClipboard(s string) (string, error) {
	if name, err := nativeClipboardCopy(s); err == nil {
		return name, nil
	}
	// OSC 52 fallback: echo the escape sequence to stderr so it isn't
	// captured by the Bubble Tea renderer writing to stdout.
	encoded := base64.StdEncoding.EncodeToString([]byte(s))
	// BEL (\a / \x07) terminator is the most widely supported.
	seq := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	if _, err := os.Stderr.WriteString(seq); err != nil {
		return "", err
	}
	return "osc52", nil
}

// nativeClipboardCopy pipes s to the first available platform helper.
// Returns the helper name on success.
func nativeClipboardCopy(s string) (string, error) {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "linux":
		// Prefer Wayland when available; fall back to X11 tools.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		candidates = append(candidates,
			[]string{"xclip", "-selection", "clipboard"},
			[]string{"xsel", "--clipboard", "--input"},
		)
	case "windows":
		candidates = [][]string{{"clip.exe"}, {"clip"}}
	}
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = bytes.NewReader([]byte(s))
		if err := cmd.Run(); err == nil {
			return argv[0], nil
		}
	}
	return "", fmt.Errorf("no clipboard helper available")
}

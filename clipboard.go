package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// copyValue puts a value on the clipboard and logs what happened, which is the
// only feedback a tray menu has for a copy. It is what the agent hands its
// plugins, so a plugin copies the same way the tray does.
func copyValue(what, value string) {
	if value == "" {
		return
	}

	if err := copyToClipboard(value); err != nil {
		log.Printf("Failed to copy %s: %v", what, err)
		return
	}
	log.Printf("Copied %s to clipboard", what)
}

// clipboardCmd describes one candidate clipboard utility.
type clipboardCmd struct {
	name string
	args []string
}

// copyToClipboard copies text to the system clipboard. On Linux it picks the
// tool matching the active display server (wl-copy under Wayland, xclip/xsel
// under X11), falling back to whichever utility is installed if env vars are
// unset (e.g. headless / virtual sessions).
func copyToClipboard(text string) error {
	candidates, err := clipboardCandidates(runtime.GOOS, os.Getenv)
	if err != nil {
		return err
	}

	var lastErr error
	var tried []string
	for _, c := range candidates {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		tried = append(tried, c.name)
		if err := pipeStringToCommand(path, c.args, text); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("clipboard write failed (tried %s): %w", strings.Join(tried, ", "), lastErr)
	}
	return clipboardUnavailableError()
}

// clipboardCandidates returns the ordered list of clipboard utilities to try
// for the given OS, using getenv to inspect the current display environment.
// Pure and testable; pass os.Getenv in production.
func clipboardCandidates(goos string, getenv func(string) string) ([]clipboardCmd, error) {
	switch goos {
	case "darwin":
		return []clipboardCmd{{name: "pbcopy"}}, nil
	case "windows":
		return []clipboardCmd{{name: "clip"}}, nil
	case "linux":
		var cands []clipboardCmd
		if getenv("WAYLAND_DISPLAY") != "" {
			cands = append(cands, clipboardCmd{name: "wl-copy"})
		}
		if getenv("DISPLAY") != "" {
			cands = append(cands,
				clipboardCmd{name: "xclip", args: []string{"-selection", "clipboard"}},
				clipboardCmd{name: "xsel", args: []string{"--clipboard", "--input"}},
			)
		}
		// Env didn't tell us the session type — try everything in preference order.
		if len(cands) == 0 {
			cands = []clipboardCmd{
				{name: "wl-copy"},
				{name: "xclip", args: []string{"-selection", "clipboard"}},
				{name: "xsel", args: []string{"--clipboard", "--input"}},
			}
		}
		return cands, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", goos)
	}
}

func clipboardUnavailableError() error {
	if runtime.GOOS == "linux" {
		return fmt.Errorf("no clipboard utility found; install one of: wl-clipboard (Wayland), xclip, or xsel")
	}
	return fmt.Errorf("no clipboard utility found")
}

func pipeStringToCommand(path string, args []string, text string) error {
	cmd := exec.Command(path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

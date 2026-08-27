// Package clipboard puts text on the system clipboard by shelling out to
// whichever utility the platform provides.
//
// It is a package of its own because more than one thing offers to copy
// something: the tray menu, and the plugins that add entries to it.
package clipboard

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Copy puts text on the system clipboard. On Linux it picks the tool matching
// the active display server (wl-copy under Wayland, xclip or xsel under X11),
// falling back to whichever utility is installed when the environment says
// nothing, as in a headless or virtual session.
func Copy(text string) error {
	cands, err := candidates(runtime.GOOS, os.Getenv)
	if err != nil {
		return err
	}

	var lastErr error
	var tried []string
	for _, c := range cands {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		tried = append(tried, c.name)
		if err := pipe(path, c.args, text); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("clipboard write failed (tried %s): %w", strings.Join(tried, ", "), lastErr)
	}
	return unavailableError()
}

// command describes one candidate clipboard utility.
type command struct {
	name string
	args []string
}

// candidates returns the ordered list of clipboard utilities to try for the
// given OS, using getenv to inspect the current display environment. Pure and
// testable; Copy passes os.Getenv.
func candidates(goos string, getenv func(string) string) ([]command, error) {
	switch goos {
	case "darwin":
		return []command{{name: "pbcopy"}}, nil
	case "windows":
		return []command{{name: "clip"}}, nil
	case "linux":
		var cands []command
		if getenv("WAYLAND_DISPLAY") != "" {
			cands = append(cands, command{name: "wl-copy"})
		}
		if getenv("DISPLAY") != "" {
			cands = append(cands,
				command{name: "xclip", args: []string{"-selection", "clipboard"}},
				command{name: "xsel", args: []string{"--clipboard", "--input"}},
			)
		}
		// Env didn't tell us the session type: try everything in preference order.
		if len(cands) == 0 {
			cands = []command{
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

func unavailableError() error {
	if runtime.GOOS == "linux" {
		return fmt.Errorf("no clipboard utility found; install one of: wl-clipboard (Wayland), xclip, or xsel")
	}
	return fmt.Errorf("no clipboard utility found")
}

func pipe(path string, args []string, text string) error {
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

// CopyValue puts a value on the clipboard and reports what happened on logger,
// which is the only feedback a tray menu has for a copy. A blank value or a nil
// logger copies nothing.
func CopyValue(logger *log.Logger, what, value string) {
	if value == "" || logger == nil {
		return
	}

	if err := Copy(value); err != nil {
		logger.Printf("Failed to copy the %s: %v", what, err)
		return
	}
	logger.Printf("Copied the %s to the clipboard", what)
}

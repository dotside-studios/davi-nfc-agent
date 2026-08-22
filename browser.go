package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser shows a URL in whatever the operator's desktop opens links with.
//
// It lives outside the control center's files because the console is not the
// only thing with a page to show: the pairing plugin opens its own, and so may
// a consumer's. A build without the console still has both.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}

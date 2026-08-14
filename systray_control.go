package main

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"

	"fyne.io/systray"
)

// setupControlMenu adds the control center entry.
func (s *SystrayApp) setupControlMenu() {
	s.mControlCenter = systray.AddMenuItem("Open Control Center", "Manage this agent in a browser")
	if s.control == nil {
		s.mControlCenter.Hide()
	}
}

// handleOpenControlCenter mints a single-use token and opens the console.
func (s *SystrayApp) handleOpenControlCenter() {
	if s.control == nil {
		return
	}

	url, err := s.control.ConsoleURL()
	if err != nil {
		log.Printf("Failed to prepare control center URL: %v", err)
		return
	}

	if err := openBrowser(url); err != nil {
		// Falling back to the clipboard keeps this usable on a machine with no
		// registered browser handler, which is common on minimal Linux desktops.
		log.Printf("Failed to open a browser: %v", err)
		if copyErr := copyToClipboard(url); copyErr != nil {
			log.Printf("Control center URL (expires shortly): %s", url)
			return
		}
		log.Printf("Control center URL copied to clipboard; it expires shortly")
	}
}

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

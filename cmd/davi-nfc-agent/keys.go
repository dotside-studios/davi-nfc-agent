package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc/keyfile"
)

// keyFilePath is the key file the agent was started with: -keys, else
// DAVI_NFC_KEYS, the same precedence agent.Setup applies.
func keyFilePath(opts *agent.Options) string {
	if opts.KeysFile != "" {
		return opts.KeysFile
	}
	return os.Getenv("DAVI_NFC_KEYS")
}

// watchKeyFile reloads the key file when the process gets SIGHUP, so keys can
// be rotated without a restart. A reload that fails keeps the keys already
// held and says why: unlike startup, there is a working configuration to fall
// back on. Replacing the NTAG 424 keys drops the sessions open under the old
// ones.
func watchKeyFile(a *agent.Agent, path string) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			keys, err := keyfile.Load(path)
			if err != nil {
				startupWarn.Printf("Reloading keys from %s failed, keeping the keys already loaded: %v", path, err)
				continue
			}
			a.SetKeys(keys)
			startupLog.Printf("Reloaded keys from %s: %s", path, keys.Summary())
		}
	}()
}

// Command davi-virtual-reader runs a standalone software NFC reader: a reader
// whose tags come from software rather than PC/SC hardware, built on
// nfc/virtualnfc. It stands up a real nfc.Supervisor, presents the tags you
// declare, prints every scan, and takes interactive commands to tap, remove and
// write tags — no reader, phone, or hardware required.
//
//	davi-virtual-reader -device entry,exit -tag uid=04A1B2C3,text=hello
//
// Then type `help` at the prompt.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/virtualnfc"
)

func main() {
	var deviceList string
	var tagFlags multiFlag
	flag.StringVar(&deviceList, "device", virtualnfc.DefaultReaderDevice, "comma-separated device (lane) names")
	flag.Var(&tagFlags, "tag", "initial tag as key=value pairs, e.g. uid=04A1B2C3,text=hello[,uri=...,type=...,ro]; repeatable")
	flag.Parse()

	devices := splitCSV(deviceList)
	reader, err := virtualnfc.NewReader(devices...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "virtual reader: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	conn := reader.Scans().Connect(printScan)
	defer conn.Disconnect()

	active := devices[0]
	for _, raw := range tagFlags {
		spec, err := parseTagSpec(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad -tag %q: %v\n", raw, err)
			os.Exit(1)
		}
		if err := reader.Present(active, spec); err != nil {
			fmt.Fprintf(os.Stderr, "present %s: %v\n", spec.UID, err)
			os.Exit(1)
		}
	}

	fmt.Printf("virtual reader up. devices: %s. active: %s. type 'help' for commands.\n",
		strings.Join(reader.Devices(), ", "), active)

	// Ctrl-C closes cleanly by closing stdin's scanner via the signal handler.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nbye")
		reader.Close()
		os.Exit(0)
	}()

	repl(reader, active)
}

// repl reads and dispatches interactive commands until EOF.
func repl(reader *virtualnfc.Reader, active string) {
	in := bufio.NewScanner(os.Stdin)
	prompt(active)
	for in.Scan() {
		fields := strings.Fields(in.Text())
		if len(fields) > 0 {
			active = dispatch(reader, active, fields)
		}
		prompt(active)
	}
}

func prompt(active string) { fmt.Printf("[%s]> ", active) }

// dispatch runs one command and returns the (possibly changed) active device.
func dispatch(reader *virtualnfc.Reader, active string, fields []string) string {
	cmd, args := fields[0], fields[1:]
	switch cmd {
	case "help", "?":
		printHelp()
	case "ls":
		fmt.Printf("devices: %s (active: %s)\n", strings.Join(reader.Devices(), ", "), active)
	case "use":
		if len(args) != 1 {
			fmt.Println("usage: use <device>")
			break
		}
		active = args[0]
	case "tap", "lock":
		if len(args) < 1 {
			fmt.Printf("usage: %s <uid> [text...]\n", cmd)
			break
		}
		spec := virtualnfc.TagSpec{UID: args[0], Text: strings.Join(args[1:], " "), ReadOnly: cmd == "lock"}
		report(reader.Present(active, spec), "tapped "+args[0])
	case "uri":
		if len(args) != 2 {
			fmt.Println("usage: uri <uid> <uri>")
			break
		}
		report(reader.Present(active, virtualnfc.TagSpec{UID: args[0], URI: args[1]}), "tapped "+args[0])
	case "off":
		if len(args) != 1 {
			fmt.Println("usage: off <uid>")
			break
		}
		report(reader.Remove(active, args[0]), "removed "+args[0])
	case "write":
		if len(args) < 2 {
			fmt.Println("usage: write <uid> <text...>")
			break
		}
		writeTag(reader, active, args[1:])
	case "caps":
		showCaps(reader, active)
	case "quit", "exit":
		reader.Close()
		os.Exit(0)
	default:
		fmt.Printf("unknown command %q; type 'help'\n", cmd)
	}
	return active
}

func writeTag(reader *virtualnfc.Reader, active string, text []string) {
	msg, err := virtualnfc.TextMessage(strings.Join(text, " "), "")
	if err != nil {
		fmt.Printf("write: %v\n", err)
		return
	}
	res, err := reader.Write(active, msg)
	if err != nil {
		fmt.Printf("write: %v\n", err)
		return
	}
	fmt.Printf("wrote %d bytes (verified=%t, locked=%t)\n", res.BytesWritten, res.Verified, res.Locked)
}

func showCaps(reader *virtualnfc.Reader, active string) {
	caps, err := reader.Capabilities(active)
	if err != nil {
		fmt.Printf("caps: %v\n", err)
		return
	}
	fmt.Printf("caps: read=%t write=%t lock=%t transceive=%t readOnly=%t snapshot=%t family=%q tech=%q\n",
		caps.CanRead, caps.CanWrite, caps.CanLock, caps.CanTransceive,
		caps.IsReadOnly, caps.ReadsAreSnapshot, caps.TagFamily, caps.Technology)
}

func report(err error, ok string) {
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println(ok)
}

func printScan(d nfc.NFCData) {
	switch {
	case d.Err != nil:
		fmt.Printf("\nscan[%s] error: %v\n", d.Device, d.Err)
	case d.Card != nil:
		fmt.Printf("\nscan[%s] tag %s (%s / %s)\n", d.Device, d.Card.UID, d.Card.Type, d.Card.Technology)
	case d.RemovedUID != "":
		fmt.Printf("\nremoved[%s] tag %s\n", d.Device, d.RemovedUID)
	}
}

func printHelp() {
	fmt.Print(`commands:
  tap <uid> [text...]    present a tag (blank if no text)
  uri <uid> <uri>        present a tag holding a URI
  lock <uid> [text...]   present a read-only tag
  write <uid> <text...>  write text to a tag through the pipeline
  off <uid>              remove a tag from the field
  caps                   show the active device's tag capabilities
  use <device>           switch the active device
  ls                     list devices
  help                   this text
  quit                   exit
`)
}

// parseTagSpec parses "uid=04A1B2C3,text=hello,ro" into a TagSpec.
func parseTagSpec(s string) (virtualnfc.TagSpec, error) {
	var spec virtualnfc.TagSpec
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "ro" {
			spec.ReadOnly = true
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return spec, fmt.Errorf("expected key=value, got %q", part)
		}
		switch key {
		case "uid":
			spec.UID = val
		case "text":
			spec.Text = val
		case "uri":
			spec.URI = val
		case "type":
			spec.Type = val
		case "tech", "technology":
			spec.Technology = val
		default:
			return spec, fmt.Errorf("unknown key %q", key)
		}
	}
	if spec.UID == "" {
		return spec, fmt.Errorf("uid is required")
	}
	return spec, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{virtualnfc.DefaultReaderDevice}
	}
	return out
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, " ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

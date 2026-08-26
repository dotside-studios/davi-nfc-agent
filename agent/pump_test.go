package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/gorilla/websocket"
)

// recordingSink stands in for whatever serves clients: it subscribes to what
// the agent reports, as the client server does.
type recordingSink struct {
	tags chan nfc.NFCData
}

func newSink(t *testing.T, a *Agent) *recordingSink {
	t.Helper()

	s := &recordingSink{tags: make(chan nfc.NFCData, 8)}
	conn := a.Events().Tag.Connect(func(data nfc.NFCData) {
		select {
		case s.tags <- data:
		default:
		}
	})
	t.Cleanup(conn.Disconnect)
	return s
}

func awaitScan(t *testing.T, sink *recordingSink) nfc.NFCData {
	t.Helper()

	select {
	case data := <-sink.tags:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("the agent reported nothing")
		return nfc.NFCData{}
	}
}

// startedPumpAgent is an agent whose readers are running, which is when the pin
// can name a reader other than the one being read.
func startedPumpAgent(t *testing.T) *Agent {
	t.Helper()

	a := newPumpAgent(t)
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Stop)
	return a
}

func newPumpAgent(t *testing.T) *Agent {
	t.Helper()

	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return rt.Agent
}

// The filter is the agent's, and it decides before the scan reaches the bridge.
func TestForwardScanAppliesTheCardTypeFilter(t *testing.T) {
	a := newPumpAgent(t)
	sink := newSink(t, a)

	// The shipped default is an empty filter, which admits everything, so a
	// type has to be named for there to be a filter at all.
	named := nfc.GetAllCardTypes()[0]
	a.AllowCardType(named)

	tag := nfc.NewMockTag("04A1B2C3")
	tag.TagType = named
	card := nfc.NewCard(tag)

	t.Run("a type the filter names is admitted", func(t *testing.T) {
		a.forwardScan(nfc.NFCData{Card: card})
		if got := awaitScan(t, sink); got.Card == nil || got.Card.UID != "04A1B2C3" {
			t.Errorf("got %+v, want the scan", got)
		}
	})

	t.Run("a type it does not name is refused", func(t *testing.T) {
		other := nfc.NewMockTag("04A1B2C3")
		other.TagType = "MIFARE Plus"

		a.forwardScan(nfc.NFCData{Card: nfc.NewCard(other)})

		// Refused scans still reach the bridge, as an error: a client that
		// asked for a write needs to hear why nothing happened.
		got := awaitScan(t, sink)
		if got.Card != nil {
			t.Errorf("a filtered-out card reached the clients: %+v", got.Card)
		}
		if got.Err == nil {
			t.Error("a filtered-out scan must say why it was dropped")
		}
	})

	t.Run("an empty filter admits a type the agent does not know", func(t *testing.T) {
		a.ClearCardTypeFilter()

		other := nfc.NewMockTag("04A1B2C3")
		other.TagType = "MIFARE Plus"

		a.forwardScan(nfc.NFCData{Card: nfc.NewCard(other)})
		if got := awaitScan(t, sink); got.Card == nil {
			t.Errorf("got %+v, want the scan admitted by an empty filter", got)
		}
	})
}

// A read failure is reported rather than swallowed.
func TestForwardScanPassesErrorsThrough(t *testing.T) {
	a := newPumpAgent(t)
	sink := newSink(t, a)

	a.forwardScan(nfc.NFCData{Err: context.DeadlineExceeded})
	if got := awaitScan(t, sink); got.Err == nil {
		t.Error("the error did not reach the bridge")
	}
}

// What the readers scan reaches whatever serves clients, which subscribes to
// the agent rather than being handed the scans. A client's half of that trip is
// e2e's: see TestAScanOnTheReaderReachesAClientAndAnObserver.
func TestAScanFromTheReadersIsReportedAfterStart(t *testing.T) {
	m := nfc.NewMockManager()
	tag := nfc.NewMockTag("04ABCDEF")
	if err := tag.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	m.MockDevice.SetTags([]nfc.Tag{tag})

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	seen := make(chan string, 1)
	a.Events().Tag.Connect(func(data nfc.NFCData) {
		if data.Card != nil {
			select {
			case seen <- data.Card.UID:
			default:
			}
		}
	})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	select {
	case uid := <-seen:
		if uid != "04ABCDEF" {
			t.Errorf("got %q, want what the reader scanned", uid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("what the readers scanned was never reported")
	}
}

// The readers' status reaches a subscriber as well as the clients. It used to
// reach the clients only, which left the tray polling the last card twice a
// second to find out what the agent already knew.
func TestReaderStatusReachesSubscribersAndClients(t *testing.T) {
	a := newPumpAgent(t)

	seen := make(chan nfc.DeviceStatus, 8)
	a.Events().Reader.Connect(func(status nfc.DeviceStatus) {
		select {
		case seen <- status:
		default:
		}
	})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	select {
	case status := <-seen:
		if status.Device != "mock:usb:001" {
			t.Errorf("the status names %q, want the reader it describes", status.Device)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the readers' status never reached a subscriber")
	}
}

// reportingManager stands in for a driver whose devices report their own scans,
// which is what the phone driver is.
type reportingManager struct {
	nfc.Manager
	scans event.Signal[nfc.ScannedTag]
}

func (m *reportingManager) Scans() *event.Signal[nfc.ScannedTag] { return &m.scans }

// report is a device reporting a tag, as the driver would.
func (m *reportingManager) report(device, uid string) {
	m.scans.Emit(nfc.ScannedTag{Device: device, Tag: nfc.NewMockTag(uid)})
}

// What a manager reports reaches the clients while the agent is serving, and
// stops reaching them once it is not: the subscription belongs to the run, not
// to the agent.
func TestManagerScansReachTheClientsWhileServing(t *testing.T) {
	m := &reportingManager{Manager: nfc.NewMockManager()}
	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	seen := make(chan string, 4)
	a.Events().Tag.Connect(func(data nfc.NFCData) {
		if data.Card == nil {
			return
		}
		select {
		case seen <- data.Card.UID:
		default:
		}
	})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.report("phone-9f2a", "04ABCDEF")

	select {
	case uid := <-seen:
		if uid != "04ABCDEF" {
			t.Errorf("got %q, want the scan the manager reported", uid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("what the manager reported never reached the clients")
	}

	a.Stop()
	m.report("phone-9f2a", "04EEEEEE")

	select {
	case uid := <-seen:
		t.Errorf("a scan reached %q after the agent stopped", uid)
	case <-time.After(300 * time.Millisecond):
	}
}

// A scan the card-type filter refuses still says which device presented it.
func TestARefusedScanKeepsItsDevice(t *testing.T) {
	a := newPumpAgent(t)
	sink := newSink(t, a)

	named := nfc.GetAllCardTypes()[0]
	a.AllowCardType(named)

	tag := nfc.NewMockTag("04A1B2C3")
	tag.TagType = "some-other-type"
	a.forwardScan(nfc.NFCData{Device: "mock:usb:001", Card: nfc.NewCard(tag)})

	got := awaitScan(t, sink)
	if got.Err == nil {
		t.Fatal("the refusal did not reach the bridge")
	}
	if got.Device != "mock:usb:001" {
		t.Errorf("the refusal names device %q, want the one that presented the card", got.Device)
	}
}

// The pinned device filters rather than locks. A preference set from the
// console records the choice without restarting the reader, so until something
// does, the reader is still on the old device: those scans used to reach every
// client as though nothing had been asked for.
func TestScansFromAnUnselectedReaderAreDropped(t *testing.T) {
	a := startedPumpAgent(t)
	sink := newSink(t, a)

	a.SetPinnedDevice("ACS ACR122U 00")
	a.forwardScan(nfc.NFCData{
		Device: "mock:usb:001",
		Card:   nfc.NewCard(nfc.NewMockTag("04A1B2C3")),
	})

	select {
	case got := <-sink.tags:
		t.Errorf("a scan from %q reached the clients while %q is selected", got.Device, a.CurrentPinnedDevice())
	case <-time.After(200 * time.Millisecond):
	}
}

// The selected reader's own scans pass, and so does every scan when nothing is
// pinned, which is auto-detect.
func TestTheSelectedReaderIsForwarded(t *testing.T) {
	a := startedPumpAgent(t)
	sink := newSink(t, a)

	a.SetPinnedDevice("mock:usb:001")
	a.forwardScan(nfc.NFCData{
		Device: "mock:usb:001",
		Card:   nfc.NewCard(nfc.NewMockTag("04A1B2C3")),
	})
	if got := awaitScan(t, sink); got.Card == nil || got.Card.UID != "04A1B2C3" {
		t.Fatalf("got %+v, want the selected reader's scan", got)
	}

	a.SetPinnedDevice("")
	a.forwardScan(nfc.NFCData{
		Device: "some-other-reader",
		Card:   nfc.NewCard(nfc.NewMockTag("04FFFFFF")),
	})
	if got := awaitScan(t, sink); got.Card == nil || got.Card.UID != "04FFFFFF" {
		t.Fatalf("got %+v, want the scan admitted by auto-detect", got)
	}
}

// A scan from a source that does not say which device it came from is not
// something the reader filter can judge, so it passes rather than vanishing.
func TestAScanWithNoDeviceIsNotFiltered(t *testing.T) {
	a := startedPumpAgent(t)
	sink := newSink(t, a)

	a.SetPinnedDevice("ACS ACR122U 00")
	a.forwardScan(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})

	if got := awaitScan(t, sink); got.Card == nil {
		t.Fatalf("got %+v, want the scan that named no device", got)
	}
}

// A phone reports its own scans and is not a reader the agent chose to read
// from, so pinning a reader has nothing to say about it. Both arrive on one
// signal now, so the distinction has to be made rather than fallen into.
func TestAPinnedReaderDoesNotFilterDevices(t *testing.T) {
	m := &reportingManager{Manager: nfc.NewMockManager()}
	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent
	a.SetPinnedDevice("ACS ACR122U 00")

	seen := make(chan string, 4)
	a.Events().Tag.Connect(func(data nfc.NFCData) {
		if data.Card == nil {
			return
		}
		select {
		case seen <- data.Card.UID:
		default:
		}
	})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	m.report("phone-9f2a", "04ABCDEF")

	select {
	case uid := <-seen:
		if uid != "04ABCDEF" {
			t.Errorf("got %q, want the phone's scan", uid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a phone's scan was filtered by the pinned reader")
	}
}

// Starting without naming a device is auto-detect, and stays that way. It used
// to pin whichever reader was listed first, so an agent that polls every reader
// dropped the scans of all but one of them.
func TestStartingWithNoDeviceServesEveryReader(t *testing.T) {
	m := nfc.NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if pinned := a.CurrentPinnedDevice(); pinned != "" {
		t.Errorf("a start that named no device pinned %q", pinned)
	}

	sink := newSink(t, a)
	for _, device := range []string{"mock:usb:001", "mock:usb:002"} {
		a.forwardScan(nfc.NFCData{Device: device, Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
		if got := awaitScan(t, sink); got.Device != device {
			t.Errorf("got a scan from %q, want the one from %q", got.Device, device)
		}
	}
}

// A start that names a device is a choice, and the preferences report it.
func TestStartingWithADevicePinsIt(t *testing.T) {
	m := nfc.NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if err := a.Start("mock:usb:002"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if pinned := a.CurrentPinnedDevice(); pinned != "mock:usb:002" {
		t.Errorf("CurrentPinnedDevice = %q, want the device the start named", pinned)
	}
}

// clientOf connects to a client server the way an application does, and returns
// the message types it receives.
func clientOf(t *testing.T, srv *clientserver.Server) chan string {
	t.Helper()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// A scan broadcast before the server has registered the connection reaches
	// nobody, and the handshake returns first.
	deadline := time.Now().Add(3 * time.Second)
	for srv.ClientCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the client server never registered the connection")
		}
		time.Sleep(5 * time.Millisecond)
	}

	seen := make(chan string, 16)
	go func() {
		for {
			var msg struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			select {
			case seen <- msg.Type:
			default:
			}
		}
	}()
	return seen
}

// await reports whether one of the messages arrives.
func await(t *testing.T, seen chan string, want string) bool {
	t.Helper()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-seen:
			if got == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// What the agent reports reaches the clients, scans and reader status alike,
// and keeps reaching them across a stop and start: the server is the plugin's
// rather than the run's, so a client is not silently cut off by a restart it
// cannot see.
func TestWhatTheAgentReportsReachesTheClients(t *testing.T) {
	// Serving clients is the plugin's, so an agent with none has nobody to
	// report to.
	p := &ServerPlugin{}
	a := serverAgent(t, p)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	serving := p.serving()
	client := clientOf(t, serving)

	a.forwardScan(nfc.NFCData{Device: "mock:usb:001", Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
	if !await(t, client, "tagData") {
		t.Fatal("a scan the agent reported never reached the clients")
	}
	a.fireReaderStatus(nfc.DeviceStatus{Device: "mock:usb:001", Connected: true})
	if !await(t, client, "deviceStatus") {
		t.Fatal("the readers' status never reached the clients")
	}

	a.Stop()
	if err := a.Start(""); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	defer a.Stop()

	if p.serving() != serving {
		t.Fatal("the restart replaced the server the clients are connected to")
	}

	a.forwardScan(nfc.NFCData{Device: "mock:usb:001", Card: nfc.NewCard(nfc.NewMockTag("04FFFFFF"))})
	if !await(t, client, "tagData") {
		t.Error("a scan after the restart never reached the client that stayed connected")
	}
}

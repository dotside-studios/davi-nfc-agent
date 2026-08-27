package clientserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// blockingOps holds a write open until the test releases it, and reports what
// ended the operation. It stands in for a reader mid-transfer.
type blockingOps struct {
	stoppedOps

	entered  chan struct{}
	ended    chan error
	release  chan struct{}
	inFlight atomic.Int32
}

func newBlockingOps() *blockingOps {
	return &blockingOps{
		entered: make(chan struct{}, 16),
		ended:   make(chan error, 16),
		release: make(chan struct{}),
	}
}

func (o *blockingOps) Write(ctx context.Context, _ server.WriteOp) (*nfc.WriteResult, error) {
	o.inFlight.Add(1)
	defer o.inFlight.Add(-1)

	o.entered <- struct{}{}
	select {
	case <-ctx.Done():
		o.ended <- ctx.Err()
		return nil, ctx.Err()
	case <-o.release:
		o.ended <- nil
		return &nfc.WriteResult{UID: "04A1B2C3"}, nil
	}
}

// writeRequest is the frame a client sends to start a write.
func writeRequest(id string) map[string]any {
	return map[string]any{
		"id":      id,
		"type":    server.WSMessageTypeWriteRequest,
		"payload": map[string]any{"uid": "04A1B2C3", "text": "hello"},
	}
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not happen within 2s", what)
	}
}

// A client that goes away mid-write cancels the write. Before the read loop was
// split from dispatch, the loop sat inside the handler for the length of the
// operation, so the disconnect was not noticed until after it had finished.
func TestClientDisconnectCancelsInFlightOperation(t *testing.T) {
	ops := newBlockingOps()
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "")

	if err := conn.WriteJSON(writeRequest("req-1")); err != nil {
		t.Fatalf("send write: %v", err)
	}
	awaitSignal(t, ops.entered, "the write reaching the ops layer")

	// The client vanishes while the write is still running.
	_ = conn.Close()

	select {
	case err := <-ops.ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the operation ended with %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the operation was never cancelled after the client disconnected")
	}
}

// An operator disconnecting a client cancels what that client asked for, not
// just its socket.
func TestOperatorDisconnectCancelsInFlightOperation(t *testing.T) {
	ops := newBlockingOps()
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "")

	clients := s.Clients()
	if len(clients) != 1 {
		t.Fatalf("Clients() = %d, want 1", len(clients))
	}

	if err := conn.WriteJSON(writeRequest("req-1")); err != nil {
		t.Fatalf("send write: %v", err)
	}
	awaitSignal(t, ops.entered, "the write reaching the ops layer")

	if !s.Disconnect(clients[0].ID) {
		t.Fatal("Disconnect reported no such client")
	}

	select {
	case err := <-ops.ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the operation ended with %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an operator disconnect did not cancel the operation")
	}
}

// A completed operation still answers, and its context is not cancelled before
// the reply is written.
func TestCompletedOperationStillAnswers(t *testing.T) {
	ops := newBlockingOps()
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "")

	if err := conn.WriteJSON(writeRequest("req-1")); err != nil {
		t.Fatalf("send write: %v", err)
	}
	awaitSignal(t, ops.entered, "the write reaching the ops layer")
	close(ops.release)

	msg := readResponse(t, conn)
	if success, _ := msg["success"].(bool); !success {
		t.Errorf("the write answered %v, want success", msg)
	}
	if err := <-ops.ended; err != nil {
		t.Errorf("the operation ended with %v, want it to complete", err)
	}
}

// The reader goroutine keeps reading while an operation runs, so a client that
// asks faster than the agent can answer is refused rather than buffered without
// limit, and the connection stays usable.
func TestConnectionRefusesWhenTheQueueIsFull(t *testing.T) {
	ops := newBlockingOps()
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "")

	// One in flight, clientRequestQueue buffered, and the rest refused.
	for i := 0; i < clientRequestQueue+6; i++ {
		if err := conn.WriteJSON(writeRequest("req")); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	var busy bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !busy {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		if payload, ok := msg["payload"].(map[string]any); ok {
			if code, _ := payload["code"].(string); code == string(protocol.ErrCodeBusy) {
				busy = true
			}
		}
	}
	if !busy {
		t.Error("a client that overran the request queue was not refused with BUSY")
	}

	close(ops.release)
}

// Requests are served one at a time, as they were when the handler ran inline.
func TestRequestsStaySerialized(t *testing.T) {
	ops := newBlockingOps()
	close(ops.release) // every write completes immediately
	s := New(Config{AllowedOrigins: []string{"*"}, Ops: ops})
	conn := dial(t, s, "")

	var peak int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			<-ops.entered
			if n := ops.inFlight.Load(); n > peak {
				peak = n
			}
		}
	}()

	for i := 0; i < 5; i++ {
		if err := conn.WriteJSON(writeRequest("req")); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	awaitSignal(t, done, "five writes reaching the ops layer")

	if peak > 1 {
		t.Errorf("%d operations ran at once on one connection, want them serialized", peak)
	}
}

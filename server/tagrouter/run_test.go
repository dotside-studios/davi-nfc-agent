package tagrouter_test

import (
	"context"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/tagrouter"
)

// opResult is what an operation reports, in the shape the tests assert on. The
// router returns a value and an error now; this keeps the assertions about
// behaviour rather than about calling convention.
type opResult struct {
	Success   bool
	Error     string
	ErrorCode protocol.ErrorCode
	Payload   any
}

func resultOf(payload any, err error) opResult {
	if err != nil {
		return opResult{Error: err.Error(), ErrorCode: protocol.ErrorPayloadFor(err).Code}
	}
	return opResult{Success: true, Payload: payload}
}

func runWrite(r *tagrouter.Router, op server.WriteOp) opResult {
	res, err := r.Write(context.Background(), op)
	return resultOf(res, err)
}

func runLock(r *tagrouter.Router, op server.LockOp) opResult {
	res, err := r.Lock(context.Background(), op)
	return resultOf(res, err)
}

func runCapabilities(r *tagrouter.Router, op server.CapabilitiesOp) opResult {
	res, err := r.Capabilities(context.Background(), op)
	return resultOf(res, err)
}

// goWrite starts a write and returns a channel carrying its result, for a test
// that must read what reached the device before the call can finish.
func goWrite(r *tagrouter.Router, op server.WriteOp) <-chan opResult {
	out := make(chan opResult, 1)
	go func() { out <- runWrite(r, op) }()
	return out
}

func goLock(r *tagrouter.Router, op server.LockOp) <-chan opResult {
	out := make(chan opResult, 1)
	go func() { out <- runLock(r, op) }()
	return out
}

var _ = testing.Verbose

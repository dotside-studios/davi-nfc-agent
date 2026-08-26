package clientserver

import (
	"context"
	"encoding/json"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/wsconn"
)

// ops returns what performs tag operations, or a stand-in that refuses when the
// agent is not running. A client asking for a write before the agent starts
// gets an answer rather than a panic.
func (s *Server) ops() server.TagOps {
	if s.config.Ops != nil {
		return s.config.Ops
	}
	if s.config.Tags == nil {
		return stoppedOps{}
	}
	return newTagOps(s.config)
}

// stoppedOps answers every operation with the same refusal.
type stoppedOps struct{}

func (stoppedOps) refuse() error {
	return protocol.Errorf(protocol.ErrCodeInternal, "the agent is not serving tag operations")
}

func (o stoppedOps) Write(context.Context, server.WriteOp) (*nfc.WriteResult, error) {
	return nil, o.refuse()
}
func (o stoppedOps) Lock(context.Context, server.LockOp) (*nfc.LockResult, error) {
	return nil, o.refuse()
}
func (o stoppedOps) Transceive(context.Context, server.TransceiveOp) ([]byte, error) {
	return nil, o.refuse()
}
func (o stoppedOps) Capabilities(context.Context, server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	return nil, o.refuse()
}

// targetOf builds the target an operation applies to.
func targetOf(uid, deviceID string, allowUntargeted bool) server.Target {
	return server.Target{TagUID: uid, DeviceID: deviceID, AllowUntargeted: allowUntargeted}
}

// decodePayload re-decodes a request payload into a typed shape, reporting
// whether it fit.
func decodePayload(payload map[string]any, into any) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}

// reply sends a successful response to one client.
func (s *Server) reply(conn *wsconn.SafeConn, requestID, msgType string, payload any) {
	err := conn.WriteJSON(protocol.WebSocketResponse{
		ID:      requestID,
		Type:    msgType,
		Success: true,
		Payload: payload,
	})
	if err != nil {
		clientFail.Printf("Failed to send %s: %v", msgType, err)
	}
}

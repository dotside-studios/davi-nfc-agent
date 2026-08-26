package agent

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// DeviceEndpointOptions is what the agent decides about device connections,
// handed to whatever builds the endpoint. It mirrors the driver's own options
// without naming them.
type DeviceEndpointOptions struct {
	// Authenticate admits or rejects a device, writing its own rejection, and
	// names the paired device it admitted. An empty name identifies nobody.
	Authenticate func(w http.ResponseWriter, r *http.Request) (deviceID string, ok bool)

	// CheckOrigin admits or rejects an upgrade by Origin.
	CheckOrigin func(r *http.Request) bool

	// AllowTagModification reports whether writes, locks and raw exchanges are
	// currently permitted. Read-only mode gates every route to a tag, not just
	// the hardware one.
	AllowTagModification func() bool

	// PublicKeyPin is reported at registration so a device can recognise this
	// agent later without a certificate authority.
	//
	// Asked for when a device registers rather than when the endpoint is
	// built, like the three above: the pin comes from certificate material,
	// which need not be settled by the time the endpoint exists.
	PublicKeyPin func() string
}

// TagModificationAllowed reports whether the agent's mode currently permits
// writes, locks and raw exchanges.
//
// Read when the operation happens rather than when the endpoint was built: the
// endpoint outlives any particular reader, and the mode changes while running.
func (a *Agent) TagModificationAllowed() bool {
	readers := a.supervisor.Load()
	if readers == nil {
		return true
	}
	return readers.Mode() != nfc.ModeReadOnly
}

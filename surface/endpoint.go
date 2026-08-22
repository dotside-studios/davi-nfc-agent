package surface

import (
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Endpoint is one address the agent hands out: what to call it, where it is,
// and what it is for.
type Endpoint struct {
	// ID keys the endpoint. Registering the same ID again replaces it, which
	// is how an address that changes is published: the pairing URL carries a
	// PIN, and rotating the PIN is a new URL under the same ID.
	ID string

	// Label names the endpoint on the menu, such as "Device" or "Pair Phone".
	Label string

	// URL is the address itself. Empty means the server behind it is not
	// running, which the menu says rather than offering an address that
	// refuses the connection.
	URL string

	// Tooltip is the hover text, for what the address is for.
	Tooltip string
}

// Running reports whether the endpoint has an address to hand out.
func (e Endpoint) Running() bool { return e.URL != "" }

// Endpoints is the register of those addresses, in the order they were first
// registered. The device and client servers put theirs here as they start, the
// pairing server does the same for its page, and a plugin serving something of
// its own is shown beside them without the tray having to know what it is.
//
// The zero value is ready to use.
type Endpoints struct {
	mu    sync.Mutex
	order []string
	byID  map[string]Endpoint

	changed traymenu.Signal[[]Endpoint]
}

// NewEndpoints returns an empty register.
func NewEndpoints() *Endpoints { return &Endpoints{} }

// Set registers an endpoint, replacing anything already under its ID and
// keeping the place that one had. An endpoint with no ID is ignored: it could
// never be replaced or removed again.
//
// Changed is raised only when something actually differs, so a server
// republishing the same address on every restart does not redraw the menu.
func (e *Endpoints) Set(endpoint Endpoint) {
	if endpoint.ID == "" {
		return
	}

	e.mu.Lock()
	if e.byID == nil {
		e.byID = make(map[string]Endpoint)
	}
	if existing, ok := e.byID[endpoint.ID]; ok && existing == endpoint {
		e.mu.Unlock()
		return
	}
	if _, ok := e.byID[endpoint.ID]; !ok {
		e.order = append(e.order, endpoint.ID)
	}
	e.byID[endpoint.ID] = endpoint
	list := e.listLocked()
	e.mu.Unlock()

	e.changed.Emit(list)
}

// SetURL moves an endpoint to a new address, keeping its label and tooltip,
// and reports whether there was one to move. An empty url marks it as not
// running, which is what a stopped server publishes rather than disappearing
// from the menu.
func (e *Endpoints) SetURL(id, url string) bool {
	e.mu.Lock()
	endpoint, ok := e.byID[id]
	e.mu.Unlock()

	if !ok {
		return false
	}
	endpoint.URL = url
	e.Set(endpoint)
	return true
}

// Remove takes an endpoint off the register for good and reports whether there
// was one. A server that has merely stopped should publish an empty URL
// instead: an address that comes back keeps its place that way.
func (e *Endpoints) Remove(id string) bool {
	e.mu.Lock()
	_, ok := e.byID[id]
	if ok {
		delete(e.byID, id)
		for i, key := range e.order {
			if key == id {
				e.order = append(e.order[:i], e.order[i+1:]...)
				break
			}
		}
	}
	list := e.listLocked()
	e.mu.Unlock()

	if !ok {
		return false
	}
	e.changed.Emit(list)
	return true
}

// Get returns the endpoint under id, and whether there was one.
func (e *Endpoints) Get(id string) (Endpoint, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	endpoint, ok := e.byID[id]
	return endpoint, ok
}

// List returns the endpoints, in the order they were first registered.
func (e *Endpoints) List() []Endpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.listLocked()
}

// Len reports how many endpoints are registered.
func (e *Endpoints) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.order)
}

// Changed is raised whenever the register changes, carrying the whole list.
func (e *Endpoints) Changed() *traymenu.Signal[[]Endpoint] { return &e.changed }

// OnChange runs fn whenever the register changes.
func (e *Endpoints) OnChange(fn func([]Endpoint)) *traymenu.Connection {
	return e.changed.Connect(fn)
}

func (e *Endpoints) listLocked() []Endpoint {
	list := make([]Endpoint, 0, len(e.order))
	for _, id := range e.order {
		list = append(list, e.byID[id])
	}
	return list
}

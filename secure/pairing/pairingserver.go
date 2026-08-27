package pairing

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/event"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
)

// ServerOptions configures the pairing endpoint. Everything here is agent
// policy that the pairing machinery honours but does not decide.
type ServerOptions struct {
	// CA hands out the local certificate authority to a request carrying the
	// PIN. Nil is normal: an agent serving an externally provisioned
	// certificate has no authority of its own to give, and pairing works
	// regardless.
	CA tlspkg.CertificateAuthority

	// Registry is where a paired device's credential is issued. Nil serves the
	// certificate pages but reports pairing as unavailable, which is what a
	// build handing out a CA and nothing else wants.
	Registry *Registry

	// AppName is the name the pairing pages present. Empty uses the build's own
	// display name.
	AppName string

	// PublicKeyPin is what a device records to recognise this agent later, and
	// AgentPort is the port it is told to connect to afterwards.
	//
	// Both are read per pairing rather than captured: the certificate can be
	// reissued and the port changed while this endpoint stays up, and a device
	// handed a stale value cannot connect.
	PublicKeyPin func() string
	AgentPort    func() int
}

// Server runs pairing and certificate distribution. It is the machinery half of
// the paired-device manager: what issues a credential, as opposed to what
// checks one.
//
// The same server can be mounted on a listener that already exists, with
// [Server.PairHandler], or bind one of its own with [Server.Listen]. Real builds do both: /pair belongs on the agent's TLS
// listener, since it issues a durable credential and the key pin that covers
// it, while the certificate pages must be reachable in the clear by a device
// that does not trust that certificate yet.
type Server struct {
	bootstrap *tlspkg.BootstrapServer
	opts      ServerOptions

	// rotated reports a fresh PIN, so everything displaying one follows a
	// rotation made anywhere else. The tray and the console each show it.
	rotated event.Signal[string]
}

// NewServer builds the pairing server. It binds nothing until Listen.
func NewServer(opts ServerOptions) *Server {
	// Port 0: Listen sets it, and a Server that is only mounted never listens.
	bootstrap := tlspkg.NewBootstrapServer(opts.CA, 0)

	if opts.AppName != "" {
		bootstrap.SetAppName(opts.AppName)
	}
	if opts.Registry != nil {
		bootstrap.SetPairingIssuer(NewPairingIssuer(opts.Registry, opts.PublicKeyPin), opts.AgentPort)
	}

	return &Server{bootstrap: bootstrap, opts: opts}
}

// UsePort sets the port a paired device is told to connect to afterwards, for a
// build that learns it after assembling this. Read per pairing either way.
func (s *Server) UsePort(agentPort func() int) {
	if s == nil {
		return
	}
	s.opts.AgentPort = agentPort
	if s.opts.Registry != nil {
		s.bootstrap.SetPairingIssuer(NewPairingIssuer(s.opts.Registry, s.opts.PublicKeyPin), agentPort)
	}
}

// PairHandler serves pairing, to mount on a listener already serving the
// certificate that this agent's key pin covers.
//
// It refuses a cleartext connection: the response carries a durable credential
// an observer would read, and a key pin an active attacker could substitute.
func (s *Server) PairHandler() http.Handler {
	if s == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Pairing is not enabled on this agent.", http.StatusNotImplemented)
		})
	}
	return s.bootstrap.PairHandler()
}

// Listen binds the certificate-distribution listener on port.
//
// Cleartext on purpose: it hands the certificate authority to a device that
// does not trust the agent's certificate yet. Pairing itself is not served from
// it; see [Server.PairHandler].
func (s *Server) Listen(port int) error {
	if s == nil {
		return nil
	}
	s.bootstrap.SetPort(port)
	return s.bootstrap.Start()
}

// Stop closes the listener Listen bound, if any.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	s.bootstrap.Stop()
}

// PIN is the code a device must present to pair, empty on a nil server.
func (s *Server) PIN() string {
	if s == nil {
		return ""
	}
	return s.bootstrap.PIN()
}

// RotatePIN issues a fresh PIN and returns it, invalidating the pairing URLs
// carrying the old one.
func (s *Server) RotatePIN() string {
	if s == nil {
		return ""
	}
	fresh := s.bootstrap.RotatePIN()
	s.rotated.Emit(fresh)
	return fresh
}

// OnPINChange registers fn to run after each rotation, with the fresh PIN. The
// connection it returns removes it.
//
// Whoever displays the PIN follows this rather than the control that rotated
// it: a rotation from the tray reaches an open console page, and one from the
// console relabels the tray.
func (s *Server) OnPINChange(fn func(pin string)) *event.Connection {
	if s == nil {
		return nil
	}
	return s.rotated.Connect(fn)
}

// Bootstrap exposes the underlying server, for a caller that needs the pairing
// URI or the certificate pages directly. Nil on a nil server.
func (s *Server) Bootstrap() *tlspkg.BootstrapServer {
	if s == nil {
		return nil
	}
	return s.bootstrap
}

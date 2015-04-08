package server

import (
	"github.com/brutella/hc/db"
	"github.com/brutella/hc/model/container"
	"github.com/brutella/hc/netio"
	"github.com/brutella/hc/netio/controller"
	"github.com/brutella/hc/netio/endpoint"
	"github.com/brutella/hc/netio/pair"

	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
)

// Server provides a similar interfaces as http.Server to start and stop a TCP server.
type Server interface {
	// ListenAndServe start the server
	ListenAndServe() error

	// Port returns the port on which the server listens to
	Port() string

	// OnStop calls the function when the server stops
	OnStop(fn ExitFunc)

	// Stop stops the server
	Stop()
}

// ExitFunc is the function which is invoked when the server shuts down.
type ExitFunc func()

type hkServer struct {
	context  netio.HAPContext
	database db.Database
	bridge   *netio.Bridge
	mux      *http.ServeMux

	exitFunc ExitFunc

	mutex     *sync.Mutex
	container *container.Container

	port     string
	listener *net.TCPListener
}

// NewServer returns a server
func NewServer(ctx netio.HAPContext, d db.Database, c *container.Container, b *netio.Bridge, mutex *sync.Mutex) Server {
	// os gives us a free Port when Port is ""
	ln, err := net.Listen("tcp", "")
	if err != nil {
		log.Fatal(err)
	}
	port := ExtractPort(ln.Addr())

	s := hkServer{
		context:   ctx,
		database:  d,
		container: c,
		bridge:    b,
		mux:       http.NewServeMux(),
		mutex:     mutex,
		listener:  ln.(*net.TCPListener),
		port:      port,
	}

	s.setupEndpoints()

	return &s
}

func (s *hkServer) OnStop(fn ExitFunc) {
	s.exitFunc = fn
}

func (s *hkServer) ListenAndServe() error {
	s.teardownOnStop()

	return s.listenAndServe(s.addrString(), s.mux, s.context)
}

func (s *hkServer) Stop() {
	for _, c := range s.context.ActiveConnections() {
		c.Close()
	}

	if s.exitFunc != nil {
		s.exitFunc()
	}
}

func (s *hkServer) Port() string {
	return s.port
}

// dnssdCommand returns a dns-sd command string to publish the server via dns-sd on OS X
func (s *hkServer) dnssdCommand() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("dns-sd -P %s _hap local %s %s 192.168.0.14 pv=1.0 id=%s c#=1 s#=1 sf=1 ff=0 md=%s\n", s.bridge.Name(), s.port, hostname, s.bridge.ID(), s.bridge.Name())
}

// listenAndServe returns a http.Server to listen on a specific address
func (s *hkServer) listenAndServe(addr string, handler http.Handler, context netio.HAPContext) error {
	server := http.Server{Addr: addr, Handler: handler}
	// Use a HAPTCPListener
	listener := netio.NewHAPTCPListener(s.listener, context)
	return server.Serve(listener)
}

// teardownOnStop calls Stop on interrupt or kill signals
func (s *hkServer) teardownOnStop() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, os.Kill)

	go func() {
		select {
		case <-c:
			log.Println("[INFO] Teardown server")
			s.Stop()
			os.Exit(1)
		}
	}()
}

func (s *hkServer) addrString() string {
	return ":" + s.port
}

// setupEndpoints creates controller objects to handle HAP endpoints
func (s *hkServer) setupEndpoints() {
	containerController := controller.NewContainerController(s.container)
	characteristicsController := controller.NewCharacteristicController(s.container)
	pairingController := pair.NewPairingController(s.database)

	s.mux.Handle("/pair-setup", endpoint.NewPairSetup(s.bridge, s.database, s.context))
	s.mux.Handle("/pair-verify", endpoint.NewPairVerify(s.context, s.database))
	s.mux.Handle("/accessories", endpoint.NewAccessories(containerController, s.mutex))
	s.mux.Handle("/characteristics", endpoint.NewCharacteristics(characteristicsController, s.mutex))
	s.mux.Handle("/pairings", endpoint.NewPairing(pairingController))
	s.mux.Handle("/identify", endpoint.NewIdentify())
}

package main

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/kavu/go_reuseport"
	"github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/pkg/random"
	"github.com/flynn/flynn/pkg/tlsconfig"
	"github.com/flynn/flynn/router/proxy"
	"github.com/flynn/flynn/router/types"
	"golang.org/x/net/context"
)

type HTTPListener struct {
	Watcher
	DataStoreReader

	Addr    string
	TLSAddr string

	mtx      sync.RWMutex
	domains  map[string]*httpRoute
	routes   map[string]*httpRoute
	services map[string]*httpService

	discoverd DiscoverdClient
	ds        DataStore
	wm        *WatchManager

	listener    net.Listener
	tlsListener net.Listener
	closed      bool
	cookieKey   *[32]byte
	keypair     tls.Certificate
}

type DiscoverdClient interface {
	NewServiceSet(string) (discoverd.ServiceSet, error)
}

func (s *HTTPListener) Close() error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	for _, service := range s.services {
		service.ss.Close()
	}
	s.listener.Close()
	s.tlsListener.Close()
	s.ds.StopSync()
	s.closed = true
	return nil
}

func (s *HTTPListener) Start() error {
	if s.Watcher != nil {
		return errors.New("router: http listener already started")
	}
	if s.wm == nil {
		s.wm = NewWatchManager()
	}
	s.Watcher = s.wm

	if s.ds == nil {
		return errors.New("router: http listener missing data store")
	}
	s.DataStoreReader = s.ds

	s.routes = make(map[string]*httpRoute)
	s.domains = make(map[string]*httpRoute)
	s.services = make(map[string]*httpService)

	if s.cookieKey == nil {
		s.cookieKey = &[32]byte{}
	}

	started := make(chan error)

	go s.ds.Sync(&httpSyncHandler{l: s}, started)
	if err := <-started; err != nil {
		return err
	}

	go s.listenAndServe(started)
	if err := <-started; err != nil {
		s.ds.StopSync()
		return err
	}
	s.Addr = s.listener.Addr().String()

	go s.listenAndServeTLS(started)
	if err := <-started; err != nil {
		s.ds.StopSync()
		s.listener.Close()
		return err
	}
	s.TLSAddr = s.tlsListener.Addr().String()

	return nil
}

var ErrClosed = errors.New("router: listener has been closed")

func (s *HTTPListener) AddRoute(r *router.Route) error {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if s.closed {
		return ErrClosed
	}
	r.ID = md5sum(r.HTTPRoute().Domain)
	return s.ds.Add(r)
}

func (s *HTTPListener) SetRoute(r *router.Route) error {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if s.closed {
		return ErrClosed
	}
	r.ID = md5sum(r.HTTPRoute().Domain)
	return s.ds.Set(r)
}

func md5sum(data string) string {
	digest := md5.Sum([]byte(data))
	return hex.EncodeToString(digest[:])
}

func (s *HTTPListener) RemoveRoute(id string) error {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if s.closed {
		return ErrClosed
	}
	return s.ds.Remove(id)
}

type httpSyncHandler struct {
	l *HTTPListener
}

func (h *httpSyncHandler) Set(data *router.Route) error {
	route := data.HTTPRoute()
	r := &httpRoute{HTTPRoute: route}

	if r.TLSCert != "" && r.TLSKey != "" {
		kp, err := tls.X509KeyPair([]byte(r.TLSCert), []byte(r.TLSKey))
		if err != nil {
			return err
		}
		r.keypair = &kp
		r.TLSCert = ""
		r.TLSKey = ""
	}

	h.l.mtx.Lock()
	defer h.l.mtx.Unlock()
	if h.l.closed {
		return nil
	}

	service := h.l.services[r.Service]
	if service != nil && service.name != r.Service {
		service.refs--
		if service.refs <= 0 {
			service.ss.Close()
			delete(h.l.services, service.name)
		}
		service = nil
	}
	if service == nil {
		ss, err := h.l.discoverd.NewServiceSet(r.Service)
		if err != nil {
			return err
		}

		service = &httpService{name: r.Service, ss: ss}
		if r.Sticky {
			service.proxy = proxy.StickyProxy(ss.Addrs(), *h.l.cookieKey)
		} else {
			service.proxy = proxy.DynamicProxy(ss.Addrs())
		}
		h.l.services[r.Service] = service
	}
	service.refs++
	r.service = service
	h.l.routes[data.ID] = r
	h.l.domains[strings.ToLower(r.Domain)] = r

	go h.l.wm.Send(&router.Event{Event: "set", ID: r.Domain})
	return nil
}

func (h *httpSyncHandler) Remove(id string) error {
	h.l.mtx.Lock()
	defer h.l.mtx.Unlock()
	if h.l.closed {
		return nil
	}
	r, ok := h.l.routes[id]
	if !ok {
		return ErrNotFound
	}

	r.service.refs--
	if r.service.refs <= 0 {
		r.service.ss.Close()
		delete(h.l.services, r.service.name)
	}

	delete(h.l.routes, id)
	delete(h.l.domains, r.Domain)
	go h.l.wm.Send(&router.Event{Event: "remove", ID: id})
	return nil
}

func (s *HTTPListener) listenAndServe(started chan<- error) {
	_, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		started <- err
		return
	}
	s.listener, err = reuseport.NewReusablePortListener("tcp4", s.Addr)
	started <- err
	if err != nil {
		return
	}

	server := &http.Server{
		Addr: s.Addr,
		Handler: fwdProtoHandler{
			Handler: s,
			Proto:   "http",
			Port:    port,
		},
	}

	// TODO: log error
	_ = server.Serve(s.listener)
}

var errMissingTLS = errors.New("router: route not found or TLS not configured")

func (s *HTTPListener) listenAndServeTLS(started chan<- error) {
	_, port, err := net.SplitHostPort(s.TLSAddr)
	if err != nil {
		started <- err
		return
	}
	certForHandshake := func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		r := s.findRouteForHost(hello.ServerName)
		if r == nil {
			return nil, errMissingTLS
		}
		return r.keypair, nil
	}
	tlsConfig := tlsconfig.SecureCiphers(&tls.Config{
		GetCertificate: certForHandshake,
		Certificates:   []tls.Certificate{s.keypair},
	})

	server := &http.Server{
		Addr: s.TLSAddr,
		Handler: fwdProtoHandler{
			Handler: s,
			Proto:   "https",
			Port:    port,
		},
	}

	l, err := reuseport.NewReusablePortListener("tcp4", s.TLSAddr)
	started <- err
	if err != nil {
		return
	}
	s.tlsListener = tls.NewListener(l, tlsConfig)
	// TODO: log error
	_ = server.Serve(s.tlsListener)
}

func (s *HTTPListener) findRouteForHost(host string) *httpRoute {
	host = strings.ToLower(host)
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if backend, ok := s.domains[host]; ok {
		return backend
	}
	// handle wildcard domains up to 5 subdomains deep, from most-specific to
	// least-specific
	d := strings.SplitN(host, ".", 5)
	for i := len(d); i > 0; i-- {
		if backend, ok := s.domains["*."+strings.Join(d[len(d)-i:], ".")]; ok {
			return backend
		}
	}
	return nil
}

func failAndClose(w http.ResponseWriter, code int) {
	w.Header().Set("Connection", "close")
	fail(w, code)
}

func fail(w http.ResponseWriter, code int) {
	msg := []byte(http.StatusText(code) + "\n")
	w.Header().Set("Content-Length", strconv.Itoa(len(msg)))
	w.WriteHeader(code)
	w.Write(msg)
}

const hdrUseStickySessions = "Flynn-Use-Sticky-Sessions"

func (s *HTTPListener) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r := s.findRouteForHost(req.Host)
	if r == nil {
		fail(w, 404)
		return
	}

	// TODO(bgentry): find a better way to access this setting in the service
	// where it's needed.
	stickyValue := "false"
	if r.Sticky {
		stickyValue = "true"
	}
	req.Header.Set(hdrUseStickySessions, stickyValue)

	r.service.ServeHTTP(w, req)
}

// A domain served by a listener, associated TLS certs,
// and link to backend service set.
type httpRoute struct {
	*router.HTTPRoute

	keypair *tls.Certificate
	service *httpService
}

// A service definition: name, and set of backends.
type httpService struct {
	name string
	ss   discoverd.ServiceSet
	refs int

	proxy proxy.Proxy
}

func (s *httpService) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.Header.Set("X-Request-Start", strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10))
	req.Header.Set("X-Request-Id", random.UUID())

	// Pass the Request-URI verbatim without any modifications
	req.URL.Opaque = strings.Split(strings.TrimPrefix(req.RequestURI, req.URL.Scheme+":"), "?")[0]

	//TODO(benburkert): only call this when the ss set is updated, instead of every request
	s.proxy.UpdateBackends(s.ss.Addrs())

	if isWebSocketUpgrade(req) {
		s.proxy.ServeWebSocket(context.TODO(), w, req)
	} else {
		s.proxy.ServeHTTP(context.TODO(), w, req)
	}
}

func isWebSocketUpgrade(req *http.Request) bool {
	return strings.ToLower(req.Header.Get("Connection")) == "upgrade" &&
		strings.ToLower(req.Header.Get("Upgrade")) == "websocket"
}

package main

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/kavu/go_reuseport"
	"github.com/flynn/flynn/Godeps/_workspace/src/golang.org/x/net/context"
	"github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/pkg/ctxhelper"
	"github.com/flynn/flynn/pkg/random"
	"github.com/flynn/flynn/pkg/tlsconfig"
	"github.com/flynn/flynn/router/proxy"
	"github.com/flynn/flynn/router/types"
)

type HTTPListener struct {
	Watcher
	DataStoreReader

	Addr    string
	TLSAddr string

	mtx        sync.Mutex
	routeTable *httpRouteTable

	discoverd DiscoverdClient
	ds        DataStore
	wm        *WatchManager
	stopSync  func()

	listener    net.Listener
	tlsListener net.Listener
	closed      bool
	cookieKey   *[32]byte
	keypair     tls.Certificate
}

type DiscoverdClient interface {
	Service(string) discoverd.Service
}

func (s *HTTPListener) Close() error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.stopSync()
	s.routeTable.Close()
	s.listener.Close()
	s.tlsListener.Close()
	s.closed = true
	return nil
}

func (s *HTTPListener) Start() error {
	ctx := context.Background() // TODO(benburkert): make this an argument
	ctx, s.stopSync = context.WithCancel(ctx)

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

	if s.cookieKey == nil {
		s.cookieKey = &[32]byte{}
	}

	s.routeTable = &httpRouteTable{
		routes:    make(map[string]*httpRoute),
		domains:   make(map[string]*httpRoute),
		services:  make(map[string]*httpService),
		discoverd: s.discoverd,
		wm:        s.wm,
		cookieKey: s.cookieKey,
	}

	if err := s.startSync(ctx); err != nil {
		return err
	}

	if err := s.startListen(); err != nil {
		s.stopSync()
		return err
	}

	return nil
}

func (s *HTTPListener) startSync(ctx context.Context) error {
	startc, errc := make(chan struct{}), make(chan error)

	go func() { errc <- s.ds.Sync(ctx, s.routeTable, startc) }()

	select {
	case err := <-errc:
		return err
	case <-startc:
		go s.runSync(ctx, errc)
		return nil
	}
}

func (s *HTTPListener) runSync(ctx context.Context, errc chan error) {
	err := <-errc

	for {
		if err == nil {
			return
		}
		log.Printf("router: sync error: %s", err)

		rt := &httpRouteTable{
			routes:    make(map[string]*httpRoute),
			domains:   make(map[string]*httpRoute),
			services:  make(map[string]*httpService),
			discoverd: s.discoverd,
			wm:        s.wm,
		}
		for k, v := range s.routeTable.services {
			rt.services[k] = v
		}

		startc := make(chan struct{})
		go func() { errc <- s.ds.Sync(ctx, rt, startc) }()

		select {
		case err = <-errc:
			continue
		case <-startc:
			s.mtx.Lock()
			s.routeTable = rt
			s.mtx.Unlock()
		}

		err = <-errc

	}
}

func (s *HTTPListener) startListen() error {
	if err := s.listenAndServe(); err != nil {
		return err
	}
	s.Addr = s.listener.Addr().String()

	if err := s.listenAndServeTLS(); err != nil {
		s.listener.Close()
		return err
	}
	s.TLSAddr = s.tlsListener.Addr().String()

	return nil
}

var ErrClosed = errors.New("router: listener has been closed")

func (s *HTTPListener) AddRoute(r *router.Route) error {
	if s.closed {
		return ErrClosed
	}
	return s.ds.Add(r)
}

func (s *HTTPListener) UpdateRoute(r *router.Route) error {
	if s.closed {
		return ErrClosed
	}
	return s.ds.Update(r)
}

func md5sum(data string) string {
	digest := md5.Sum([]byte(data))
	return hex.EncodeToString(digest[:])
}

func (s *HTTPListener) RemoveRoute(id string) error {
	if s.closed {
		return ErrClosed
	}
	return s.ds.Remove(id)
}

func (s *HTTPListener) listenAndServe() error {
	var err error
	s.listener, err = reuseport.NewReusablePortListener("tcp4", s.Addr)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr: s.listener.Addr().String(),
		Handler: fwdProtoHandler{
			Handler: s,
			Proto:   "http",
			Port:    mustPortFromAddr(s.listener.Addr().String()),
		},
	}

	// TODO: log error
	go server.Serve(s.listener)
	return nil
}

var errMissingTLS = errors.New("router: route not found or TLS not configured")

func (s *HTTPListener) listenAndServeTLS() error {
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

	l, err := reuseport.NewReusablePortListener("tcp4", s.TLSAddr)
	if err != nil {
		return err
	}
	s.tlsListener = tls.NewListener(l, tlsConfig)

	server := &http.Server{
		Addr: s.tlsListener.Addr().String(),
		Handler: fwdProtoHandler{
			Handler: s,
			Proto:   "https",
			Port:    mustPortFromAddr(s.tlsListener.Addr().String()),
		},
	}

	// TODO: log error
	go server.Serve(s.tlsListener)
	return nil
}

func (s *HTTPListener) findRouteForHost(host string) *httpRoute {
	s.mtx.Lock()
	rt := s.routeTable
	s.mtx.Unlock()

	return rt.Lookup(host)
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

func (s *HTTPListener) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx := context.Background()
	ctx = ctxhelper.NewContextStartTime(ctx, time.Now())
	r := s.findRouteForHost(req.Host)
	if r == nil {
		fail(w, 404)
		return
	}

	r.service.ServeHTTP(ctx, w, req)
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
	sc   DiscoverdServiceCache
	refs int

	rp *proxy.ReverseProxy
}

func (s *httpService) ServeHTTP(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	start, _ := ctxhelper.StartTimeFromContext(ctx)
	req.Header.Set("X-Request-Start", strconv.FormatInt(start.UnixNano()/int64(time.Millisecond), 10))
	req.Header.Set("X-Request-Id", random.UUID())

	s.rp.ServeHTTP(w, req)
}

func mustPortFromAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	return port
}

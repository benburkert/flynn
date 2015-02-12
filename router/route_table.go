package main

import (
	"crypto/tls"
	"net"
	"strings"
	"sync"

	"github.com/flynn/flynn/router/proxy"
	"github.com/flynn/flynn/router/types"
)

type httpRouteTable struct {
	sync.RWMutex

	// domain name to httpRoute
	domains map[string]*httpRoute
	// route id to httpRoute
	routes map[string]*httpRoute
	// service name to httpService
	services map[string]*httpService

	// TODO(benburkert): refactor these to somewhere else
	discoverd DiscoverdClient
	cookieKey *[32]byte
	wm        *WatchManager
}

func (t *httpRouteTable) Set(data *router.Route) error {
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

	t.Lock()
	defer t.Unlock()

	service := t.services[r.Service]
	if service != nil && service.name != r.Service {
		service.refs--
		if service.refs <= 0 {
			service.sc.Close()
			delete(t.services, service.name)
		}
		service = nil
	}
	if service == nil {
		sc, err := NewDiscoverdServiceCache(t.discoverd.Service(r.Service))
		if err != nil {
			return err
		}
		service = &httpService{
			name: r.Service,
			sc:   sc,
			rp:   proxy.NewReverseProxy(sc.Addrs, t.cookieKey, r.Sticky),
		}
		t.services[r.Service] = service
	}
	service.refs++
	r.service = service
	t.routes[data.ID] = r
	t.domains[strings.ToLower(r.Domain)] = r

	go t.wm.Send(&router.Event{Event: "set", ID: r.Domain})
	return nil
}

func (t *httpRouteTable) Remove(id string) error {
	t.Lock()
	defer t.Unlock()

	r, ok := t.routes[id]
	if !ok {
		return ErrNotFound
	}

	r.service.refs--
	if r.service.refs <= 0 {
		r.service.sc.Close()
		delete(t.services, r.service.name)
	}

	delete(t.routes, id)
	delete(t.domains, r.Domain)
	go t.wm.Send(&router.Event{Event: "remove", ID: id})
	return nil
}

func (t *httpRouteTable) Lookup(host string) *httpRoute {
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		host, _, _ = net.SplitHostPort(host)
	}
	t.RLock()
	defer t.RUnlock()
	if backend, ok := t.domains[host]; ok {
		return backend
	}
	// handle wildcard domains up to 5 subdomains deep, from most-specific to
	// least-specific
	d := strings.SplitN(host, ".", 5)
	for i := len(d); i > 0; i-- {
		if backend, ok := t.domains["*."+strings.Join(d[len(d)-i:], ".")]; ok {
			return backend
		}
	}
	return nil
}

func (t *httpRouteTable) Close() {
	t.Lock()
	defer t.Unlock()
	for _, service := range t.services {
		service.sc.Close()
	}
}

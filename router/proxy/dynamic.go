package proxy

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/flynn/flynn/pkg/random"
	"golang.org/x/net/context"
)

type Dynamic struct {
	*ReverseProxy

	mu       sync.RWMutex
	backends []string
}

var (
	httpTransport = &http.Transport{
		Dial:                dynamicDial,
		TLSHandshakeTimeout: 10 * time.Second, // unused, but safer to leave default in place
	}

	dialer = &net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: 30 * time.Second,
	}
)

func NewDynamic(backends []string) *Dynamic {
	prox := &Dynamic{backends: backends}
	prox.ReverseProxy = &ReverseProxy{
		Transport:        prox,
		RequestDirector:  stripRequestScheme,
		ResponseDirector: responseNop,
	}
	return prox
}

func (d *Dynamic) UpdateBackends(backends []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.backends = backends
}

func (d *Dynamic) RoundTripHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	d.mu.RLock()
	backends := make([]string, len(d.backends))
	copy(backends, d.backends)
	d.mu.RUnlock()

	shuffle(backends)

	return redialRoundTrip(backends, req)
}

func redialRoundTrip(backends []string, req *http.Request) (*http.Response, error) {
	//TODO(benburkert): instead of ranging over the backends once, this could
	//detect changes to the backend list and update the backends list accordingly.
	for _, backend := range backends {
		req.URL.Host = backend
		res, err := httpTransport.RoundTrip(req)
		if err == nil {
			return res, nil
		}
		if _, ok := err.(*dialErr); !ok {
			return nil, err
		}
		// retry, maybe log a message about it
	}
	return nil, errors.New("router: no backends available")
}

func shuffle(s []string) {
	for i := len(s) - 1; i > 0; i-- {
		j := random.Math.Intn(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

func stripRequestScheme(_ context.Context, req *http.Request) {
	// the inbound scheme may be http or https, but only http is supported on
	// the backend.
	req.URL.Scheme = "http"
}

func responseNop(_ context.Context, _ *http.Response) {}

func dynamicDial(network, addr string) (net.Conn, error) {
	conn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, dialErr{err}
	}
	return conn, nil
}

type dialErr struct {
	error
}

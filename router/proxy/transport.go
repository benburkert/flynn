package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/golang.org/x/crypto/nacl/secretbox"
	"github.com/flynn/flynn/pkg/random"
	"golang.org/x/net/context"
)

var (
	httpTransport = &http.Transport{
		Dial:                customDial,
		TLSHandshakeTimeout: 10 * time.Second, // unused, but safer to leave default in place
	}

	dialer = &net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: 30 * time.Second,
	}
)

type transport struct {
	mu       sync.RWMutex
	backends []string
}

func (t *transport) UpdateBackends(backends []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.backends = backends
}

func (t *transport) RoundTripHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	backends := make([]string, len(t.backends))
	copy(backends, t.backends)
	t.mu.RUnlock()

	shuffle(backends)
	return roundTripHTTP(backends, req)
}

func roundTripHTTP(backends []string, req *http.Request) (*http.Response, error) {
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

func customDial(network, addr string) (net.Conn, error) {
	conn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, dialErr{err}
	}
	return conn, nil
}

type dialErr struct {
	error
}

type stickyTransport struct {
	*transport

	cookieKey [32]byte
}

const (
	stickyCookie = "_backend"
)

func (t *stickyTransport) RoundTripHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	backends := make([]string, len(t.backends))
	copy(backends, t.backends)
	t.mu.RUnlock()

	shuffle(backends)

	stickyBackend := t.getStickyCookieBackend(req)
	if stickyBackend != "" {
		swapToFront(backends, stickyBackend)
	}

	res, err := roundTripHTTP(backends, req)
	if err != nil {
		return res, err
	}

	if stickyBackend != res.Request.URL.Host {
		t.setStickyCookieBackend(res, stickyBackend)
	}

	return res, nil
}

func (t *stickyTransport) getStickyCookieBackend(req *http.Request) string {
	cookie, err := req.Cookie(stickyCookie)
	if err != nil {
		return ""
	}

	data, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return ""
	}
	return string(t.decrypt(data))
}

func (t *stickyTransport) setStickyCookieBackend(res *http.Response, backend string) {
	cookie := http.Cookie{
		Name:  stickyCookie,
		Value: base64.StdEncoding.EncodeToString(t.encrypt([]byte(backend))),
		Path:  "/",
	}
	res.Header.Add("Set-Cookie", cookie.String())
}

func (t *stickyTransport) encrypt(data []byte) []byte {
	var nonce [24]byte
	_, err := io.ReadFull(rand.Reader, nonce[:])
	if err != nil {
		panic(err)
	}

	out := make([]byte, len(nonce), len(nonce)+len(data)+secretbox.Overhead)
	copy(out, nonce[:])
	return secretbox.Seal(out, data, &nonce, &t.cookieKey)
}

func (t *stickyTransport) decrypt(data []byte) []byte {
	var nonce [24]byte
	if len(data) < len(nonce) {
		return []byte{}
	}
	copy(nonce[:], data)
	res, ok := secretbox.Open(nil, data[len(nonce):], &nonce, &t.cookieKey)
	if !ok {
		return []byte{}
	}
	return res
}

func swapToFront(ss []string, s string) {
	for i := range ss {
		if ss[i] == s {
			ss[0], ss[i] = ss[i], ss[0]
			return
		}
	}
}

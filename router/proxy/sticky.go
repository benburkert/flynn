package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"sync"

	"github.com/flynn/flynn/Godeps/_workspace/src/golang.org/x/crypto/nacl/secretbox"

	"golang.org/x/net/context"
)

type key int

const (
	stickyCookie     = "_backend"
	backendKey   key = 0
)

type Sticky struct {
	*ReverseProxy

	mu       sync.RWMutex
	backends []string

	cookieKey [32]byte
}

func NewSticky(backends []string, cookieKey [32]byte) *Sticky {
	prox := &Sticky{
		backends:  backends,
		cookieKey: cookieKey,
	}
	prox.ReverseProxy = &ReverseProxy{
		Transport:        prox,
		RequestDirector:  stripRequestScheme,
		ResponseDirector: prox.setBackendCookie,
	}
	return prox
}

func (s *Sticky) ServeHTTP(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	if stickyAddr := s.stickyCookieAddr(req); stickyAddr != "" {
		ctx = context.WithValue(ctx, backendKey, stickyAddr)
	}
	s.ReverseProxy.ServeHTTP(ctx, w, req)
}

func (s *Sticky) RoundTrip(ctx context.Context, req *http.Request) (*http.Response, error) {
	s.mu.RLock()
	backends := make([]string, len(s.backends))
	copy(backends, s.backends)
	s.mu.RUnlock()

	shuffle(backends)
	if stickyBackend, ok := ctx.Value(backendKey).(string); ok {
		swapToFront(backends, stickyBackend)
	}

	return redialRoundTrip(backends, req)
}

func (s *Sticky) stickyCookieAddr(req *http.Request) string {
	cookie, err := req.Cookie(stickyCookie)
	if err != nil {
		return ""
	}

	data, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return ""
	}
	var nonce [24]byte
	if len(data) < len(nonce) {
		return ""
	}
	copy(nonce[:], data)
	res, ok := secretbox.Open(nil, data[len(nonce):], &nonce, &s.cookieKey)
	if !ok {
		return ""
	}
	return string(res)
}

func (s *Sticky) setBackendCookie(ctx context.Context, res *http.Response) {
	backend := res.Request.URL.Host
	if addr, ok := ctx.Value(backendKey).(string); ok && backend == addr {
		return
	}

	res.Header.Add("Set-Cookie", s.backendCookie(backend))
}

func (s *Sticky) backendCookie(backend string) string {
	cookie := http.Cookie{
		Name:  stickyCookie,
		Value: base64.StdEncoding.EncodeToString(s.encrypt([]byte(backend))),
		Path:  "/",
	}
	return cookie.String()
}

func (s *Sticky) encrypt(data []byte) []byte {
	var nonce [24]byte
	_, err := io.ReadFull(rand.Reader, nonce[:])
	if err != nil {
		panic(err)
	}

	out := make([]byte, len(nonce), len(nonce)+len(data)+secretbox.Overhead)
	copy(out, nonce[:])
	return secretbox.Seal(out, data, &nonce, &s.cookieKey)
}

func swapToFront(ss []string, s string) {
	for i := range ss {
		if ss[i] == s {
			ss[0], ss[i] = ss[i], ss[0]
			return
		}
	}
}

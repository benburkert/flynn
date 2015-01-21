package main

import (
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/context"
)

const (
	fwdForHeaderName   = "X-Forwarded-For"
	fwdProtoHeaderName = "X-Forwarded-Proto"
	fwdPortHeaderName  = "X-Forwarded-Port"
)

// Handler is the http.Handler interface modifed to include a context argument.
type Handler interface {
	ServeHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request)
}

// HandlerFunc is the equivalent http.HandlerFunc type for the router Handler.
type HandlerFunc func(context.Context, http.ResponseWriter, *http.Request)

// ServeHTTP calls f(ctx, w, r).
func (f HandlerFunc) ServeHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	f(ctx, w, r)
}

// ServerHandler returns a http.Handler for use with a http.Server object.
func ServerHandler(ctx context.Context, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(ctx, w, r)
	}
}

// fwdProtoHandler is an http.Handler that sets the X-Forwarded-For header on
// inbound requests to match the remote IP address, and sets X-Forwarded-Proto
// and X-Forwarded-Port headers to match the values in Proto and Port. If those
// headers already exist, the new values will be appended.
type fwdProtoHandler struct {
	Handler
	Proto string
	Port  string
}

func (h fwdProtoHandler) ServeHTTP(c context.Context, w http.ResponseWriter, r *http.Request) {
	// If we aren't the first proxy retain prior X-Forwarded-* information as a
	// comma+space separated list and fold multiple headers into one.
	if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if prior, ok := r.Header[fwdForHeaderName]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		r.Header.Set(fwdForHeaderName, clientIP)
	}

	proto, port := h.Proto, h.Port
	if prior, ok := r.Header[fwdProtoHeaderName]; ok {
		proto = strings.Join(prior, ", ") + ", " + proto
	}
	if prior, ok := r.Header[fwdPortHeaderName]; ok {
		port = strings.Join(prior, ", ") + ", " + port
	}
	r.Header.Set(fwdProtoHeaderName, proto)
	r.Header.Set(fwdPortHeaderName, port)

	h.Handler.ServeHTTP(c, w, r)
}

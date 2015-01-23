// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP reverse proxy handler

package proxy

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"
)

// onExitFlushLoop is a callback set by tests to detect the state of the
// flushLoop() goroutine.
var onExitFlushLoop func()

// ReverseProxy is an HTTP Handler that takes an incoming request and
// sends it to another server, proxying the response back to the
// client.
type ReverseProxy struct {
	// The transport used to perform proxy requests.
	Transport Transporter

	// FlushInterval specifies the flush interval
	// to flush to the client while copying the
	// response body.
	// If zero, no periodic flushing is done.
	FlushInterval time.Duration

	// ErrorLog specifies an optional logger for errors
	// that occur when attempting to proxy the request.
	// If nil, logging goes to os.Stderr via the log package's
	// standard logger.
	ErrorLog *log.Logger
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var (
	httpHopHeaders = []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te", // canonicalized version of "TE"
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}

	// retain "Connection" & "Upgrade" for the WebSocket protocol handshake.
	wsHopHeaders = []string{
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te", // canonicalized version of "TE"
		"Trailers",
		"Transfer-Encoding",
	}
)

func (p *ReverseProxy) ServeHTTP(ctx context.Context, rw http.ResponseWriter, req *http.Request) {
	transport := p.Transport
	if transport == nil {
		panic("router: nil transport for proxy")
	}

	outreq := prepRequest(req, httpHopHeaders)

	res, err := transport.RoundTripHTTP(ctx, outreq)
	if err != nil {
		p.logf("http: proxy error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer res.Body.Close()

	p.writeResponse(rw, res, httpHopHeaders)
}

func (p *ReverseProxy) ServeWebSocket(ctx context.Context, rw http.ResponseWriter, req *http.Request) {
	transport := p.Transport
	if transport == nil {
		panic("router: nil transport for proxy")
	}

	outreq := prepRequest(req, wsHopHeaders)

	res, uconn, ubufrw, err := transport.ConnectWebSocket(ctx, outreq)
	if err != nil {
		p.logf("ws: proxy error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer uconn.Close()

	if !webSocketHandshakeSuccess(res) {
		// Handshake was not successful, not going to reuse this connection.
		rw.Header().Set("Connection", "close")

		p.writeResponse(rw, res, wsHopHeaders)
		res.Body.Close()

		return
	}

	p.writeResponse(rw, res, wsHopHeaders)
	res.Body.Close()

	hj, ok := rw.(http.Hijacker)
	if !ok {
		p.logf("ws: hijack failed")
		return
	}
	dconn, dbufrw, err := hj.Hijack()
	if err != nil {
		p.logf("ws: hijack failed: %v", err)
		return
	}
	defer dconn.Close()

	splice(ubufrw, dbufrw)
}

func splice(a, b *bufio.ReadWriter) {
	errc := make(chan error, 2)
	cp := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		errc <- err
	}

	go cp(a, b)
	go cp(b, a)

	timer := time.NewTimer(time.Second)
	for {
		select {
		case <-errc:
			<-errc
			return
		case <-timer.C:
			erra, errb := a.Flush(), b.Flush()
			if erra != nil || errb != nil {
				panic("flush error during splice")
			}
		}

		timer.Reset(time.Second)
	}
}

func (p *ReverseProxy) writeResponse(rw http.ResponseWriter, res *http.Response, hopHeaders []string) {
	for _, h := range hopHeaders {
		res.Header.Del(h)
	}

	copyHeader(rw.Header(), res.Header)

	rw.WriteHeader(res.StatusCode)
	p.copyResponse(rw, res.Body)
}

func prepRequest(req *http.Request, hopHeaders []string) *http.Request {
	outreq := new(http.Request)
	*outreq = *req // includes shallow copies of maps, but okay

	outreq.URL.Scheme = "http"
	outreq.Proto = "HTTP/1.1"
	outreq.ProtoMajor = 1
	outreq.ProtoMinor = 1
	outreq.Close = false

	// Remove hop-by-hop headers to the backend.  Especially
	// important is "Connection" because we want a persistent
	// connection, regardless of what the client sent to us.  This
	// is modifying the same underlying map from req (shallow
	// copied above) so we only copy it if necessary.
	copiedHeaders := false
	for _, h := range hopHeaders {
		if outreq.Header.Get(h) != "" {
			if !copiedHeaders {
				outreq.Header = make(http.Header)
				copyHeader(outreq.Header, req.Header)
				copiedHeaders = true
			}
			outreq.Header.Del(h)
		}
	}

	return outreq
}

func (p *ReverseProxy) copyResponse(dst io.Writer, src io.Reader) {
	if p.FlushInterval != 0 {
		if wf, ok := dst.(writeFlusher); ok {
			mlw := &maxLatencyWriter{
				dst:     wf,
				latency: p.FlushInterval,
				done:    make(chan bool),
			}
			go mlw.flushLoop()
			defer mlw.stop()
			dst = mlw
		}
	}

	io.Copy(dst, src)
}

func (p *ReverseProxy) logf(format string, args ...interface{}) {
	if p.ErrorLog != nil {
		p.ErrorLog.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

type writeFlusher interface {
	io.Writer
	http.Flusher
}

type maxLatencyWriter struct {
	dst     writeFlusher
	latency time.Duration

	lk   sync.Mutex // protects Write + Flush
	done chan bool
}

func (m *maxLatencyWriter) Write(p []byte) (int, error) {
	m.lk.Lock()
	defer m.lk.Unlock()
	return m.dst.Write(p)
}

func (m *maxLatencyWriter) flushLoop() {
	t := time.NewTicker(m.latency)
	defer t.Stop()
	for {
		select {
		case <-m.done:
			if onExitFlushLoop != nil {
				onExitFlushLoop()
			}
			return
		case <-t.C:
			m.lk.Lock()
			m.dst.Flush()
			m.lk.Unlock()
		}
	}
}

func (m *maxLatencyWriter) stop() { m.done <- true }

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP request round tripper

package proxy

import (
	"bufio"
	"net"
	"net/http"

	"golang.org/x/net/context"
)

// Transporter is an interface representing the ability to transport downstream
// connection data to an upstream host.  It can execute a single HTTP transaction,
// obtaining the Response for a given Request. It can establish WebSocket
// connections, obtaining the handshake response and the WebSocket connection.
//
// A Transporter must be safe for concurrent use by multiple goroutines.
type Transporter interface {
	// RoundTripHTTP executes a single HTTP transaction, returning
	// the Response for the request req.  RoundTrip should not
	// attempt to interpret the response.  In particular,
	// RoundTrip must return err == nil if it obtained a response,
	// regardless of the response's HTTP status code.  A non-nil
	// err should be reserved for failure to obtain a response.
	// Similarly, RoundTrip should not attempt to handle
	// higher-level protocol details such as redirects,
	// authentication, or cookies.
	//
	// RoundTripHTTP should not modify the request, except for
	// consuming and closing the Body, including on errors. The
	// request's URL and Header fields are guaranteed to be
	// initialized.
	RoundTripHTTP(context.Context, *http.Request) (*http.Response, error)

	// ConnectWebSocket executes the WebSocket protocol handshake, and readies
	// the connection for WebSocket traffic. On a successful handshake, the
	// http response from the backend is returned along with a bufio ReadWriter
	// encapsulating the connection to the backend.
	ConnectWebSocket(context.Context, *http.Request) (*http.Response, net.Conn, *bufio.ReadWriter, error)
}

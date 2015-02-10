package router

import (
	"fmt"
	"time"
)

// Route is a struct that combines the fields of HTTPRoute and TCPRoute
// for easy JSON marshaling.
type Route struct {
	// Type is the type of Route, either "http" or "tcp".
	Type string `json:"type"`
	// ID is the unique ID of this route.
	ID string `json:"id,omitempty"`
	// ParentRef is an external opaque identifier used by the route creator for
	// filtering and correlation. It typically contains the app ID.
	ParentRef string `json:"parent_ref,omitempty"`
	// Service is the ID of the service.
	Service string `json:"service"`
	// CreatedAt is the time this Route was created.
	CreatedAt time.Time `json:"created_at,omitempty"`
	// UpdatedAt is the time this Route was last updated.
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	// Domain is the domain name of this Route. It is only used for HTTP routes.
	Domain string `json:"domain,omitempty"`
	// TLSCert is the optional TLS public certificate of this Route. It is only
	// used for HTTP routes.
	TLSCert string `json:"tls_cert,omitempty"`
	// TLSCert is the optional TLS private key of this Route. It is only
	// used for HTTP routes.
	TLSKey string `json:"tls_key,omitempty"`
	// Sticky is whether or not to use sticky sessions for this route. It is only
	// used for HTTP routes.
	Sticky bool `json:"sticky,omitempty"`

	// Port is the TCP port to listen on for TCP Routes.
	Port int32 `json:"port,omitempty"`
}

func (r Route) HTTPRoute() *HTTPRoute {
	return &HTTPRoute{
		ID:        r.ID,
		ParentRef: r.ParentRef,
		Service:   r.Service,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,

		Domain:  r.Domain,
		TLSCert: r.TLSCert,
		TLSKey:  r.TLSKey,
		Sticky:  r.Sticky,
	}
}

func (r Route) TCPRoute() *TCPRoute {
	return &TCPRoute{
		ID:        r.ID,
		ParentRef: r.ParentRef,
		Service:   r.Service,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,

		Port: int(r.Port),
	}
}

func (r Route) routeForDB() interface{} {
	switch r.Type {
	case "http":
		return r.HTTPRoute()
	case "tcp":
		return r.TCPRoute()
	default:
		panic(fmt.Sprintf("unknown table name: %q", r.Type))
	}
}

// HTTPRoute is an HTTP Route.
type HTTPRoute struct {
	// TODO(bgentry): remove json tags here, or make this serialize properly with
	// a Type field.
	ID        string    `json:"id,omitempty"`
	ParentRef string    `json:"parent_ref,omitempty"`
	Service   string    `json:"service"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	Domain  string `json:"domain,omitempty"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`
	Sticky  bool   `json:"sticky,omitempty"`
}

func (r HTTPRoute) ToRoute() *Route {
	return &Route{
		// common fields
		Type:      "http",
		ID:        r.ID,
		ParentRef: r.ParentRef,
		Service:   r.Service,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,

		// http-specific fields
		Domain:  r.Domain,
		TLSCert: r.TLSCert,
		TLSKey:  r.TLSKey,
		Sticky:  r.Sticky,
	}
}

// TCPRoute is a TCP Route.
type TCPRoute struct {
	// TODO(bgentry): remove json tags here, or make this serialize properly with
	// a Type field.
	ID        string    `json:"id,omitempty"`
	ParentRef string    `json:"parent_ref,omitempty"`
	Service   string    `json:"service"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	Port int `json:"port"`
}

func (r TCPRoute) ToRoute() *Route {
	return &Route{
		Type:      "tcp",
		ID:        r.ID,
		ParentRef: r.ParentRef,
		Service:   r.Service,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,

		Port: int32(r.Port),
	}
}

type Event struct {
	Event string
	ID    string
	Error error
}

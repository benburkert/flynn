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
	ID string `json:"id,omitempty" sql:"id"`
	// ParentRef is ... TODO
	ParentRef string `json:"parent_ref,omitempty" sql:"parent_ref"`
	// Service is the ID of the service.
	Service string `json:"service" sql:"service"`
	// CreatedAt is the time this Route was created.
	CreatedAt time.Time `json:"created_at,omitempty" sql:"created_at"`
	// UpdatedAt is the time this Route was last updated.
	UpdatedAt time.Time `json:"updated_at,omitempty" sql:"updated_at"`

	// Domain is the domain name of this Route. It is only used for HTTP routes.
	Domain string `json:"domain,omitempty" sql:"domain"`
	// TLSCert is the optional TLS public certificate of this Route. It is only
	// used for HTTP routes.
	TLSCert string `json:"tls_cert,omitempty" sql:"tls_cert"`
	// TLSCert is the optional TLS private key of this Route. It is only
	// used for HTTP routes.
	TLSKey string `json:"tls_key,omitempty" sql:"tls_key"`
	// Sticky is whether or not to use sticky sessions for this route. It is only
	// used for HTTP routes.
	Sticky bool `json:"sticky,omitempty" sql:"sticky"`

	// Port is the TCP port to listen on for TCP Routes.
	Port int32 `json:"port,omitempty" sql:"port"`
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
	ID        string    `json:"id,omitempty" sql:"id"`
	ParentRef string    `json:"parent_ref,omitempty" sql:"parent_ref"`
	Service   string    `json:"service" sql:"service"`
	CreatedAt time.Time `json:"created_at,omitempty" sql:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty" sql:"updated_at"`

	Domain  string `json:"domain,omitempty" sql:"domain"`
	TLSCert string `json:"tls_cert,omitempty" sql:"tls_cert"`
	TLSKey  string `json:"tls_key,omitempty" sql:"tls_key"`
	Sticky  bool   `json:"sticky,omitempty" sql:"sticky"`
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
	ID        string    `json:"id,omitempty" sql:"id"`
	ParentRef string    `json:"parent_ref,omitempty" sql:"parent_ref"`
	Service   string    `json:"service" sql:"service"`
	CreatedAt time.Time `json:"created_at,omitempty" sql:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty" sql:"updated_at"`

	Port int `json:"port" sql:"port"`
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

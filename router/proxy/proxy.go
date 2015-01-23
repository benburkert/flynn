package proxy

import (
	"net/http"

	"golang.org/x/net/context"
)

type Proxy interface {
	ServeHTTP(context.Context, http.ResponseWriter, *http.Request)
	ServeWebSocket(context.Context, http.ResponseWriter, *http.Request)
	UpdateBackends([]string)
}

type dynamicProxy struct {
	*ReverseProxy
	*transport
}

func DynamicProxy(backends []string) Proxy {
	t := &transport{backends: backends}
	return &dynamicProxy{
		ReverseProxy: &ReverseProxy{
			Transport: t,
		},
		transport: t,
	}
}

type stickyProxy struct {
	*ReverseProxy
	*stickyTransport
}

func StickyProxy(backends []string, cookieKey [32]byte) Proxy {
	t := &stickyTransport{
		transport: &transport{backends: backends},
		cookieKey: cookieKey,
	}
	return &stickyProxy{
		ReverseProxy: &ReverseProxy{
			Transport: t,
		},
		stickyTransport: t,
	}
}

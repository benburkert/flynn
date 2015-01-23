package proxy

import (
	"net/http"
	"strings"
)

func webSocketHandshakeSuccess(res *http.Response) bool {
	return res.StatusCode == 101 &&
		strings.ToLower(res.Header.Get("Upgrade")) == "websocket" &&
		strings.ToLower(res.Header.Get("Connection")) == "upgrade"
}

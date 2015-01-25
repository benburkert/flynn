package proxy

import (
	"bufio"
	"net"
)

type streamConn struct {
	*bufio.Reader
	net.Conn
}

func (c *streamConn) Read(b []byte) (int, error) {
	return c.Reader.Read(b)
}

func (c *streamConn) CloseWrite() error {
	return c.Close()
}

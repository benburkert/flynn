package syslog

import (
	"net"

	"github.com/flynn/flynn/pkg/syslog/rfc5424"
	"github.com/flynn/flynn/pkg/syslog/rfc6587"
)

type Client struct {
	c net.Conn

	h *rfc5424.Header
	w *rfc5424.Writer
}

func Dial(address string, hdr *rfc5424.Header) (*Client, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	return &Client{
		c: conn,
		h: hdr,
		w: rfc5424.NewWriter(rfc6587.NewWriter(conn)),
	}, nil
}

func (c *Client) Write(b []byte) (int, error) {
	return c.WriteFormat(c.h, nil, b)
}

func (c *Client) WriteFormat(hdr *rfc5424.Header, sd rfc5424.StructuredData, msg []byte) (int, error) {
	return c.WriteMessage(rfc5424.Format(hdr, sd, msg))
}

func (c *Client) WriteMessage(msg *rfc5424.Message) (int, error) {
	return c.w.WriteMessage(msg)
}

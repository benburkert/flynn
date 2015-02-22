package syslog

import (
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/flynn/flynn/Godeps/_workspace/src/github.com/flynn/go-check"
	"github.com/flynn/flynn/pkg/syslog/rfc5424"
	"github.com/flynn/flynn/pkg/syslog/rfc6587"
)

// Hook gocheck up to the "go test" runner
func TestRFC5424(t *testing.T) { TestingT(t) }

type S struct {
}

var _ = Suite(&S{})

func (s *S) TestClientWrite(c *C) {
	msgc := make(chan *rfc5424.Message)

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		c.Fatal(err)
	}
	defer l.Close()

	h := testChanHandler(msgc)
	go runServer(l, h)
	addr := l.Addr().String()

	ts := time.Now().UTC()
	tss := ts.Format(time.RFC3339Nano)

	table := []struct {
		hdr  *rfc5424.Header
		msgs [][]byte

		want []string
	}{
		{
			hdr: &rfc5424.Header{Timestamp: ts},
			msgs: [][]byte{
				[]byte("Hello, world!"),
				[]byte(""),
				[]byte("☃"),
			},
			want: []string{
				fmt.Sprintf("<0>1 %s - - - - - Hello, world!", tss),
				fmt.Sprintf("<0>1 %s - - - - -", tss),
				fmt.Sprintf("<0>1 %s - - - - - ☃", tss),
			},
		},
		{
			hdr: &rfc5424.Header{
				Priority:  byte(1),
				Version:   byte(2),
				Timestamp: ts,
				Hostname:  []byte("3.4.5.6"),
				AppName:   []byte("APP-7"),
				ProcID:    []byte("PID-8"),
				MsgID:     []byte("FD9"),
			},
			msgs: [][]byte{
				[]byte("Starting process with command `bundle exec rackup config.ru -p 24405`"),
				[]byte("yay this is a message!!!\n"),
			},
			want: []string{
				fmt.Sprintf("<1>2 %s 3.4.5.6 APP-7 PID-8 FD9 - %s", tss, "Starting process with command `bundle exec rackup config.ru -p 24405`"),
				fmt.Sprintf("<1>2 %s 3.4.5.6 APP-7 PID-8 FD9 - %s", tss, "yay this is a message!!!\n"),
			},
		},
	}

	for _, test := range table {
		client, err := Dial(addr, test.hdr)
		if err != nil {
			c.Fatal(err)
		}

		for _, msg := range test.msgs {
			if _, err := client.Write(msg); err != nil {
				c.Fatal(err)
			}
		}

		for _, want := range test.want {
			got := <-msgc
			c.Assert(got.String(), Equals, want)
		}
	}
}

func testChanHandler(msgc chan *rfc5424.Message) messageHandler {
	return func(msg *rfc5424.Message) {
		msgc <- msg
	}
}

type messageHandler func(*rfc5424.Message)

func runServer(l net.Listener, h messageHandler) {
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				r := rfc5424.NewReader(rfc6587.Scanner(conn))

				for {
					msg, err := r.ReadMessage()
					if err != nil {
						conn.Close()
						return
					}

					h(msg)
				}
			}()
		}
	}()
}

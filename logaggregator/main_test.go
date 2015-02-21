package main

import (
	"net"
	"strings"
	"testing"
	"time"

	. "github.com/flynn/flynn/Godeps/_workspace/src/github.com/flynn/go-check"
)

// Hook gocheck up to the "go test" runner
func Test(t *testing.T) { TestingT(t) }

type S struct {
}

var _ = Suite(&S{})

func (s *S) SetUpSuite(c *C) {
}

func (s *S) TearDownSuite(c *C) {
}

func (s *S) TearDownTest(c *C) {
}

func newAggregator(c *C) *Aggregator {
	a := NewAggregator("127.0.0.1:0")
	if err := a.Start(); err != nil {
		c.Fatal(err)
	}
	return a
}

func (s *S) TestAggregatorListensOnAddr(c *C) {
	a := newAggregator(c)
	defer a.Shutdown()

	parts := strings.Split(a.Addr, ":")
	ip, port := parts[0], parts[1]
	c.Assert(ip, Equals, "127.0.0.1")
	c.Assert(port, Not(Equals), "0")

	conn, err := net.Dial("tcp", a.Addr)
	if err != nil {
		c.Fatal(err)
	}
	defer conn.Close()
}

const (
	sampleLogLine1 = "120 <40>1 2012-11-30T06:45:26+00:00 host app web.1 - - Starting process with command `bundle exec rackup config.ru -p 24405`"
	sampleLogLine2 = "77 <40>1 2012-11-30T06:45:26+00:00 host app web.2 - - 25 yay this is a message!!!\n"
)

func (s *S) TestAggregatorShutdown(c *C) {
	a := newAggregator(c)
	defer a.Shutdown()

	conn, err := net.Dial("tcp", a.Addr)
	if err != nil {
		c.Fatal(err)
	}
	defer conn.Close()

	conn.Write([]byte(sampleLogLine1))
	a.Shutdown()

	select {
	case <-a.logc:
	default:
		c.Errorf("logc was not closed")
	}
}

func (s *S) TestAggregatorBuffersMessages(c *C) {
	a := newAggregator(c)
	defer a.Shutdown()

	conn, err := net.Dial("tcp", a.Addr)
	if err != nil {
		c.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(sampleLogLine1)); err != nil {
		c.Fatal(err)
	}
	if _, err := conn.Write([]byte(sampleLogLine2)); err != nil {
		c.Fatal(err)
	}
	conn.Close()
	time.Sleep(10 * time.Millisecond) // time for messages to be received

	buf := a.getBuffer("app")
	msgs := buf.ReadAll()
	c.Assert(msgs, HasLen, 2)
	c.Assert(msgs[0].ProcID, Equals, "web.1")
	c.Assert(msgs[1].ProcID, Equals, "web.2")
	a.Shutdown()
}

// TODO(bgentry): tests specifically for rfc6587Split()

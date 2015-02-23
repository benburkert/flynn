package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/flynn/flynn/logaggregator/ring"
	"github.com/flynn/flynn/pkg/shutdown"
	"github.com/flynn/flynn/pkg/syslog/rfc5424"
	"github.com/flynn/flynn/pkg/syslog/rfc6587"
)

func main() {
	defer shutdown.Exit()

	listenPort := os.Getenv("PORT")
	if listenPort == "" {
		listenPort = "5000"
	}

	listenAddr := flag.String("listenaddr", ":"+listenPort, "syslog input listen address")

	a := NewAggregator(*listenAddr)
	if err := a.Start(); err != nil {
		log.Fatal(err)
	}
	defer a.Shutdown()
}

// Aggregator is a log aggregation server that collects syslog messages.
type Aggregator struct {
	// Addr is the address (host:port) to listen on for incoming syslog messages.
	Addr string

	bmu          sync.Mutex // protects buffers
	buffers      map[string]*ring.Buffer
	listener     net.Listener
	logc         chan *rfc5424.Message
	numConsumers int
	consumerwg   sync.WaitGroup
	filerwg      sync.WaitGroup
	producerwg   sync.WaitGroup

	smu      sync.Mutex // protects the following:
	shutdown chan struct{}
	closed   bool
}

// NewAggregator creates a new unstarted Aggregator that will listen on addr.
func NewAggregator(addr string) *Aggregator {
	return &Aggregator{
		Addr:         "127.0.0.1:0",
		buffers:      make(map[string]*ring.Buffer),
		logc:         make(chan *rfc5424.Message),
		numConsumers: 10,
		shutdown:     make(chan struct{}),
	}
}

// Start starts the Aggregator on Addr.
func (a *Aggregator) Start() error {
	var err error
	a.listener, err = net.Listen("tcp", a.Addr)
	if err != nil {
		return err
	}
	a.Addr = a.listener.Addr().String()

	for i := 0; i < a.numConsumers; i++ {
		a.consumerwg.Add(1)
		go func() {
			defer a.consumerwg.Done()
			a.consumeLogs()
		}()
	}

	a.producerwg.Add(1)
	go func() {
		defer a.producerwg.Done()
		a.accept()
	}()
	return nil
}

// Shutdown shuts down the Aggregator gracefully by closing its listener,
// and waiting for already-received logs to be processed.
func (a *Aggregator) Shutdown() {
	a.smu.Lock()
	defer a.smu.Unlock()
	if a.closed {
		return
	}
	close(a.shutdown)
	a.listener.Close()
	a.producerwg.Wait()
	close(a.logc)
	a.closed = true
	a.consumerwg.Wait()
}

// ReadNLogs reads up to N logs from the log buffer with id. If n is 0, or if
// there are fewer than n logs buffered, all buffered logs are returned.
func (a *Aggregator) ReadLastN(id string, n int) []*rfc5424.Message {
	if n == 0 {
		return a.getBuffer(id).ReadAll()
	}
	return a.getBuffer(id).ReadLastN(n)
}

func (a *Aggregator) accept() {
	defer a.listener.Close()

	for {
		select {
		case <-a.shutdown:
			return
		default:
		}
		conn, err := a.listener.Accept()
		if err != nil {
			continue
		}

		a.producerwg.Add(1)
		go func() {
			defer a.producerwg.Done()
			a.readLogsFromConn(conn)
		}()
	}
}

func (a *Aggregator) consumeLogs() {
	for msg := range a.logc {
		// TODO: forward message to follower aggregator
		a.getBuffer(string(msg.AppName)).Add(msg)
	}
}

func (a *Aggregator) getBuffer(id string) *ring.Buffer {
	a.bmu.Lock()
	defer a.bmu.Unlock()

	if buf, ok := a.buffers[id]; ok {
		return buf
	}
	buf := ring.NewBuffer()
	a.buffers[id] = buf
	return buf
}

func (a *Aggregator) readLogsFromConn(conn net.Conn) {
	defer conn.Close()

	connDone := make(chan struct{})
	defer close(connDone)

	go func() {
		select {
		case <-connDone:
		case <-a.shutdown:
			conn.Close()
		}
	}()

	r := rfc5424.NewReader(rfc6587.Scanner(conn))
	for {
		msg, err := r.ReadMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Println("logaggregator: conn error:", err)
			return
		}

		a.logc <- msg
	}
}

func rfc6587Split(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	i := bytes.IndexByte(data, ' ')
	switch {
	case i == 0:
		return 0, nil, errors.New("expected MSG-LEN, got space")
	case i > 5:
		return 0, nil, errors.New("MSG-LEN was longer than 5 characters")
	case i > 0:
		msgLen := data[0:i]
		length, err := strconv.Atoi(string(msgLen))
		if err != nil {
			return 0, nil, err
		}
		if length > 10000 {
			return 0, nil, fmt.Errorf("maximum MSG-LEN is 10000, got %d", length)
		}
		end := length + i + 1
		if len(data) >= end {
			// Return frame without msg length
			return end, data[i+1 : end], nil
		}
	}
	// Request more data.
	return 0, nil, nil
}

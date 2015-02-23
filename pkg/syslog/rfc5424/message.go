package rfc5424

import (
	"fmt"
	"io"
	"time"
)

var NilValue = []byte{'-'}

type Header struct {
	Priority  byte
	Version   byte
	Timestamp time.Time
	Hostname  []byte
	AppName   []byte
	ProcID    []byte
	MsgID     []byte
}

func (h Header) String() string {
	hostname := NilValue
	if len(h.Hostname) > 0 {
		hostname = h.Hostname
	}

	appname := NilValue
	if len(h.AppName) > 0 {
		appname = h.AppName
	}

	procid := NilValue
	if len(h.ProcID) > 0 {
		procid = h.ProcID
	}

	msgid := NilValue
	if len(h.MsgID) > 0 {
		msgid = h.MsgID
	}

	return fmt.Sprintf("<%d>%d %s %s %s %s %s",
		h.Priority,
		h.Version,
		h.Timestamp.Format(time.RFC3339Nano),
		hostname,
		appname,
		procid,
		msgid)
}

type StructuredData []byte // TODO: placeholder

func (sd StructuredData) String() string {
	if len(sd) > 0 {
		return string(sd)
	}
	return string(NilValue)
}

type Message struct {
	Header
	StructuredData

	Message []byte
}

func Format(hdr *Header, sd StructuredData, msg []byte) *Message {
	h := *hdr

	if h.Version == 0 {
		h.Version = byte(1)
	}
	if h.Timestamp.IsZero() {
		h.Timestamp = time.Now().UTC()
	}

	return &Message{h, sd, msg}
}

func (m Message) Msg() string {
	return string(m.Message)
}

func (m Message) WriteTo(w io.Writer) (int, error) {
	if len(m.Message) > 0 {
		return fmt.Fprintf(w, "%s %s %s", m.Header, m.StructuredData, m.Message)
	}
	return fmt.Fprintf(w, "%s %s", m.Header, m.StructuredData)
}

func (m Message) String() string {
	if len(m.Message) > 0 {
		return fmt.Sprintf("%s %s %s", m.Header, m.StructuredData, m.Message)
	}
	return fmt.Sprintf("%s %s", m.Header, m.StructuredData)
}

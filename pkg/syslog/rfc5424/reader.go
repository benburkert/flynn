package rfc5424

import (
	"bufio"
	"bytes"
	"strconv"
	"time"
)

type Reader struct {
	s scanner
}

func NewReader(s *bufio.Scanner) *Reader {
	return &Reader{scanner{s}}
}

func (r *Reader) ReadMessage() (*Message, error) {
	parts, err := r.s.next()
	if err != nil {
		return nil, err
	}

	priority, err := parseDecimal(parts[0])
	if err != nil {
		return nil, err
	}

	version, err := parseDecimal(parts[1])
	if err != nil {
		return nil, err
	}

	timestamp, err := parseTimestamp(parts[2])
	if err != nil {
		return nil, err
	}

	m := &Message{
		Header: Header{
			Priority:  priority,
			Version:   version,
			Timestamp: timestamp,
			Hostname:  parseMaybeNil(parts[3]),
			AppName:   parseMaybeNil(parts[4]),
			ProcID:    parseMaybeNil(parts[5]),
			MsgID:     parseMaybeNil(parts[6]),
		},
		StructuredData: parseMaybeNil(parts[7]),
	}

	if len(parts) > 8 {
		m.Message = parts[8]
	}

	return m, nil
}

func parseDecimal(b []byte) (byte, error) {
	v, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, err
	}
	return byte(v), nil
}

func parseTimestamp(b []byte) (time.Time, error) {
	if bytes.Equal(b, NilValue) {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339Nano, string(b))
}

func parseMaybeNil(b []byte) []byte {
	if bytes.Equal(b, NilValue) {
		return nil
	}
	return b
}

package rfc5424

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

type scanner struct {
	s *bufio.Scanner
}

func (s scanner) next() ([][]byte, error) {
	if !s.s.Scan() {
		if err := s.s.Err(); err != nil {
			return nil, s.s.Err()
		}
		return nil, io.EOF
	}

	buf := make([]byte, len(s.s.Bytes()))
	copy(buf, s.s.Bytes())

	tokens := make([][]byte, 0, 9)

	buf = buf[1:] //skip '<'
	b, buf, err := scanBytes(buf, '>')
	if err != nil {
		return nil, err
	}
	tokens = append(tokens, b)

	// {VERSION, HOSTNAME, APP-NAME, PROCID, MSGID, TIMESTAMP} + SP
	for i := 0; i < 6; i++ {
		if b, buf, err = scanBytes(buf, ' '); err != nil {
			return nil, err
		}
		tokens = append(tokens, b)
	}

	if buf[0] != '-' {
		return nil, errors.New("structured data is unsupported")
	}
	tokens = append(tokens, buf[:1])

	// SP + MSG after STRUCTURED-DATA
	if len(buf) > 1 {
		tokens = append(tokens, buf[2:])
	}

	return tokens, nil
}

func scanBytes(buf []byte, delim byte) (token, remaining []byte, err error) {
	i := bytes.IndexByte(buf, delim)
	if i == -1 {
		return nil, nil, errors.New("missing delimiter")
	}

	return buf[:i], buf[i+1:], nil
}

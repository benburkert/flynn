package rfc6587

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	MaxMsgLen   = 10000
	MaxFrameLen = MaxMsgLen + 6
)

func Scanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Split(split)

	return s
}

func split(data []byte, atEOF bool) (advance int, token []byte, err error) {
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
		if length > MaxMsgLen {
			return 0, nil, fmt.Errorf("maximum MSG-LEN is MaxMsgLen, got %d", length)
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

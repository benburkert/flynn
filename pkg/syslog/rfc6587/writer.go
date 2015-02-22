package rfc6587

import (
	"fmt"
	"io"
)

type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w}
}

func (w *Writer) Write(p []byte) (int, error) {
	return fmt.Fprintf(w.w, "%d %s", len(p), p)
}

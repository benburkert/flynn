package rfc5424

import "io"

type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w}
}

func (w *Writer) WriteMessage(msg *Message) (int, error) {
	return msg.WriteTo(w.w)
}

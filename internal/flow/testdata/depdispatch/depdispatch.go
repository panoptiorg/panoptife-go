package depdispatch

import (
	"io"
	"strings"
)

var _ strings.Builder

func sink(string) {}

// Call writes through an interface; a dispatch target with no emitted body
// stands for the writer.
func Call(w io.Writer, p []byte) {
	w.Write(p)
	sink(string(p))
}

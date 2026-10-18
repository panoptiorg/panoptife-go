package lib

import "log"

// Base declares Tag with a value receiver.
type Base struct{ name string }

// Tag logs the name: the receiver reaches a log sink.
func (b Base) Tag() string {
	log.Println(b.name)
	return b.name
}

// Wrapper gets Tag by embedding Base. It declares no Tag of its own.
type Wrapper struct {
	Base
}

// Tagger is implemented by Base and, through the embedded field, by Wrapper.
type Tagger interface {
	Tag() string
}

// NewWrapper returns the concrete *Wrapper.
func NewWrapper(name string) *Wrapper { return &Wrapper{Base{name: name}} }

// NewTagger returns a Tagger, so the concrete type is hidden from callers.
func NewTagger(name string) Tagger { return &Wrapper{Base{name: name}} }

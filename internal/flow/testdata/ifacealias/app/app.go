// Package app writes one cell three ways and reads it once. Every writer is a
// true flow into the reader: `.(string)` accepts a model.Query.
package app

import (
	"example.com/ifacealias/bus"
	"example.com/ifacealias/gen"
	"example.com/ifacealias/model"
)

// StoreAlias's parameter has the alias type, so the stored value does too.
func StoreAlias(q model.Query) { bus.Ch <- &bus.Event{Payload: q} }

// The alias spelling instantiates gen.Store first.
func GenAlias(q model.Query) { gen.Store[model.Query](q) }

func GenPlain(q string) { gen.Store[string](q) }

func sink(string) {}

// Read asserts the payload back to string.
func Read() {
	ev := <-bus.Ch
	if s, ok := ev.Payload.(string); ok {
		sink(s)
	}
}

package gen

import "example.com/ifacealias/bus"

// Store is generic: the stored value's type is T, spelled by whichever caller
// instantiated it first.
func Store[T any](v T) { bus.Ch <- &bus.Event{Payload: v} }

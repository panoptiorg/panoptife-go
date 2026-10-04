// Package app is the structural fixture for W1d closure captures: the shapes
// flow.emitClosureBindings has to get right, with no taint semantics involved
// (that is fixtures/closureflow + scripts/e2e-closure.sh).
package app

import "strings"

// WithTx mirrors the acme transaction idiom: it takes the closure as a value
// and calls it. `fn` is the call's Value, not one of its Args, which is why the
// capture cannot cross here and has to be bound at the MakeClosure site.
func WithTx(fn func(tx string) error) error { return fn("tx") }

// Captures builds a closure over the local `q`, derived from the param. Two
// real call instructions (Repeat, WithTx) plus one synthetic binding site.
func Captures(in string) error {
	q := strings.Repeat(in, 2)
	return WithTx(func(tx string) error {
		_ = tx + q
		return nil
	})
}

type T struct{ name string }

func (t *T) Method(a string) string { return t.name + a }

// BoundMethod yields a `$bound` wrapper whose single free var is the captured
// receiver — the same code path, with the receiver in the capture position.
func BoundMethod(t *T) func(string) string { return t.Method }

// NoCapture's closure captures nothing, so MakeClosure has no bindings and no
// synthetic site is emitted.
func NoCapture() func() string { return func() string { return "k" } }

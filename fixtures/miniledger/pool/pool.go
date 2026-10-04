// Package pool is the epool analogue: a generic batch pool whose flush invokes
// a caller-provided func value. NOTE: as of R3 the frontend drops generic
// instantiations from the CGF (Pkg==nil filter), so Add/flush are invisible to
// the analysis — callers see a default-leaf. The fixture keeps this package as
// a live marker of that gap (see ledger.retryEvent).
package pool

type Pool[T any] struct {
	buf []T
	max int
	fn  func([]T) error
}

func New[T any](max int, fn func([]T) error) *Pool[T] {
	return &Pool[T]{max: max, fn: fn}
}

// Add buffers v; when the batch is full it flushes the buffered values plus v
// through the handler func value.
func (p *Pool[T]) Add(v T) error {
	if len(p.buf)+1 < p.max {
		p.buf = append(p.buf, v)
		return nil
	}
	batch := append(p.buf, v)
	p.buf = nil
	return p.flush(batch)
}

func (p *Pool[T]) flush(batch []T) error { return p.fn(batch) }

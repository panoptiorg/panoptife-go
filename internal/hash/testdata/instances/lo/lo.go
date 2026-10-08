// Package lo mirrors github.com/samber/lo's shapes: a generic function whose
// instantiated signature is identical to slices.Contains's, a generic type with
// a method, and a closure inside a generic body.
package lo

func Contains[T comparable](collection []T, element T) bool {
	for _, x := range collection {
		if x == element {
			return true
		}
	}
	return false
}

func Map[T, R any](collection []T, iteratee func(item T) R) []R {
	out := make([]R, 0, len(collection))
	each := func(x T) { out = append(out, iteratee(x)) }
	for _, x := range collection {
		each(x)
	}
	return out
}

type Set[T comparable] struct{ m map[T]struct{} }

func (s *Set[T]) Add(v T) { s.m[v] = struct{}{} }

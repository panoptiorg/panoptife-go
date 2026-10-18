package lib

// Map applies f to every element of xs. It is generic, so the app calls an
// instance of it that has no package of its own.
func Map[T any](xs []T, f func(T) T) []T {
	out := make([]T, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

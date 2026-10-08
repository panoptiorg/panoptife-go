// Package pb instantiates the same identity classes with lo first and the
// plain spelling.
package pb

import (
	"slices"

	"example.com/instances/lo"
)

func B(xs []string, x string) bool { return lo.Contains(xs, x) || slices.Contains(xs, x) }

func MapB(xs []string) []int { return lo.Map(xs, func(t string) int { return len(t) }) }

func SetB(s *lo.Set[string]) { s.Add("b") }

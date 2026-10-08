// Package pa instantiates with slices first and with the alias spelling.
package pa

import (
	"slices"

	"example.com/instances/lo"
	"example.com/instances/model"
)

func A(xs []string, x string) bool { return slices.Contains(xs, x) }

func AliasA(xs []model.Str, x model.Str) bool { return lo.Contains[model.Str](xs, x) }

func MapA(xs []string) []int { return lo.Map(xs, func(s string) int { return len(s) }) }

func SetA(s *lo.Set[model.Str]) { s.Add("a") }

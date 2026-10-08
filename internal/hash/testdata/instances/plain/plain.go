// Package plain instantiates with no alias, no named function parameter in a
// type argument and no interface type argument: canonical names must equal
// ssa's own.
package plain

import "example.com/instances/lo"

type ID int

func Plain(xs []ID, x ID) bool { return lo.Contains(xs, x) }

func PlainMap(xs []ID) []string { return lo.Map(xs, func(ID) string { return "" }) }

func PlainSet(s *lo.Set[*ID], v *ID) { s.Add(v) }

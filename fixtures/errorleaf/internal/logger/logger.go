// Package logger stands in for a structured-logging package — the package path
// ends in `/logger`, which is what the catalog's log-class selector_regex
// matches. The variadic `...any` is not incidental: in real code
// the tainted operand arrives inside that pack, which is why no SINK-side type
// rule can see whether it is an error (doc 31 §4 — the pack is ONE arg port
// typed []any in the CGF).
package logger

func Errorf(format string, args ...any) {}
func Warnf(format string, args ...any)  {}

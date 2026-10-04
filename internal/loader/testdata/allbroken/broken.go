// Package allbroken fails type-check: extraction over it must be a loud
// error, never an empty CGF (doc 16 §3.7).
package allbroken

func F() int { return undefinedSymbol }

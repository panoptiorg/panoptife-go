// Package funcvars is a fixture for the package-variable call rule: a
// package-level variable bound exactly once, in its initializer, to a static
// function turns calls through it into static calls; a variable with a second
// store, or one set only from a function, stays dynamic.
package funcvars

// Clean is bound exactly once, in the package initializer, and never stored
// again: every call through it is a static call to Normalize.
var Clean = Normalize

// Rebound is bound in the initializer but stored again in Swap: dynamic.
var Rebound = Normalize

// Swap rebinds Rebound, which keeps every call through Rebound dynamic.
func Swap() {
	Rebound = Alias
}

// SetOnly is never bound in the initializer, only stored from SetOnlyFn: it
// is not provably constant, so calls through it stay dynamic.
var SetOnly func(string) string

// SetOnlyFn gives SetOnly its single store — outside any initializer.
func SetOnlyFn() {
	SetOnly = Normalize
}

func Normalize(s string) string {
	return s
}

func Alias(s string) string {
	return Normalize(s)
}

// CallClean is the function whose call site must name Normalize.
func CallClean(s string) string {
	return Clean(s)
}

// CallRebound must stay a dynamic call even though the call graph resolves
// exactly one target while Swap has not run.
func CallRebound(s string) string {
	return Rebound(s)
}

// CallSetOnly must stay dynamic: the store is not in an initializer.
func CallSetOnly(s string) string {
	if SetOnly == nil {
		SetOnlyFn()
	}
	return SetOnly(s)
}

// --- address escape ---

// Escaped is bound exactly once, but &Escaped reaches TakeAddr, which may
// store anything through it: the address escapes, so calls through Escaped
// stay dynamic.
var Escaped = Normalize

// TakeAddr may store through the pointer it is given.
func TakeAddr(p *func(string) string) {
	*p = Alias
}

// CallEscaped passes &Escaped on, then calls through Escaped.
func CallEscaped(s string) string {
	TakeAddr(&Escaped)
	return Escaped(s)
}

// ReboundViaPtr is bound in the initializer, then stored through a pointer
// alias: the alias makes it a second store to the variable itself.
var ReboundViaPtr = Normalize

// RebindViaPtr stores Alias into ReboundViaPtr through a pointer alias.
func RebindViaPtr() {
	p := &ReboundViaPtr
	*p = Alias
}

// CallReboundViaPtr must stay dynamic.
func CallReboundViaPtr(s string) string {
	return ReboundViaPtr(s)
}

// --- method expression ---

// Thing is the receiver type for the method-expression case.
type Thing struct{}

// Greet normalizes s.
func (*Thing) Greet(s string) string {
	return Normalize(s)
}

// MethodExpr is the method expression (*Thing).Greet, bound exactly once. A
// call through it is a static call to Greet with the receiver at arg 0.
var MethodExpr = (*Thing).Greet

// CallMethodExpr must name (*example.com/funcvars.Thing).Greet.
func CallMethodExpr(t *Thing, s string) string {
	return MethodExpr(t, s)
}

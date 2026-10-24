package flow

import (
	"go/token"
	"strings"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// VarInitIndex resolves package-level function variables to the one function
// their initializer binds.
//
// Generated code declares its helpers at package scope and calls them through
// the variable:
//
//	var marshalString = jsonpkg.String
//	...
//	marshalString(buf, v)
//
// SSA cannot see that the call is static: the callee operand is a load of a
// global, so the CallSite is named by the variable's type string and no
// catalog rule for the real function can select it — and every call through
// any such variable shares one callee name. The binding, though, is a
// program-level fact: the variable has exactly one store in the whole
// program, that store sits in a package initializer, and it stores a static
// *ssa.Function. Then every call through the variable IS a call to that
// function, and the CallSite is emitted as a static call to it.
//
// Exactly one store is not enough by itself: the address must never escape.
// A global has no referrers list, so BuildVarInitIndex inspects every operand
// of every instruction. The variable qualifies only when every use is either
// the destination of a store (the one initializer store) or a plain load.
// `f(&v)` hands the address to whoever might store through it; `s.f = ...`
// on a global struct writes a field; both make the variable's value
// unprovable, and the variable stays dynamic.
//
// A single call-graph target is NOT such a proof either: the resolver's
// answer is a property of the flows it happened to see, not of the program —
// a store it did not connect (in a body it never saw, or through an escaped
// address) leaves the site stamped with a wrong identity and no default-leaf
// fallback. The unique-initializer store with no escaped address is true
// regardless of analysis precision.
type VarInitIndex struct {
	// init maps a global with exactly one store, no other use, and an
	// initializer store of a *ssa.Function, to that function.
	init map[*ssa.Global]*ssa.Function
}

// BuildVarInitIndex scans every function body prog holds, one pass. It is
// built once per program and shared by every Build call. Bodies that were
// never built are invisible to it: in-scope packages are always built, and
// dependency bodies only with --dep-bodies — a store hiding in an unbuilt
// body would be missed. The initializer store is by far the commonest shape
// (generated code binds once at package scope), so the gap is theoretical.
//
// The walk can panic: ssautil.AllFunctions also visits the program's
// runtime types, and x/tools panics on some of them
// (typesinternal.ForEachElement) — the call-graph builder recovers exactly
// that panic (see buildCallGraph). Here a panic costs nothing but the fix
// itself, so it degrades to the old emission: every call through a variable
// stays dynamic.
func BuildVarInitIndex(prog *ssa.Program) (ix *VarInitIndex) {
	ix = &VarInitIndex{init: map[*ssa.Global]*ssa.Function{}}
	defer func() {
		if recover() != nil {
			ix.init = map[*ssa.Global]*ssa.Function{}
		}
	}()
	nstores := map[*ssa.Global]int{}
	inits := map[*ssa.Global]*ssa.Function{}
	escaped := map[*ssa.Global]bool{}
	for fn := range ssautil.AllFunctions(prog) {
		if fn.Blocks == nil {
			continue // never built: no stores are visible in it
		}
		inInit := packageInitializer(fn)
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				st, isStore := instr.(*ssa.Store)
				if isStore {
					if g, ok := st.Addr.(*ssa.Global); ok {
						nstores[g]++ // a non-function store disqualifies too
						if fnv, ok := st.Val.(*ssa.Function); inInit && ok {
							inits[g] = fnv
						}
					}
				}
				// Every operand use of the global must be a store to it or
				// a load of it; anything else means the address escapes.
				for _, ref := range instr.Operands(nil) {
					g, ok := (*ref).(*ssa.Global)
					if !ok {
						continue
					}
					if isStore && ref == &st.Addr {
						continue // the store's own destination
					}
					if un, ok := instr.(*ssa.UnOp); ok && un.Op == token.MUL && ref == &un.X {
						continue // a load of the variable
					}
					escaped[g] = true
				}
			}
		}
	}
	for g, fnv := range inits {
		if nstores[g] == 1 && !escaped[g] {
			ix.init[g] = methodFromThunk(fnv)
		}
	}
	return ix
}

// Init returns the function g's single initializer store binds, or nil when g
// has no store, more than one, an escaped address, or an initializer store
// that is not a static function.
func (ix *VarInitIndex) Init(g *ssa.Global) *ssa.Function {
	if ix == nil {
		return nil
	}
	return ix.init[g]
}

// packageInitializer reports whether fn is, or is nested in, a package
// initializer: the synthetic one, or a user `func init()`.
func packageInitializer(fn *ssa.Function) bool {
	for f := fn; f != nil; f = f.Parent() {
		if f.Synthetic == "package initializer" {
			return true
		}
		if f.Synthetic == "" && f.Name() == "init" && f.Pkg != nil {
			return true
		}
	}
	return false
}

// methodFromThunk resolves a method-expression thunk to the method it wraps:
// `var thunked = (*T).Method` stores the synthetic `(*T).Method$thunk`, whose
// body is one static call to the method with the thunk's first parameter as
// the receiver (plus at most the ssa:wrapnilchk builtin before it). Naming
// the method is exact, and the call site keeps the direct-call form: SSA
// passes the receiver as arg 0 of a static method call, which is what the
// thunk call does too. A thunk whose body does not have exactly one static
// callee — a promoted method reached through an embedded interface, say —
// keeps the thunk.
func methodFromThunk(fn *ssa.Function) *ssa.Function {
	if fn == nil || !strings.HasPrefix(fn.Synthetic, "thunk") {
		return fn
	}
	var method *ssa.Function
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok {
				continue
			}
			sc := call.Common().StaticCallee()
			if sc == nil {
				continue // e.g. the ssa:wrapnilchk builtin
			}
			if method != nil {
				return fn // more than one static callee: keep the thunk
			}
			method = sc
		}
	}
	if method == nil {
		return fn
	}
	return method
}

// staticFuncVar names the function a dynamic call goes to when the called
// value is a load of a package-level variable that VarInitIndex can prove
// constant. Any other operand — a local, a parameter, a MakeInterface, a
// load of a variable with other stores or an escaped address — leaves the
// call dynamic.
func (b *builder) staticFuncVar(v ssa.Value) *ssa.Function {
	load, ok := v.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return nil
	}
	g, ok := load.X.(*ssa.Global)
	if !ok {
		return nil
	}
	return b.opts.VarInits.Init(g)
}

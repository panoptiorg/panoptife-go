package hash

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// CanonicalInstances selects the order-independent names and identities for
// generic instances (pc-fe --canonical-instance-ids, default on). Set once by
// emit.Run before any function is hashed; false reproduces the previous
// emission byte for byte.
//
// Why it exists. go/ssa creates ONE instance per type-identity class of type
// arguments, for whichever caller asks first, and pc-fe builds packages in
// parallel, so "first" is a race:
//   - the instance's name is printed from the first caller's type arguments
//     (`lo.Contains[model.Str]` or `lo.Contains[string]` — an alias and its
//     target are one identity class);
//   - its signature is a canonical representative shared by EVERY identical
//     signature type, and type identity ignores parameter names:
//     `slices.Contains[[]string string]` and `lo.Contains[string]` share one,
//     printed with the names of whichever instance was created first.
//
// Neither string is a function of the instance, yet IID hashed both — the same
// commit produced different iids run to run (measured: 2 outcomes in 40 runs
// of a 24-package probe; 4 of 25 files of one internal repo). Everything below
// prints an instance from its type-identity class alone: aliases resolved at
// every depth, parameter names dropped, interfaces flattened to their method
// sets. Ordinary functions are untouched.
var CanonicalInstances = true

// IsInstanceLike: a generic instance, or a function nested in one (anonymous
// functions and range-over-func yields share their parent's type arguments).
func IsInstanceLike(fn *ssa.Function) bool {
	return len(fn.TypeArgs()) > 0
}

// canonical reports whether fn's printed forms must be canonicalized.
func canonical(fn *ssa.Function) bool {
	return CanonicalInstances && IsInstanceLike(fn)
}

// TypeString prints t from its type-identity class: identical types print
// identically, whoever spelled them first. Format follows types.TypeString
// with full package paths, which is what ssa uses in names.
func TypeString(t types.Type) string {
	return types.TypeString(canonType(t, 0), nil)
}

// TypeStringIn is the type string for a value inside fn: canonical when fn is
// instance-like, the plain t.String() otherwise (byte-identical to before).
func TypeStringIn(fn *ssa.Function, t types.Type) string {
	if fn != nil && canonical(fn) {
		return TypeString(t)
	}
	return t.String()
}

// SignatureString is fn's signature without receiver or parameter names, in
// canonical form — the identity part of an instance's signature.
func SignatureString(sig *types.Signature) string {
	return TypeString(types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic()))
}

// instanceFQN mirrors ssa's (*Function).RelString(nil) for instances, built
// from canonical pieces. For an instance with no alias, no named parameter in a
// function-typed type argument and no non-empty interface type argument, it is
// exactly fn.String() (pinned by a test).
func instanceFQN(fn *ssa.Function) string {
	if p := fn.Parent(); p != nil {
		for i, a := range p.AnonFuncs {
			if a == fn {
				return fmt.Sprintf("%s$%d", FQN(p), i+1)
			}
		}
		return fn.String() // RelString's "should never happen" branch
	}
	obj := fn.Object()
	if obj == nil {
		return fn.String()
	}
	targs := make([]string, len(fn.TypeArgs()))
	for i, t := range fn.TypeArgs() {
		targs[i] = TypeString(t)
	}
	name := obj.Name() + "[" + strings.Join(targs, " ") + "]"
	if recv := fn.Signature.Recv(); recv != nil {
		return "(" + TypeString(recv.Type()) + ")." + name
	}
	if obj.Pkg() != nil {
		return obj.Pkg().Path() + "." + name
	}
	return name
}

const maxCanonDepth = 64

// canonType rebuilds t with every alias resolved, every parameter name
// dropped, every non-empty interface flattened to its method set and the empty
// interface spelled `any`. Named types are kept (their identity is their
// declaration); only their type arguments are rebuilt. Type literals cannot be
// cyclic without a Named in between, and Named underlyings are never entered,
// so the recursion terminates; the depth cap is a backstop.
func canonType(t types.Type, depth int) types.Type {
	if t == nil || depth > maxCanonDepth {
		return t
	}
	d := depth + 1
	switch t := t.(type) {
	case *types.Alias:
		return canonType(types.Unalias(t), d)
	case *types.Basic, *types.TypeParam:
		return t
	case *types.Named:
		args := t.TypeArgs()
		if args == nil || args.Len() == 0 {
			return t
		}
		cargs := make([]types.Type, args.Len())
		for i := range cargs {
			cargs[i] = canonType(args.At(i), d)
		}
		inst, err := types.Instantiate(nil, t.Origin(), cargs, false)
		if err != nil {
			return t
		}
		return inst
	case *types.Pointer:
		return types.NewPointer(canonType(t.Elem(), d))
	case *types.Slice:
		return types.NewSlice(canonType(t.Elem(), d))
	case *types.Array:
		return types.NewArray(canonType(t.Elem(), d), t.Len())
	case *types.Map:
		return types.NewMap(canonType(t.Key(), d), canonType(t.Elem(), d))
	case *types.Chan:
		return types.NewChan(t.Dir(), canonType(t.Elem(), d))
	case *types.Signature:
		return types.NewSignatureType(nil, nil, nil,
			canonTuple(t.Params(), d), canonTuple(t.Results(), d), t.Variadic())
	case *types.Tuple:
		return canonTuple(t, d)
	case *types.Struct:
		fields := make([]*types.Var, t.NumFields())
		tags := make([]string, t.NumFields())
		for i := range fields {
			f := t.Field(i)
			// field names and tags ARE part of struct identity: keep them
			fields[i] = types.NewField(f.Pos(), f.Pkg(), f.Name(), canonType(f.Type(), d), f.Embedded())
			tags[i] = t.Tag(i)
		}
		return types.NewStruct(fields, tags)
	case *types.Interface:
		if t.NumMethods() == 0 && t.NumEmbeddeds() == 0 {
			return types.Universe.Lookup("any").Type()
		}
		methods := make([]*types.Func, t.NumMethods())
		for i := range methods {
			m := t.Method(i) // ordered by Id: deterministic
			sig, _ := canonType(m.Type(), d).(*types.Signature)
			if sig == nil {
				return t
			}
			methods[i] = types.NewFunc(m.Pos(), m.Pkg(), m.Name(), sig)
		}
		iface := types.NewInterfaceType(methods, nil)
		iface.Complete()
		return iface
	}
	return t
}

func canonTuple(t *types.Tuple, depth int) *types.Tuple {
	if t == nil {
		return nil
	}
	vars := make([]*types.Var, t.Len())
	for i := range vars {
		v := t.At(i)
		vars[i] = types.NewParam(v.Pos(), v.Pkg(), "", canonType(v.Type(), depth))
	}
	return types.NewTuple(vars...)
}

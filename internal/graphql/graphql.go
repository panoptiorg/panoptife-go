// Package graphql extracts gqlgen resolver facts: schema field -> resolver
// method bindings, the SDL-exact field name, the SDL arg names, and the
// untrusted arg param indices (doc 19, doc 36 §2). A gqlgen service is
// identified structurally by its generated ResolverRoot interface (stable
// across gqlgen v2, including the v0.17.x ResolveField codegen), so no
// gqlgen.yml parsing is needed: the resolver interfaces are generated from the
// schema and carry exactly the fc.Args surface as post-ctx/obj params.
//
// The *names* are Go-ified in those interfaces (`Login`, not `login`), which
// would leak Go into the cross-repo join key. So the exec package's generated
// dispatchers — methods `_<Type>_<field>` on *executionContext, whose body (or
// a closure inside it) calls `ec.Resolvers.<Type>().<Method>(ctx, [obj,]
// fc.Args["a"].(T), …)` — are scanned to recover the SDL field name and the
// SDL arg names. Missing dispatcher ⇒ lcfirst fallback + a `graphql-warn:`.
package graphql

import (
	"fmt"
	"go/constant"
	"go/types"
	"os"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Arg is one SDL argument of a field, bound to the resolver param that carries
// it (flow InParam numbering, ctx = 0).
type Arg struct {
	Name     string
	ParamIdx uint32
}

// Field is one schema field bound to its resolver implementation.
type Field struct {
	TypeField   string // SDL-exact: "Query.searchByToken", "AuthMutations.login"
	FieldIID    []byte // hash.ContractIID("graphql:" + TypeField) — namespaced vs gRPC
	ResolverFn  *ssa.Function
	ResolverIID []byte
	// ArgParamIdx uses flow's InParam numbering (receiver excluded, first real
	// param = 0): the fc.Args params — never ctx (0), never obj (1 on field
	// resolvers).
	ArgParamIdx []uint32
	// Args in SDL/dispatch order. Empty only when the resolver takes no args.
	Args []Arg
}

// Root resolver interfaces whose methods take no parent obj: args start right
// after ctx. Entity is the Apollo Federation entity resolver — its key args
// come from the request representation and are untrusted like fc.Args.
var rootTypes = map[string]bool{"Query": true, "Mutation": true, "Subscription": true, "Entity": true}

// dispatch is what one generated `_<Type>_<field>` dispatcher tells us about
// the resolver method it calls.
type dispatch struct {
	sdlField string
	args     []Arg
}

// Extract scans in-scope packages for gqlgen ResolverRoot interfaces and
// returns all schema-field bindings whose resolver impl is in scope.
func Extract(prog *ssa.Program, inScope map[*ssa.Package]bool, repo string) []Field {
	var out []Field
	seen := map[string]bool{}
	warned := map[string]bool{}
	for sp := range inScope {
		if sp == nil || sp.Pkg == nil {
			continue
		}
		root := resolverRoot(sp.Pkg)
		if root == nil {
			continue
		}
		// typeName -> per-type resolver interface, from ResolverRoot.
		ifaces := map[string]*types.Interface{}
		for i := 0; i < root.NumMethods(); i++ {
			m := root.Method(i)
			if iface := resolverIface(m); iface != nil {
				ifaces[m.Name()] = iface
			}
		}
		// "<Type>.<GoMethod>" -> SDL field + SDL args, from the generated
		// dispatchers in this same exec package.
		disp := scanDispatchers(prog, sp.Pkg, ifaces)
		for typeName, iface := range ifaces {
			for _, f := range bindField(prog, inScope, repo, typeName, iface, disp, warned) {
				key := f.TypeField + "|" + hash.FQN(f.ResolverFn)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, f)
			}
		}
	}
	// Deterministic emission order (inScope is a map).
	sort.Slice(out, func(i, j int) bool {
		if out[i].TypeField != out[j].TypeField {
			return out[i].TypeField < out[j].TypeField
		}
		return hash.FQN(out[i].ResolverFn) < hash.FQN(out[j].ResolverFn)
	})
	return out
}

// resolverRoot returns the gqlgen-generated ResolverRoot interface of pkg, with
// a structural sanity gate: gqlgen's exec package always also declares
// DirectiveRoot and ComplexityRoot structs (root_.generated.go), so a
// hand-written interface that merely shares the name is not misread.
func resolverRoot(pkg *types.Package) *types.Interface {
	scope := pkg.Scope()
	obj, ok := scope.Lookup("ResolverRoot").(*types.TypeName)
	if !ok {
		return nil
	}
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	for _, gate := range []string{"DirectiveRoot", "ComplexityRoot"} {
		tn, ok := scope.Lookup(gate).(*types.TypeName)
		if !ok {
			return nil
		}
		if _, ok := tn.Type().Underlying().(*types.Struct); !ok {
			return nil
		}
	}
	return iface
}

// resolverIface unwraps a ResolverRoot method "Query() QueryResolver" to the
// per-type resolver interface.
func resolverIface(m *types.Func) *types.Interface {
	sig, ok := m.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return nil
	}
	named, ok := sig.Results().At(0).Type().(*types.Named)
	if !ok || !strings.HasSuffix(named.Obj().Name(), "Resolver") {
		return nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	return iface
}

// scanDispatchers walks the exec package's `_<Type>_<field>` methods on
// *executionContext and recovers, from the `ec.Resolvers.<Type>().<Method>(…)`
// call inside (possibly nested in a closure — gqlgen v0.17.x wraps it in a
// `func(ctx) (T, error)` passed to the field middleware), the mapping
//
//	"<Type>.<Method>" -> { SDL field name, SDL arg names -> resolver param idx }
//
// The arg names come from the `fc.Args["name"]` / `args["name"]` map lookups in
// the call's argument list; param_idx is the argument's position in the call,
// which IS the resolver's InParam index (ctx is argument 0).
func scanDispatchers(prog *ssa.Program, pkg *types.Package, ifaces map[string]*types.Interface) map[string]dispatch {
	out := map[string]dispatch{}
	tn, _ := pkg.Scope().Lookup("executionContext").(*types.TypeName)
	if tn == nil {
		return out
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return out
	}
	// Longest type name first so "_Query_x" is not stolen by a hypothetical
	// shorter prefix; deterministic for equal lengths.
	typeNames := make([]string, 0, len(ifaces))
	for t := range ifaces {
		typeNames = append(typeNames, t)
	}
	sort.Slice(typeNames, func(i, j int) bool {
		if len(typeNames[i]) != len(typeNames[j]) {
			return len(typeNames[i]) > len(typeNames[j])
		}
		return typeNames[i] < typeNames[j]
	})
	for i := 0; i < named.NumMethods(); i++ {
		m := named.Method(i)
		name := m.Name()
		typeName, sdlField := "", ""
		for _, t := range typeNames {
			if strings.HasPrefix(name, "_"+t+"_") {
				typeName, sdlField = t, name[len(t)+2:]
				break
			}
		}
		if typeName == "" || sdlField == "" {
			continue
		}
		fn := prog.FuncValue(m)
		if fn == nil {
			continue
		}
		method, args, ok := resolverCall(fn, ifaces[typeName])
		if !ok {
			continue
		}
		key := typeName + "." + method
		// A dispatcher already recorded wins (first in declaration order);
		// gqlgen emits exactly one per field, and two SDL fields never share a
		// resolver method.
		if _, dup := out[key]; !dup {
			out[key] = dispatch{sdlField: sdlField, args: args}
		}
	}
	return out
}

// resolverCall finds the invoke of a method of `iface` reachable in fn or any
// of its anonymous functions, and reads the SDL arg names off its arguments.
func resolverCall(fn *ssa.Function, iface *types.Interface) (string, []Arg, bool) {
	if fn == nil || iface == nil {
		return "", nil, false
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			cc := call.Common()
			if cc.Method == nil { // not an interface invoke
				continue
			}
			recv, ok := cc.Value.Type().Underlying().(*types.Interface)
			if !ok || !types.Identical(recv, iface) {
				continue
			}
			var args []Arg
			for i, a := range cc.Args { // Invoke: receiver excluded, ctx is 0
				if n, ok := mapArgName(a); ok {
					args = append(args, Arg{Name: n, ParamIdx: uint32(i)})
				}
			}
			return cc.Method.Name(), args, true
		}
	}
	for _, af := range fn.AnonFuncs {
		if m, args, ok := resolverCall(af, iface); ok {
			return m, args, true
		}
	}
	return "", nil, false
}

// mapArgName unwraps `fc.Args["name"].(T)` / `args["name"].(T)` back to the
// constant map key. Conversions between the lookup and the call are transparent.
func mapArgName(v ssa.Value) (string, bool) {
	for i := 0; i < 8; i++ { // bounded: the shapes below never nest deeply
		switch x := v.(type) {
		case *ssa.TypeAssert:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.Extract: // typeassert with comma-ok
			v = x.Tuple
		case *ssa.Lookup:
			c, ok := x.Index.(*ssa.Const)
			if !ok || c.Value == nil || c.Value.Kind() != constant.String {
				return "", false
			}
			return constant.StringVal(c.Value), true
		default:
			return "", false
		}
	}
	return "", false
}

// bindField maps every method of one resolver interface to its in-scope
// implementations. gqlgen wires exactly one impl per interface; scanning by
// types.Implements finds it without reading the generated dispatch (extra
// impls, if any, are seeded too — over-approximation in the safe direction).
// The generated dispatch is consulted only for the SDL names.
func bindField(prog *ssa.Program, inScope map[*ssa.Package]bool, repo, typeName string,
	iface *types.Interface, disp map[string]dispatch, warned map[string]bool) []Field {
	var out []Field
	for ip := range inScope {
		if ip == nil || ip.Pkg == nil {
			continue
		}
		scope := ip.Pkg.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || obj.IsAlias() {
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				continue
			}
			if !types.Implements(types.NewPointer(named), iface) && !types.Implements(named, iface) {
				continue
			}
			mset := types.NewMethodSet(types.NewPointer(named))
			for i := 0; i < iface.NumMethods(); i++ {
				im := iface.Method(i)
				sel := mset.Lookup(im.Pkg(), im.Name())
				if sel == nil {
					continue
				}
				fn, ok := sel.Obj().(*types.Func)
				if !ok {
					continue
				}
				ssaFn := prog.FuncValue(fn)
				if ssaFn == nil || len(ssaFn.Blocks) == 0 {
					continue
				}
				argIdx := argParams(im, typeName)
				key := typeName + "." + im.Name()
				d, found := disp[key]
				field, args := d.sdlField, d.args
				if !found {
					field, args = lcfirst(im.Name()), fallbackArgs(im, argIdx)
					if !warned[key] {
						warned[key] = true
						fmt.Fprintf(os.Stderr, "graphql-warn: no generated dispatcher `_%s_<field>` "+
							"calls %s; falling back to lcfirst names %q(%s) — the cross-repo join key "+
							"and arg mapping are Go-derived, not SDL-exact\n",
							typeName, key, typeName+"."+field, argNames(args))
					}
				}
				tf := typeName + "." + field
				out = append(out, Field{
					TypeField:   tf,
					FieldIID:    hash.ContractIID("graphql:" + tf),
					ResolverFn:  ssaFn,
					ResolverIID: hash.IID(repo, ssaFn),
					ArgParamIdx: argIdx,
					Args:        args,
				})
			}
		}
	}
	return out
}

func argNames(args []Arg) string {
	n := make([]string, len(args))
	for i, a := range args {
		n[i] = a.Name
	}
	return strings.Join(n, ",")
}

// fallbackArgs names the fc.Args params after their Go param names when no
// generated dispatcher was found. Unnamed params get "" (the core then leaves
// that port positional).
func fallbackArgs(m *types.Func, idx []uint32) []Arg {
	sig, ok := m.Type().(*types.Signature)
	if !ok {
		return nil
	}
	var out []Arg
	for _, i := range idx {
		if int(i) >= sig.Params().Len() {
			continue
		}
		out = append(out, Arg{Name: lcfirst(sig.Params().At(int(i)).Name()), ParamIdx: i})
	}
	return out
}

func lcfirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	// gqlgen's Go-ification uppercases the whole leading acronym ("ID" -> id,
	// "URLField" -> urlField); mirror it so the common case round-trips.
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	if n > 1 && n < len(r) {
		n-- // last upper starts the next word
	}
	for i := 0; i < n; i++ {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// argParams returns the untrusted fc.Args param indices of a resolver-interface
// method, in flow's InParam numbering (ctx = 0). Root resolvers (Query/
// Mutation/Subscription/Entity) have (ctx, args...); field resolvers have
// (ctx, obj, args...) — obj is the parent's already-modeled output and must
// never be seeded (doc 19 §5).
func argParams(m *types.Func, typeName string) []uint32 {
	sig, ok := m.Type().(*types.Signature)
	if !ok {
		return nil
	}
	p := sig.Params()
	if p.Len() == 0 || !strings.HasSuffix(p.At(0).Type().String(), "context.Context") {
		return nil // not the gqlgen resolver shape; seed nothing
	}
	start := 1
	if !rootTypes[typeName] {
		start = 2 // skip obj
	}
	var idx []uint32
	for i := start; i < p.Len(); i++ {
		idx = append(idx, uint32(i))
	}
	return idx
}

package callgraph

import (
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/types/typeutil"

	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Externals describes the functions a call can reach in a dependency: declared
// in a package that is out of scope, that a module owns (not the standard
// library) and that is not mock output. Such a function has no emitted body in
// this extraction, but the module that declares it has its own CGF, and that
// CGF holds a summary under the iid Owner gives it. A call site that names the
// function by that iid links to the summary.
//
// A nil *Externals is valid and knows no external function.
type Externals struct {
	Owner hash.Owner
	Prog  *ssa.Program
	// Pkgs are the dependency packages (loader.Loaded.DepPackages).
	Pkgs []*ssa.Package
	// Excluded reports a function in a file that emit leaves out (a test or a
	// mockgen file), so it is never a target.
	Excluded func(*ssa.Function) bool
	// DepImpls turns on the declared-implementations rule (see Resolver).
	DepImpls bool

	pkgs map[*types.Package]bool

	byIID map[string]*ssa.Function // built on the first Lookup
	index map[string][]*types.Named
	built bool
	memo  typeutil.Map // interface type -> map[method id]*implResult
}

// implResult is the declared-implementations answer for one interface method.
type implResult struct {
	limit int
	fns   []*ssa.Function // sorted by fqn; at most limit
	over  bool            // more than limit implementations
}

// NewExternals indexes the dependency packages. It reads ssa and types data
// and creates nothing in prog.
func NewExternals(prog *ssa.Program, owner hash.Owner, pkgs []*ssa.Package, excluded func(*ssa.Function) bool, depImpls bool) *Externals {
	x := &Externals{Owner: owner, Prog: prog, Pkgs: pkgs, Excluded: excluded, DepImpls: depImpls, pkgs: map[*types.Package]bool{}}
	for _, sp := range pkgs {
		x.pkgs[sp.Pkg] = true
	}
	return x
}

// Is reports whether fn is an external function: declared by name (not a
// closure, a wrapper or a generic instance) in a dependency package, and not
// in an excluded file.
func (x *Externals) Is(fn *ssa.Function) bool {
	if x == nil || fn == nil || fn.Pkg == nil || fn.Synthetic != "" || fn.Parent() != nil || len(fn.TypeArgs()) > 0 {
		return false
	}
	return x.pkgs[fn.Pkg.Pkg] && (x.Excluded == nil || !x.Excluded(fn))
}

// Declared maps a call-graph target to the external function it stands for. A
// declared function maps to itself. A synthetic target (promotion wrapper,
// pointer-receiver wrapper or bound method) maps to the method it wraps, found
// through the program's table of declared functions. A method-expression thunk
// maps to nothing: it takes the receiver as a parameter (see sameParams). The
// lookup only reads: methods such as Program.MethodValue would create
// functions, and creation changes what the extraction emits. Anything else
// maps to nil.
func (x *Externals) Declared(fn *ssa.Function) *ssa.Function {
	if x == nil || fn == nil {
		return nil
	}
	if fn.Synthetic == "" {
		if x.Is(fn) {
			return fn
		}
		return nil
	}
	if len(fn.TypeArgs()) > 0 {
		return nil
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return nil
	}
	if d := x.Prog.FuncValue(obj); x.Is(d) && sameParams(fn.Signature, d.Signature) {
		return d
	}
	return nil
}

// sameParams reports whether two signatures take the same parameters, the
// receiver left out. A method-expression thunk (`lib.Base.Tag` as a value)
// takes the receiver as its first parameter, so a call through it passes one
// argument more than the method has. Naming the method for that call would
// shift every argument by one slot in the core, so the thunk is not mapped.
func sameParams(a, b *types.Signature) bool {
	if a.Params().Len() != b.Params().Len() || a.Variadic() != b.Variadic() {
		return false
	}
	for i := 0; i < a.Params().Len(); i++ {
		if !types.Identical(a.Params().At(i).Type(), b.Params().At(i).Type()) {
			return false
		}
	}
	return true
}

// Lookup returns the external function with the given iid, or nil. The table
// is built on the first call: the functions and methods that the dependency
// packages declare, under the iid Owner gives them. A cgstore replay needs it
// for the callee iids that no emitted function has.
func (x *Externals) Lookup(iid []byte) *ssa.Function {
	if x == nil {
		return nil
	}
	if x.byIID == nil {
		x.byIID = map[string]*ssa.Function{}
		add := func(fn *ssa.Function) {
			if x.Is(fn) {
				x.byIID[string(x.Owner.IID(fn))] = fn
			}
		}
		for _, sp := range x.Pkgs {
			for _, m := range sp.Members {
				if fn, ok := m.(*ssa.Function); ok {
					add(fn)
				}
			}
			for _, named := range declaredTypes(sp.Pkg) {
				for i := 0; i < named.NumMethods(); i++ {
					add(x.Prog.FuncValue(named.Method(i)))
				}
			}
		}
	}
	return x.byIID[string(iid)]
}

// declaredTypes lists the named types declared at package level, in name order.
func declaredTypes(pkg *types.Package) []*types.Named {
	var out []*types.Named
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		if named, ok := tn.Type().(*types.Named); ok {
			out = append(out, named)
		}
	}
	return out
}

// implementations returns the declared implementations of method m of the
// interface type iface: for every non-generic named type declared in a
// dependency package, where T or *T implements iface, the declared method
// that m selects on it. A method a type gets by embedding is the embedded
// type's own declared method. The search stops once it has more than limit
// answers; over reports that. A nil result with over false means there is no
// implementation, or that iface is not a plain method-set interface declared
// by a module (not the standard library).
func (x *Externals) implementations(iface types.Type, m *types.Func, limit int) (fns []*ssa.Function, over bool) {
	if x == nil || m == nil {
		return nil, false
	}
	t := types.Unalias(iface)
	if _, ok := t.(*types.TypeParam); ok {
		return nil, false
	}
	if n, ok := t.(*types.Named); ok && n.TypeParams().Len() > 0 {
		return nil, false // a generic interface that is not instantiated
	}
	// An interface of the standard library, or the universe, has implementations
	// everywhere: any library that happens to be loaded has some type with a
	// matching method, unrelated to the call. Only an interface that a module
	// declares says which types it is for.
	if n, ok := t.(*types.Named); ok {
		if pkg := n.Obj().Pkg(); pkg == nil || x.Owner.Modules[pkg.Path()] == "" {
			return nil, false
		}
	}
	it, ok := t.Underlying().(*types.Interface)
	if !ok || !it.IsMethodSet() || it.NumMethods() == 0 {
		return nil, false
	}
	byMethod, _ := x.memo.At(iface).(map[string]*implResult)
	if byMethod == nil {
		byMethod = map[string]*implResult{}
		x.memo.Set(iface, byMethod)
	}
	if r := byMethod[m.Id()]; r != nil && r.limit == limit {
		return r.fns, r.over
	}

	seen := map[*ssa.Function]bool{}
	for _, named := range x.candidates(m) {
		ptr := types.NewPointer(named)
		var recv types.Type
		switch {
		case types.Implements(named, it):
			recv = named
		case types.Implements(ptr, it):
			recv = ptr
		default:
			continue
		}
		sel := types.NewMethodSet(recv).Lookup(m.Pkg(), m.Name())
		if sel == nil {
			continue
		}
		obj, ok := sel.Obj().(*types.Func)
		if !ok {
			continue
		}
		fn := x.Prog.FuncValue(obj)
		if !x.Is(fn) || seen[fn] {
			continue
		}
		seen[fn] = true
		fns = append(fns, fn)
		if len(fns) > limit {
			over = true
			break
		}
	}
	if !over {
		sort.Slice(fns, func(i, j int) bool { return hash.FQN(fns[i]) < hash.FQN(fns[j]) })
	} else {
		fns = nil // never observed: the site keeps its previous result
	}
	byMethod[m.Id()] = &implResult{limit: limit, fns: fns, over: over}
	return fns, over
}

// candidates lists the named types of the dependency packages that have a
// method called like m, declared or promoted. The index is built once, on the
// first query.
func (x *Externals) candidates(m *types.Func) []*types.Named {
	if !x.built {
		x.built = true
		x.index = map[string][]*types.Named{}
		for _, sp := range x.Pkgs {
			for _, named := range declaredTypes(sp.Pkg) {
				if named.TypeParams().Len() > 0 {
					continue
				}
				if _, isIface := named.Underlying().(*types.Interface); isIface {
					continue
				}
				ms := types.NewMethodSet(types.NewPointer(named))
				for i := 0; i < ms.Len(); i++ {
					id := ms.At(i).Obj().Id()
					x.index[id] = append(x.index[id], named)
				}
			}
		}
	}
	return x.index[m.Id()]
}

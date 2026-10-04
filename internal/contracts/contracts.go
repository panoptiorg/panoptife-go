// Package contracts extracts gRPC service facts from Go: a type embedding
// Unimplemented<Svc>Server (or the Unsafe<Svc>Server escape hatch) is a gRPC
// server; its exported handler methods — unary or streaming — bind to
// GRPCMethod endpoints. The contract identity "pkg.Service/Method" is the
// cross-repo join key (matches the client side in flow.remoteContract).
package contracts

import (
	"fmt"
	"go/types"
	"os"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/hash"
)

var unimplRe = regexp.MustCompile(`^(?:Unimplemented|Unsafe)(.+)Server$`)

type GrpcMethod struct {
	FullName        string // "ledger.Ledger/GetAccount"
	ContractIID     []byte
	HandlerFn       *ssa.Function
	HandlerIID      []byte
	ClientStreaming bool // mirrors descriptor.proto; bidi = both true
	ServerStreaming bool
}

// Extract scans in-scope SSA packages for gRPC server implementations.
func Extract(prog *ssa.Program, inScope map[*ssa.Package]bool, repo string) []GrpcMethod {
	var out []GrpcMethod
	seen := map[string]bool{}
	for sp := range inScope {
		if sp == nil || sp.Pkg == nil {
			continue
		}
		scope := sp.Pkg.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			st, ok := named.Underlying().(*types.Struct)
			if !ok {
				continue
			}
			svc, pbPkg, found := serverEmbed(st)
			if !found || pbPkg == nil {
				continue
			}
			// The generated <Svc>Server interface is the authoritative RPC
			// list; without it, exported ctx-first helpers on the impl struct
			// would become phantom endpoints (doc 16 §3.5).
			rpcs, haveIface := rpcSet(svc, pbPkg)
			if !haveIface {
				fmt.Fprintf(os.Stderr, "contracts-warn: %s.%sServer interface not found; falling back to handler shape heuristic\n",
					pbPkg.Path(), svc)
			}
			// handler methods: exported, defined in this package, unary or streaming.
			mset := types.NewMethodSet(types.NewPointer(named))
			for i := 0; i < mset.Len(); i++ {
				fn := mset.At(i).Obj().(*types.Func)
				if !fn.Exported() || fn.Pkg() != sp.Pkg {
					continue
				}
				if haveIface && !rpcs[fn.Name()] {
					continue
				}
				kind := handlerKind(fn, svc, pbPkg)
				if kind == notHandler {
					if !haveIface {
						continue
					}
					// A name in the RPC set must never be dropped by
					// classification; the impl compiles against the interface,
					// so an unclassifiable shape can only be unary-like.
					kind = unary
				}
				full := pbPkg.Name() + "." + svc + "/" + fn.Name()
				if seen[full] {
					continue
				}
				seen[full] = true
				ssaFn := prog.FuncValue(fn)
				var hiid []byte
				if ssaFn != nil {
					hiid = hash.IID(repo, ssaFn)
				}
				out = append(out, GrpcMethod{
					FullName:        full,
					ContractIID:     hash.ContractIID(full),
					HandlerFn:       ssaFn,
					HandlerIID:      hiid,
					ClientStreaming: kind == clientStream || kind == bidiStream,
					ServerStreaming: kind == serverStream || kind == bidiStream,
				})
			}
		}
	}
	return out
}

// rpcSet returns the exported method names of the generated <Svc>Server
// interface in pbPkg — the authoritative RPC list (the unexported mustEmbed*
// method drops out). ok=false when the interface is missing; callers fall
// back to the shape heuristic.
func rpcSet(svc string, pbPkg *types.Package) (map[string]bool, bool) {
	tn, ok := pbPkg.Scope().Lookup(svc + "Server").(*types.TypeName)
	if !ok {
		return nil, false
	}
	iface, ok := tn.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, false
	}
	set := make(map[string]bool, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		if m := iface.Method(i); m.Exported() {
			set[m.Name()] = true
		}
	}
	return set, true
}

// serverEmbed returns (service, pb package) if st embeds Unimplemented<Svc>Server
// or Unsafe<Svc>Server (the latter is a generated interface, which still passes
// the *types.Named check below).
func serverEmbed(st *types.Struct) (string, *types.Package, bool) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Embedded() {
			continue
		}
		ft := f.Type()
		if p, ok := ft.(*types.Pointer); ok {
			ft = p.Elem()
		}
		named, ok := ft.(*types.Named)
		if !ok {
			continue
		}
		m := unimplRe.FindStringSubmatch(named.Obj().Name())
		if m == nil {
			continue
		}
		return m[1], named.Obj().Pkg(), true
	}
	return "", nil, false
}

type hkind int

const (
	notHandler hkind = iota
	unary
	serverStream
	clientStream
	bidiStream
)

// handlerKind classifies a candidate handler method:
//
//	unary:         (ctx context.Context, req *T) (*R, error)
//	serverStream:  (req *T, s pb.Svc_MServer) error
//	clientStream:  (s pb.Svc_MServer) error   — stream iface has SendAndClose
//	bidiStream:    (s pb.Svc_MServer) error   — stream iface has Send
//
// The stream param must be the generated interface named exactly
// "<Svc>_<Method>Server" in the same pb package as the embed — constructed
// equality, no name parsing.
func handlerKind(fn *types.Func, svc string, pbPkg *types.Package) hkind {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return notHandler
	}
	p := sig.Params()
	if p.Len() >= 1 && strings.HasSuffix(p.At(0).Type().String(), "context.Context") {
		return unary
	}
	if p.Len() < 1 || p.Len() > 2 {
		return notHandler
	}
	r := sig.Results()
	if r.Len() != 1 || r.At(0).Type().String() != "error" {
		return notHandler
	}
	last, ok := p.At(p.Len() - 1).Type().(*types.Named)
	if !ok {
		return notHandler
	}
	iface, ok := last.Underlying().(*types.Interface)
	if !ok {
		return notHandler
	}
	if last.Obj().Name() != svc+"_"+fn.Name()+"Server" || last.Obj().Pkg() != pbPkg {
		return notHandler
	}
	if p.Len() == 2 {
		return serverStream
	}
	// 1 param: bidi streams have Send(*Resp); client-streams have SendAndClose.
	for i := 0; i < iface.NumMethods(); i++ {
		if iface.Method(i).Name() == "Send" {
			return bidiStream
		}
	}
	return clientStream
}

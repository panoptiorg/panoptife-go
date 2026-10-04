package flow

import (
	"go/types"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
)

// RemoteClientIndex links a hand-rolled client interface to the generated
// gRPC <Svc>Client it narrows, by method-set identity rather than by name.
//
// FN-01 (measurements 2026-09-01): remoteContract recognises a remote call
// only when the receiver's type is literally named `<Svc>Client` and declared
// in a pb/api package. The standard Go mockability idiom — a local
// `Client interface { Search(ctx, *pb.SearchRequest, ...grpc.CallOption) … }`
// injected with the real pb client — fails both tests, and the boundary
// vanished silently: 5 of 80 client call sites on the broker vertical, but 3
// of 11 distinct downstream repos and 100% of one service's egress.
//
// The identity test: a local interface L is a narrowed remote client iff some
// pb `<Svc>Client` interface implements L (its method set is a superset of
// L's, signatures included). Request/response types are pb-specific, so a
// unique match is the norm; an ambiguous one (two services sharing every
// method signature L names) is refused and counted, never guessed.
type RemoteClientIndex struct {
	clients []pbClient
	// Linked counts call sites classified through this index; Unclassified
	// counts gRPC-shaped calls (variadic ...grpc.CallOption) on receivers no
	// pb client uniquely satisfies, keyed by receiver type — the loud version
	// of the old silent miss. Ambiguous counts receivers matched by >1 client.
	Linked       int
	Unclassified map[string]int
	Ambiguous    map[string]int
}

type pbClient struct {
	pkgName string
	svc     string
	named   *types.Named
}

// BuildRemoteClientIndex collects every `<Svc>Client` interface declared in a
// pb/api package anywhere in the program — dependencies included, since the
// generated package is usually OUT of the emission scope but always loaded.
func BuildRemoteClientIndex(prog *ssa.Program, pbp pkgclass.PbPaths) *RemoteClientIndex {
	idx := &RemoteClientIndex{Unclassified: map[string]int{}, Ambiguous: map[string]int{}}
	if prog == nil {
		return idx
	}
	for _, p := range prog.AllPackages() {
		if p == nil || p.Pkg == nil {
			continue
		}
		// A generated client package is recognised by its path (pb/api) OR by
		// its shape: every method of the <Svc>Client interface takes a variadic
		// ...grpc.CallOption. Some corpora keep their generated clients under
		// pkg/<svc> instead of a pb/api path, which the path rule alone
		// rejects — 100+ call sites the first remote-warn census surfaced.
		pathOK := pbp.Match(p.Pkg.Path())
		scope := p.Pkg.Scope()
		for _, name := range scope.Names() {
			if name == "Client" || !strings.HasSuffix(name, "Client") {
				continue
			}
			svc := strings.TrimSuffix(name, "Client")
			if strings.Contains(svc, "_") { // Svc_MethodClient stream object
				continue
			}
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			iface, isIface := named.Underlying().(*types.Interface)
			if !isIface || iface.NumMethods() == 0 {
				continue
			}
			if !pathOK && !allGrpcShaped(iface) {
				continue
			}
			idx.clients = append(idx.clients, pbClient{pkgName: p.Pkg.Name(), svc: svc, named: named})
		}
	}
	sort.Slice(idx.clients, func(i, j int) bool {
		return idx.clients[i].named.String() < idx.clients[j].named.String()
	})
	return idx
}

// Resolve classifies an interface call whose receiver remoteContract rejected.
// Returns the proto-style contract name when exactly one pb client satisfies
// the receiver's interface.
func (idx *RemoteClientIndex) Resolve(cc *ssa.CallCommon) (string, bool) {
	if idx == nil || cc.Method == nil {
		return "", false
	}
	t := cc.Value.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	iface, ok := t.Underlying().(*types.Interface)
	if !ok || iface.NumMethods() == 0 {
		return "", false
	}
	var found []pbClient
	for _, c := range idx.clients {
		if types.Implements(c.named, iface) {
			found = append(found, c)
		}
	}
	recv := types.TypeString(t, nil)
	switch len(found) {
	case 1:
		idx.Linked++
		return found[0].pkgName + "." + found[0].svc + "/" + cc.Method.Name(), true
	case 0:
		// grpc.ClientConnInterface.Invoke/NewStream is the generated stub's
		// own transport call — the mechanism behind every boundary, never a
		// boundary itself.
		if grpcShaped(cc.Method) && !strings.HasSuffix(recv, "google.golang.org/grpc.ClientConnInterface") {
			idx.Unclassified[recv]++
		}
		return "", false
	default:
		idx.Ambiguous[recv]++
		return "", false
	}
}

// allGrpcShaped: every method of the interface has the generated client shape.
func allGrpcShaped(iface *types.Interface) bool {
	for i := 0; i < iface.NumMethods(); i++ {
		if !grpcShaped(iface.Method(i)) {
			return false
		}
	}
	return true
}

// grpcShaped: the method's last parameter is `...grpc.CallOption` — the
// generated client signature. Such a call on a receiver we could not link is
// a boundary we are about to lose, and must be reported.
func grpcShaped(m *types.Func) bool {
	sig, ok := m.Type().(*types.Signature)
	if !ok || !sig.Variadic() || sig.Params().Len() == 0 {
		return false
	}
	last := sig.Params().At(sig.Params().Len() - 1).Type()
	sl, ok := last.(*types.Slice)
	if !ok {
		return false
	}
	named, ok := sl.Elem().(*types.Named)
	if !ok || named.Obj().Name() != "CallOption" || named.Obj().Pkg() == nil {
		return false
	}
	return strings.HasSuffix(named.Obj().Pkg().Path(), "google.golang.org/grpc")
}

// TopUnclassified renders the receivers with the most unclassified sites.
func (idx *RemoteClientIndex) TopUnclassified(n int) []string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range idx.Unclassified {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	var out []string
	for i, e := range all {
		if i >= n {
			break
		}
		out = append(out, e.k+"="+strconvItoa(e.v))
	}
	return out
}

func strconvItoa(i int) string { return strconv.Itoa(i) }

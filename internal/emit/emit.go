// Package emit orchestrates the frontend pipeline: load → SSA/VTA → LocalFlow +
// contracts → hash (iid/bid) → write CGF protobuf (one file per package).
package emit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"google.golang.org/protobuf/proto"

	"github.com/panoptiorg/panoptife-go/internal/callgraph"
	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/cgstore"
	spb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
	"github.com/panoptiorg/panoptife-go/internal/contracts"
	"github.com/panoptiorg/panoptife-go/internal/flow"
	"github.com/panoptiorg/panoptife-go/internal/graphql"
	"github.com/panoptiorg/panoptife-go/internal/hash"
	"github.com/panoptiorg/panoptife-go/internal/loader"
	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
	"github.com/panoptiorg/panoptife-go/internal/satisfaction"
)

type Options struct {
	RepoDir string
	Scope   string
	OutDir  string
	Mode    string
	Base    string
	Head    string
	Verbose bool
	// IncludeMocks keeps mockgen packages in the analysis. Default (false) drops
	// them entirely — never built, emitted, or fed to VTA (doc 21 §4.2).
	IncludeMocks bool
	// AllowMissingScope defeats the requested-vs-loaded guard (debt A3). Only
	// for a scope entry that legitimately has no Go files; anything else means
	// the corpus is partial and every number off it is a floor.
	AllowMissingScope bool
	// Dispatch selects virtual-dispatch resolution: "vta" (default), "cha", or
	// "off" (no call graph: function-value calls are opaque; interface calls
	// get no targets but are not marked opaque).
	Dispatch string
	// CgStoreDir enables the call-graph snapshot store (R4 Ph1): a re-extract
	// at an unchanged commit replays persisted dispatch instead of rebuilding
	// vta/cha. "" or "off" disables. Auto-bypassed (loud) on non-git repos and
	// dirty working trees.
	CgStoreDir string
	// NoFieldPaths disables k=2 field-path emission (doc 20 §2), restoring the
	// field-insensitive LocalFlow byte-for-byte (escape hatch; default on).
	NoFieldPaths bool
	// NoClosureFlow disables closure free-variable slots + their MakeClosure
	// binding sites (W1d), restoring the pre-W1d LocalFlow byte-for-byte. This
	// is what makes the corpus A/B honest: off must reproduce the old emission
	// exactly, so every moved chain is attributable to the capture edge.
	NoClosureFlow bool
	// NoContainerWrites disables the map-write / channel-send edges
	// (`m[k] = v`, `ch <- v` and select send cases put v into the container),
	// restoring the previous LocalFlow byte-for-byte. Default on: the edges are
	// local to one function, like field paths and closure flow, not a
	// program-wide join like heap slots.
	NoContainerWrites bool
	// NoLibraryWriteback disables the write-back edge at library call sites
	// (flow.Opts.LibraryWriteback), restoring the previous LocalFlow
	// byte-for-byte. Default on: the edge is inert unless a catalog propagator
	// fires on it.
	NoLibraryWriteback bool
	// HeapSlots enables heap-cell in/out vertices, *ssa.Send, the precise
	// *ssa.Select wiring and the `copy` builtin (W1F, doc 24 N5). Default OFF:
	// unlike field paths and closure flow this is a program-wide,
	// object-insensitive join, so it ships opt-in until doc 28 has measured it.
	HeapSlots bool
	// HeapAllFields widens heap cells from channel-typed fields to all struct
	// fields. Ignored unless HeapSlots.
	HeapAllFields bool
	// HeapIfaceNarrow tags both sides of an interface-typed cell with the
	// concrete type, so the core drops writer->reader pairings the reader's type
	// assertion cannot accept (A16, doc 30 §6.1). Ignored unless HeapSlots.
	// Default OFF — emission change, OFF reproduces the previous CGF exactly.
	HeapIfaceNarrow bool
	// HeapIfaceDrop is the blunt "never open an interface-typed cell" filter.
	// Measurement instrument only; doc 30 §7b rejected it as a shipping
	// candidate. Ignored unless HeapSlots.
	HeapIfaceDrop bool
	// ByRefOut enables by-ref param/receiver out-slots + the caller-side arg
	// back-edge (W1a, doc 29). Default OFF: it is an emission change, so OFF
	// must reproduce the previous CGF byte-for-byte or the A/B is not
	// attributable.
	ByRefOut bool
	// ErrorResults emits CallSite.error_results (B1, doc 31 §4) so the core's
	// default leaf can refuse to taint an `error`-typed result port. Default OFF:
	// an emission change, so OFF must reproduce the previous CGF byte-for-byte.
	ErrorResults bool
	// PbPaths is the generated-protobuf package heuristic (--pb-paths); the
	// zero value is the historical `pb,api`. One list gates remote-call
	// recognition, stream ports, pb-getter canonicalization AND the gRPC
	// handler request-param sourcing below, so all of them move together.
	PbPaths pkgclass.PbPaths
	// MockPaths is the mockgen directory heuristic (--mock-paths); the zero
	// value is `mock,mocks`. Only the path SEGMENTS are configurable — the
	// `mock_` package prefix and the `_mock.go` / `zzz_` file names are
	// mockgen's own output naming and stay fixed.
	MockPaths pkgclass.MockPaths
	// RequireContracts turns the "no contracts and no remote calls" warning
	// into exit code 3 (assessment §4/§9: the monoculture failure mode is a
	// successful run with an empty result).
	RequireContracts bool
	// ErrorResultsStrict also drops the result-tuple source alias for
	// multi-result calls (doc 31 §6a) — the half of B1 that reaches
	// `x, err := f()` producers, at a measured cost of 2 keys on backend-a.
	ErrorResultsStrict bool
}

// flowOpts is the single place the CLI's escape-hatch polarity (No*) is
// translated into flow.Opts' positive polarity.
func (o Options) flowOpts() flow.Opts {
	return flow.Opts{
		FieldPaths:         !o.NoFieldPaths,
		ClosureFlow:        !o.NoClosureFlow,
		ContainerWrites:    !o.NoContainerWrites,
		LibraryWriteback:   !o.NoLibraryWriteback,
		HeapSlots:          o.HeapSlots,
		HeapAllFields:      o.HeapSlots && o.HeapAllFields,
		HeapIfaceNarrow:    o.HeapSlots && o.HeapIfaceNarrow,
		HeapIfaceDrop:      o.HeapSlots && o.HeapIfaceDrop,
		ByRefOut:           o.ByRefOut,
		ErrorResults:       o.ErrorResults,
		ErrorResultsStrict: o.ErrorResults && o.ErrorResultsStrict,
		PbPaths:            o.PbPaths,
	}
}

// schemaVersion of proto/cgf.proto (additive-only, doc 03 §5).
//
//	1 -> 2 (doc 36 §1): GraphqlField.args, GraphqlArg, CallSite.arg_names;
//	       GraphqlField.type_field became SDL-exact ("AuthMutations.login"),
//	       which changes graphql contract iids.
const schemaVersion = 2

func Run(o Options) error {
	if o.Mode == "mr" && (o.Base == "" || o.Head == "") {
		return fmt.Errorf("--mode mr requires --base and --head")
	}
	switch o.Dispatch {
	case "", "off", "vta", "cha":
	default:
		return fmt.Errorf("--dispatch must be off|vta|cha, got %q", o.Dispatch)
	}
	ld, err := loader.Load(o.RepoDir, o.Scope, o.Verbose, !o.IncludeMocks, o.AllowMissingScope, o.MockPaths)
	if err != nil {
		return err
	}
	repo := ld.Module
	if repo == "" {
		repo = filepath.Base(o.RepoDir)
	}
	mode := o.Dispatch
	if mode == "" {
		mode = "vta"
	}

	inScope := inScopeSet(ld.InitPkgs)

	// declaringScopePkg locates the in-scope package owning a Pkg==nil generic
	// instantiation (or a closure nested in one) via its declaring object.
	// Plain reads only — Origin() would Build() packages mid-iteration and
	// perturb which functions have bodies (CGF-byte-visible side effect).
	declaringScopePkg := func(fn *ssa.Function) *ssa.Package {
		for f := fn; f != nil; f = f.Parent() {
			if obj := f.Object(); obj != nil && obj.Pkg() != nil {
				return ld.Prog.Package(obj.Pkg())
			}
		}
		return nil
	}
	// emittable: has a body, belongs to an in-scope package — directly, or for
	// package-less functions (Pkg==nil) via the declaring object's package —
	// and is not in an excluded file. Shared with the dispatch resolver so every
	// resolved target links to an emitted function (no dangling callee iids).
	//
	// Pkg==nil covers two kinds, both wanted (doc 24 N1):
	//   - generic instantiations (TypeArgs set), real bodies;
	//   - SYNTHETIC WRAPPERS: `$bound` method values, `$thunk` method
	//     expressions, and embedded-promotion wrappers. x/tools' createBound
	//     sets no Pkg and Synthetic != "", so these used to be rejected here and
	//     in the resolver, which made `BatchFn: i.handleEventsBatch` resolve to
	//     ZERO targets — the call site went opaque and the whole callee body was
	//     invisible from it. Method values are pervasive in acme services.
	// declaringScopePkg keeps this scope-limited: a `(*sync.Mutex).Lock$bound`
	// resolves to `sync`, which is out of scope, so it is still dropped.
	emittable := func(fn *ssa.Function) bool {
		if fn == nil || len(fn.Blocks) == 0 {
			return false
		}
		if fn.Pkg != nil {
			return inScope[fn.Pkg] && !excludedFile(fileOf(fn))
		}
		if len(fn.TypeArgs()) == 0 && fn.Synthetic == "" {
			return false // package-less, neither an instance nor a wrapper
		}
		sp := declaringScopePkg(fn)
		return sp != nil && inScope[sp] && !excludedFile(fileOf(fn))
	}
	// Collect emittable functions: user code, has body, not test/mock — now
	// including generic instantiations. droppedGeneric counts the residual gap:
	// in-scope instantiations still skipped (unbuilt body / excluded file).
	droppedGeneric := 0
	var fns []*ssa.Function
	for fn := range ssautil.AllFunctions(ld.Prog) {
		if emittable(fn) {
			fns = append(fns, fn)
			continue
		}
		if len(fn.TypeArgs()) > 0 {
			if sp := declaringScopePkg(fn); sp != nil && inScope[sp] {
				droppedGeneric++
			}
		}
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].String() < fns[j].String() })

	// Precompute iids for all in-scope functions (needed to embed callee iids).
	iidOf := map[*ssa.Function][]byte{}
	for _, fn := range fns {
		iidOf[fn] = hash.IID(repo, fn)
	}

	// Dispatch: replay a cgstore snapshot when one validates against this
	// commit + function universe; otherwise build the vta/cha graph live (and,
	// with the store enabled, persist what emit consumed after the CGF write).
	disp, resolver, store, tCG, dispatchLabel := resolveDispatch(o, ld, repo, mode, iidOf, emittable)
	fmt.Fprintf(os.Stderr, "pc-fe timing: load=%s ssa-build=%s dispatch[%s]=%s (in-scope pkgs=%d, mocks-excluded=%d, procs=%d)\n",
		ld.TLoad.Round(time.Millisecond), ld.TBuild.Round(time.Millisecond),
		dispatchLabel, tCG.Round(time.Millisecond), len(ld.InitPkgs), ld.NMocks, runtime.GOMAXPROCS(0))

	// Build Function facts, grouped by package.
	byPkg := map[string]*pb.CgfPackage{}
	getPkg := func(path string) *pb.CgfPackage {
		if p, ok := byPkg[path]; ok {
			return p
		}
		p := &pb.CgfPackage{
			Repo:          repo,
			CommitSha:     ld.CommitSHA,
			PackagePath:   path,
			SchemaVersion: schemaVersion,
			Language:      "go",
		}
		byPkg[path] = p
		return p
	}

	// capturing: live graph built AND the store wants a snapshot of what emit
	// consumed (the cached path never re-persists — bytes would be identical).
	capturing := store != nil && resolver != nil
	type callerCap struct {
		pkg string
		ce  *spb.CallerEdges
	}
	var caps []callerCap
	fnByIID := map[string]*pb.Function{}
	fo := o.flowOpts()
	fo.Emittable = emittable
	fo.RemoteClients = flow.BuildRemoteClientIndex(ld.Prog, o.PbPaths)
	nRemoteSites := 0
	var sc siteCensus
	for _, fn := range fns {
		f, ce, fnSC := buildFunction(repo, disp, fn, iidOf, capturing, fo)
		if f.Flow != nil {
			for _, cs := range f.Flow.Callsites {
				if cs.Kind == pb.CallSite_INVOKES_REMOTE {
					nRemoteSites++
				}
			}
		}
		sc.add(fnSC)
		pkgPath := hash.PackagePath(fn)
		getPkg(pkgPath).Functions = append(getPkg(pkgPath).Functions, f)
		fnByIID[string(f.Id.Iid)] = f
		if ce != nil {
			caps = append(caps, callerCap{pkgPath, ce})
		}
	}
	// FN-01: boundaries recovered through method-set identity, and the ones
	// still lost — a gRPC-shaped call the detector could not classify is a
	// silently missing boundary, so it is reported, never swallowed.
	if rc := fo.RemoteClients; rc != nil {
		if rc.Linked > 0 {
			fmt.Fprintf(os.Stderr, "remote-clients: %d call site(s) linked to a generated <Svc>Client by method-set identity\n", rc.Linked)
		}
		if n := len(rc.Unclassified); n > 0 {
			sites := 0
			for _, v := range rc.Unclassified {
				sites += v
			}
			fmt.Fprintf(os.Stderr, "remote-warn: %d gRPC-shaped call site(s) on %d receiver type(s) NOT classified as INVOKES_REMOTE (no pb <Svc>Client satisfies the interface): %s\n",
				sites, n, strings.Join(rc.TopUnclassified(5), ", "))
		}
		if n := len(rc.Ambiguous); n > 0 {
			fmt.Fprintf(os.Stderr, "remote-warn: %d receiver type(s) satisfied by MORE THAN ONE pb <Svc>Client — refused, boundary not emitted: %v\n", n, rc.Ambiguous)
		}
	}
	ge, allG, someG := 0, 0, 0
	switch d := disp.(type) {
	case *callgraph.Resolver:
		if d != nil {
			ge, allG, someG = d.GenericEdges, d.SitesAllGeneric, d.SitesSomeGeneric
		}
	case *callgraph.SnapshotResolver:
		ge, allG, someG = d.GenericEdges, d.SitesAllGeneric, d.SitesSomeGeneric
	}
	if droppedGeneric > 0 || ge > 0 {
		fmt.Fprintf(os.Stderr,
			"generics-gap: dropped_instantiations=%d (in-scope origin) dropped_edges=%d sites_all_targets_generic=%d sites_some_targets_generic=%d\n",
			droppedGeneric, ge, allG, someG)
	}
	// opaque census (no --quiet flag exists yet to gate this behind): total
	// call sites, how many are opaque to the core's default leaf, and why —
	// capped fan-out (resolver.TargetsAt's cappedOpaque) vs. genuinely
	// unresolved dynamic dispatch (zero targets, not capped) vs. a resolved
	// target that never got emitted (the "dangling callee iid" warn below).
	fmt.Fprintf(os.Stderr, "opaque: sites=%d opaque=%d capped=%d unresolved=%d dangling=%d\n",
		sc.sites, sc.opaque, sc.capped, sc.unresolved, sc.dangling)

	// gRPC contract facts: GRPCMethod endpoints + handler binds_to.
	nGrpc := 0
	for _, gm := range contracts.Extract(ld.Prog, inScope, repo) {
		if gm.HandlerFn == nil {
			continue
		}
		nGrpc++
		pkgPath := hash.PackagePath(gm.HandlerFn)
		cp := getPkg(pkgPath)
		cp.GrpcMethods = append(cp.GrpcMethods, &pb.GrpcMethod{
			Iid:             gm.ContractIID,
			FullName:        gm.FullName,
			HandlerIid:      gm.HandlerIID,
			EndpointIid:     gm.ContractIID,
			ClientStreaming: gm.ClientStreaming,
			ServerStreaming: gm.ServerStreaming,
		})
		cp.Endpoints = append(cp.Endpoints, &pb.Endpoint{
			Iid:            gm.ContractIID,
			Kind:           pb.Endpoint_GRPC,
			UntrustedInput: true,
			Name:           gm.FullName,
		})
		if f := fnByIID[string(gm.HandlerIID)]; f != nil {
			f.BindsTo = append(f.BindsTo, gm.ContractIID)
			// doc 19 follow-up: structural sourcing for gRPC handlers — the
			// request-message params are untrusted input, same as GraphQL
			// resolver args. This is what lets getter canonicalization drop
			// pb-getter callsites without losing the catalog's
			// `Request).Get*` sourcing (those callsites cease to exist).
			f.SourceParams = mergeParams(f.SourceParams, requestParamIdxs(gm.HandlerFn, o.PbPaths))
		}
	}

	// Debt A3's cheap corollary. A repo whose closure declares an
	// `Unimplemented<Svc>Server` IS a gRPC server; extracting zero handlers from
	// it means the packages that implement them are not in the analysis. Three
	// of A3's four live instances looked exactly like this and exited 0.
	// A warning, not a failure: we cannot prove the handlers are in THIS scope
	// (a client-only repo legitimately sees the server type through its deps).
	if ld.HasGRPCServer && nGrpc == 0 {
		fmt.Fprintf(os.Stderr,
			"emit-warn: this closure declares an Unimplemented<Svc>Server but 0 gRPC handlers "+
				"were extracted — chains root at endpoints, so a CGF with no entry surface finds "+
				"nothing. Check the scope covers the handler packages (debt A3)\n")
	}

	// GraphQL resolver facts: field endpoints + resolver binds_to + untrusted
	// arg seeding via source_params (doc 19).
	nGql := 0
	for _, gf := range graphql.Extract(ld.Prog, inScope, repo) {
		nGql++
		pkgPath := hash.PackagePath(gf.ResolverFn)
		cp := getPkg(pkgPath)
		args := make([]*pb.GraphqlArg, 0, len(gf.Args))
		for _, a := range gf.Args {
			args = append(args, &pb.GraphqlArg{Name: a.Name, ParamIdx: a.ParamIdx})
		}
		cp.GraphqlFields = append(cp.GraphqlFields, &pb.GraphqlField{
			Iid:         gf.FieldIID,
			TypeField:   gf.TypeField,
			ResolverIid: gf.ResolverIID,
			EndpointIid: gf.FieldIID,
			Args:        args,
		})
		cp.Endpoints = append(cp.Endpoints, &pb.Endpoint{
			Iid:            gf.FieldIID,
			Kind:           pb.Endpoint_GRAPHQL,
			UntrustedInput: true,
			Name:           gf.TypeField,
		})
		if f := fnByIID[string(gf.ResolverIID)]; f != nil {
			f.BindsTo = append(f.BindsTo, gf.FieldIID)
			f.SourceParams = mergeParams(f.SourceParams, gf.ArgParamIdx)
		}
	}

	// Monoculture guard (assessment §4/§9). Zero contracts AND zero remote
	// call sites means the frontend recognised no boundary of any kind: the
	// CGF has no entry surface and no egress, so every query over it answers
	// "nothing". That is almost never a property of the code — it is an
	// unsupported generator or a pb layout the heuristic does not match.
	//
	// noContracts is checked here (loud hint printed immediately) but the
	// --require-contracts error is returned only AFTER the CGF write loop
	// below: a CI run that hits exit 3 still gets the artifact on disk to
	// diagnose, instead of nothing at all.
	noContracts := nGrpc == 0 && nGql == 0 && nRemoteSites == 0
	if noContracts {
		fmt.Fprint(os.Stderr, noContractsHint(o))
	}

	outDir := o.OutDir
	if outDir == "" {
		outDir = filepath.Join(o.RepoDir, "..", "output", "cgf", sanitize(repo))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	nfns := 0
	paths := make([]string, 0, len(byPkg))
	for p := range byPkg {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		cp := byPkg[p]
		nfns += len(cp.Functions)
		blob, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
		if err != nil {
			return err
		}
		fpath := filepath.Join(outDir, sanitize(p)+".pb")
		if err := os.WriteFile(fpath, blob, 0o644); err != nil {
			return err
		}
		if o.Verbose {
			fmt.Fprintf(os.Stderr, "wrote %s (%d functions)\n", fpath, len(cp.Functions))
		}
	}

	// The CGF is now on disk — safe to fail the run for CI without leaving the
	// operator with nothing to diagnose.
	if noContracts && o.RequireContracts {
		return ErrNoContracts
	}

	// Persist the dispatch snapshot + satisfaction facts AFTER a successful CGF
	// write (a snapshot must never outlive a failed emit). Best-effort: a write
	// failure costs the next run a rebuild, not correctness.
	if capturing {
		tSat := time.Now()
		shards := map[string]*spb.PkgShard{}
		shard := func(pkg string) *spb.PkgShard {
			if s, ok := shards[pkg]; ok {
				return s
			}
			s := &spb.PkgShard{Pkg: pkg}
			shards[pkg] = s
			return s
		}
		for _, c := range caps {
			shard(c.pkg).Callers = append(shard(c.pkg).Callers, c.ce)
		}
		idx := satisfaction.Build(typesPkgs(ld.InitPkgs))
		for _, r := range idx.Types {
			shard(r.Pkg).Types = append(shard(r.Pkg).Types, &spb.TypeFingerprint{TypeKey: r.Key, Msfp: r.Msfp})
		}
		for _, r := range idx.Ifaces {
			shard(r.Pkg).Ifaces = append(shard(r.Pkg).Ifaces, &spb.IfaceFingerprint{IfaceKey: r.Key, Ifp: r.Ifp})
		}
		for _, p := range idx.Pairs {
			shard(p.Pkg).SatPairs = append(shard(p.Pkg).SatPairs, &spb.SatPair{TypeKey: p.TypeKey, IfaceKey: p.IfaceKey, PointerOnly: p.PointerOnly})
		}
		snap := &cgstore.Snapshot{
			Meta: &spb.Meta{
				SchemaVersion:    cgstore.SchemaVersion,
				Repo:             repo,
				CommitSha:        ld.CommitSHA,
				ParentSha:        loader.GitParent(o.RepoDir),
				Scope:            o.Scope,
				PcfeVersion:      pcfeVersionHex(),
				GoToolchain:      runtime.Version(),
				CgMode:           mode,
				CapN:             callgraph.DefaultFanoutCap,
				ExcludeMocks:     !o.IncludeMocks,
				FieldPaths:       !o.NoFieldPaths,
				ClosureFlow:      !o.NoClosureFlow,
				FnSetHash:        fnSetHash(iidOf),
				CreatedUnix:      time.Now().Unix(),
				DispatchWallMs:   tCG.Milliseconds(),
				GenericEdges:     int32(ge),
				SitesAllGeneric:  int32(allG),
				SitesSomeGeneric: int32(someG),
			},
			Shards: shards,
		}
		if err := store.Write(snap); err != nil {
			fmt.Fprintf(os.Stderr, "cgstore: write failed: %v\n", err)
		} else if o.Verbose {
			fmt.Fprintf(os.Stderr, "cgstore: wrote snapshot commit=%s (shards=%d callers=%d sat_pairs=%d, satisfaction+write=%s)\n",
				short(ld.CommitSHA), len(shards), len(caps), len(idx.Pairs), time.Since(tSat).Round(time.Millisecond))
		}
	}

	if o.Mode == "mr" {
		m, err := buildManifest(o, byPkg)
		if err != nil {
			return err
		}
		// .json, so the core's load_dir (*.pb only) never sees it
		if err := writeManifest(outDir, m); err != nil {
			return err
		}
		fmt.Printf("mr base=%s head=%s changed_files=%d changed_fns=%d\n",
			short(o.Base), short(o.Head), len(m.ChangedFiles), len(m.ChangedFns))
	}
	fmt.Printf("repo=%s commit=%s packages=%d functions=%d out=%s\n",
		repo, short(ld.CommitSHA), len(byPkg), nfns, outDir)
	return nil
}

// ErrNoContracts is returned under --require-contracts when the extraction
// recognised no endpoint and no remote call site. Callers map it to exit 3.
var ErrNoContracts = errors.New("no contracts and no remote call sites extracted")

// noContractsHint is the loud version of the monoculture failure (assessment
// §4/§9): the run "succeeds" with an empty result, and the user has no way to
// tell an unsupported generator from a repo layout the pb heuristic misses.
func noContractsHint(o Options) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("pc-fe: NO CONTRACTS AND NO REMOTE CALLS were extracted from this repo.\n")
	b.WriteString("  The CGF has no entry surface (gRPC methods, GraphQL fields) and no egress\n")
	b.WriteString("  (client call sites), so every query over it will answer \"nothing\".\n")
	b.WriteString("  Recognised generators — this frontend supports exactly these:\n")
	b.WriteString("    gRPC    : protoc-gen-go-grpc >= v1 (grpc-go server/client interfaces)\n")
	b.WriteString("    GraphQL : gqlgen v2 (codegen v0.17.x ResolverRoot)\n")
	b.WriteString("    NOT supported: gogo/protobuf service plugin, connect-go, twirp, drpc,\n")
	b.WriteString("                   plain HTTP routers (chi, gin, echo, net/http).\n")
	b.WriteString("  If your generated packages DO use protoc-gen-go-grpc, the package-layout\n")
	b.WriteString("  heuristic is the likely cause: a package counts as generated only when its\n")
	b.WriteString("  import path has one of the --pb-paths segments (currently: " + o.PbPaths.String() + ").\n")
	b.WriteString("  Try e.g. --pb-paths=" + o.PbPaths.String() + ",gen,proto,genproto\n")
	b.WriteString("  Also check --scope covers the packages that implement the handlers.\n")
	b.WriteString("  Use --require-contracts to make this an error (exit 3) in CI.\n")
	return b.String()
}

// siteCensus tallies call-site dispatch outcomes across the whole extraction
// for the "opaque:" stderr line — a census, not a CGF field: it exists so a
// monoculture-shaped precision loss (wide interfaces, dispatch=off, a scope
// gap that leaves targets unemitted) is visible on every run instead of only
// discoverable by re-deriving it from the CGF after the fact.
type siteCensus struct {
	sites      int // total CallSite entries emitted
	opaque     int // CallSite.Opaque == true
	capped     int // opaque because TargetsAt's fan-out cap tripped
	unresolved int // interface, func-value or builtin call with zero targets, uncapped; opaque unless an interface call
	dangling   int // resolved target whose iid was never emitted (scope gap)
}

func (s *siteCensus) add(o siteCensus) {
	s.sites += o.sites
	s.opaque += o.opaque
	s.capped += o.capped
	s.unresolved += o.unresolved
	s.dangling += o.dangling
}

func buildFunction(repo string, disp flow.Dispatcher, fn *ssa.Function, iidOf map[*ssa.Function][]byte, capture bool, fo flow.Opts) (*pb.Function, *spb.CallerEdges, siteCensus) {
	res := flow.Build(fn, disp, fo)
	sc := siteCensus{
		sites:      len(res.Flow.Callsites),
		capped:     res.CappedSites,
		unresolved: res.UnresolvedDynamicSites,
	}

	var ce *spb.CallerEdges
	if capture {
		// NCallInstrs, NOT len(Callsites): the snapshot ordinal key
		// re-enumerates ssa.CallInstructions, and the synthetic closure-binding
		// sites in the tail are not among them. Using the full length here makes
		// NewSnapshotResolver reject every caller that creates a closure — a
		// silent, total loss of dispatch reuse (doc 24 §5 W1d).
		ce = &spb.CallerEdges{CallerIid: iidOf[fn], NCallsites: uint32(res.NCallInstrs)}
	}

	// Resolve callee iids and stamp them on each call site.
	var calleeIIDs [][]byte
	for i, cs := range res.Flow.Callsites {
		callee := res.Callees[i]
		if cs.Opaque {
			sc.opaque++
		}
		var ids [][]byte
		switch {
		case callee.Kind == pb.CallSite_INVOKES_REMOTE && callee.RemoteFullName != "":
			// cross-repo: key by the shared contract identity.
			ids = append(ids, hash.ContractIID(callee.RemoteFullName))
		case callee.Static != nil:
			if id, ok := iidOf[callee.Static]; ok {
				ids = append(ids, id)
			} else {
				ids = append(ids, hash.IID(repo, callee.Static))
			}
		case len(callee.Targets) > 0:
			for _, t := range callee.Targets {
				if id, ok := iidOf[t]; ok {
					ids = append(ids, id)
				} else {
					// Resolver targets outside the emitted set (dep package,
					// excluded file) produce iids no summary will ever match —
					// the chain silently leafs there. Loud so scope gaps surface.
					fmt.Fprintf(os.Stderr, "emit-warn: dangling callee iid: %s -> %s (target not emitted)\n", fn.String(), t.String())
					sc.dangling++
					ids = append(ids, hash.IID(repo, t))
				}
			}
		default:
			// external / unresolved: synthesize an iid from the fqn.
			ids = append(ids, hash.IIDFromParts("", "", callee.FQN, ""))
		}
		cs.CalleeIids = ids
		calleeIIDs = append(calleeIIDs, ids...)

		// Snapshot capture: persist exactly what the resolver decided at this
		// site. Targets → the emitted iid list; capped VIRTUAL invoke → the
		// opaque bit (at invoke sites cs.Opaque ⇔ capped; a capped or starved
		// dynamic-func site replays byte-identically as absent). Static and
		// remote sites replay from SSA/type facts alone — never persisted.
		if ce != nil {
			switch {
			case len(callee.Targets) > 0:
				ce.Sites = append(ce.Sites, &spb.SiteEdge{CallsiteId: cs.Id, CalleeIids: ids, Confidence: cs.DispatchConfidence})
			case cs.Kind == pb.CallSite_VIRTUAL && cs.Opaque:
				ce.Sites = append(ce.Sites, &spb.SiteEdge{CallsiteId: cs.Id, OpaqueByCap: true, Confidence: 1.0})
			}
		}
	}
	if ce != nil && len(ce.Sites) == 0 {
		ce = nil
	}

	// iid + bid. bid = H(structural LocalFlow ⊕ sorted callee iids).
	iid := iidOf[fn]
	bid := hash.BID(canonicalFlow(res.Flow), calleeIIDs)

	file := fileOf(fn)
	return &pb.Function{
		Id:        &pb.Ident{Iid: iid, Bid: bid},
		Fqn:       fn.String(),
		Package:   hash.PackagePath(fn),
		Origin:    originOf(hash.PackagePath(fn), repo),
		Generated: generatorOf(file) != pb.Generator_GEN_NONE,
		Generator: generatorOf(file),
		HasBody:   true,
		Span:      spanOf(fn),
		Signature: signatureOf(fn),
		Flow:      res.Flow,
	}, ce, sc
}

// resolveDispatch decides between a cgstore snapshot replay and a live vta/cha
// build. Returns the Dispatcher flow consumes, the live resolver (nil on the
// cached path), the opened store (nil when disabled/bypassed/nothing to
// persist), the dispatch wall-clock, and the timing-line label.
func resolveDispatch(o Options, ld *loader.Loaded, repo, mode string, iidOf map[*ssa.Function][]byte, emittable func(*ssa.Function) bool) (flow.Dispatcher, *callgraph.Resolver, *cgstore.Store, time.Duration, string) {
	var store *cgstore.Store
	if o.CgStoreDir != "" && o.CgStoreDir != "off" && mode != "off" {
		switch {
		case ld.CommitSHA == "":
			fmt.Fprintln(os.Stderr, "cgstore: disabled (not a git repo)")
		case loader.GitDirty(o.RepoDir):
			fmt.Fprintln(os.Stderr, "cgstore: disabled (dirty working tree — the commit key would lie)")
		default:
			if cfg, err := storeConfig(o, repo, mode); err != nil {
				fmt.Fprintf(os.Stderr, "cgstore: disabled (%v)\n", err)
			} else {
				store = cgstore.Open(cfg)
			}
		}
	}
	if store != nil {
		t0 := time.Now()
		snap, reason := store.Probe(ld.CommitSHA)
		if snap != nil {
			if !bytes.Equal(snap.Meta.FnSetHash, fnSetHash(iidOf)) {
				reason = "fn-set hash drift"
			} else {
				byIID := make(map[string]*ssa.Function, len(iidOf))
				for fn, id := range iidOf {
					byIID[string(id)] = fn
				}
				// The snapshot's CallSite.Id ordinals must be re-enumerated with
				// the SAME canonicalization filter the emission uses, or every
				// id shifts. FieldPaths is read off the snapshot; HeapSlots off
				// the current run — Config.Key() covers both, so a snapshot
				// written under a different setting is in a different namespace
				// and can never be probed here.
				skip := flow.CanonicalizedCallFilter(flow.Opts{
					FieldPaths: snap.Meta.FieldPaths,
					HeapSlots:  o.HeapSlots,
					ByRefOut:   o.ByRefOut,
					PbPaths:    o.PbPaths,
				})
				sr, err := callgraph.NewSnapshotResolver(snap.Meta, snap.Shards, byIID, skip)
				if err != nil {
					reason = err.Error()
				} else {
					return sr, nil, store, time.Since(t0), mode + "-cached"
				}
			}
		}
		if reason != "" && reason != "no snapshot" {
			fmt.Fprintf(os.Stderr, "cgstore: %s — rebuilding\n", reason)
		}
	}
	cg, tCG := loader.BuildCallGraph(ld.Prog, mode)
	resolver := callgraph.NewResolver(cg, callgraph.DefaultFanoutCap, emittable)
	if cg == nil {
		store = nil // off mode or a recovered graph-build panic: nothing trustworthy to persist
	}
	return resolver, resolver, store, tCG, mode
}

func storeConfig(o Options, repo, mode string) (cgstore.Config, error) {
	pv := pcfeVersionHex()
	if pv == "" {
		return cgstore.Config{}, fmt.Errorf("cannot hash own binary")
	}
	goSum := ""
	if b, err := os.ReadFile(filepath.Join(o.RepoDir, "go.sum")); err == nil {
		s := sha256.Sum256(b)
		goSum = fmt.Sprintf("%x", s[:])
	}
	return cgstore.Config{
		Root:         o.CgStoreDir,
		Repo:         repo,
		Scope:        o.Scope,
		PcfeVersion:  pv,
		GoToolchain:  runtime.Version(),
		CgMode:       mode,
		CapN:         callgraph.DefaultFanoutCap,
		ExcludeMocks: !o.IncludeMocks,
		FieldPaths:   !o.NoFieldPaths,
		ClosureFlow:  !o.NoClosureFlow,

		ContainerWrites:    !o.NoContainerWrites,
		LibraryWriteback:   !o.NoLibraryWriteback,
		HeapSlots:          o.HeapSlots,
		HeapAllFields:      o.HeapSlots && o.HeapAllFields,
		HeapIfaceNarrow:    o.HeapSlots && o.HeapIfaceNarrow,
		HeapIfaceDrop:      o.HeapSlots && o.HeapIfaceDrop,
		ByRefOut:           o.ByRefOut,
		ErrorResults:       o.ErrorResults,
		ErrorResultsStrict: o.ErrorResults && o.ErrorResultsStrict,
		PbPaths:            o.PbPaths.String(),
		MockPaths:          o.MockPaths.String(),
		GoSumHash:          goSum,
	}, nil
}

var pcfeVersionMemo *string

// pcfeVersionHex content-hashes the running pc-fe binary (the same proxy for
// "extraction logic version" run.sh's extract-key uses); "" when unhashable
// (exec wrappers, deleted binary) — the store is then disabled, never guessed.
func pcfeVersionHex() string {
	if pcfeVersionMemo != nil {
		return *pcfeVersionMemo
	}
	v := ""
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			s := sha256.Sum256(b)
			v = fmt.Sprintf("%x", s[:])
		}
	}
	pcfeVersionMemo = &v
	return v
}

// fnSetHash digests the sorted iid set of every emitted function — the
// structural guard that a snapshot's function universe matches the freshly
// rebuilt SSA even when every store-key input matched.
func fnSetHash(iidOf map[*ssa.Function][]byte) []byte {
	ids := make([]string, 0, len(iidOf))
	for _, id := range iidOf {
		ids = append(ids, string(id)) // fixed-width 32-byte iids: no framing ambiguity
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
	}
	return h.Sum(nil)
}

func typesPkgs(pkgs []*ssa.Package) []*types.Package {
	out := make([]*types.Package, 0, len(pkgs))
	for _, sp := range pkgs {
		if sp != nil && sp.Pkg != nil {
			out = append(out, sp.Pkg)
		}
	}
	return out
}

// canonicalFlow serializes the LocalFlow WITHOUT callee_iids (those are embedded
// separately into the bid), so structural body identity is independent of how
// callees are keyed.
func canonicalFlow(fl *pb.LocalFlow) []byte {
	clone := proto.Clone(fl).(*pb.LocalFlow)
	for _, cs := range clone.Callsites {
		cs.CalleeIids = nil
		cs.Span = nil
	}
	for _, v := range clone.Vertices {
		v.Span = nil
		// cosmetic: identity is field_path — a field RENAME must not cold-start
		v.FieldNames = nil
	}
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	return b
}

func signatureOf(fn *ssa.Function) *pb.Signature {
	sig := fn.Signature
	s := &pb.Signature{HasReceiver: sig.Recv() != nil, Variadic: sig.Variadic()}
	if sig.Recv() != nil {
		s.Params = append(s.Params, &pb.Param{Name: "recv", Type: sig.Recv().Type().String()})
	}
	if params := sig.Params(); params != nil {
		for i := 0; i < params.Len(); i++ {
			p := params.At(i)
			s.Params = append(s.Params, &pb.Param{Name: p.Name(), Type: p.Type().String(), ByRef: isByRef(p.Type().String())})
		}
	}
	if rs := sig.Results(); rs != nil {
		for i := 0; i < rs.Len(); i++ {
			s.Returns = append(s.Returns, &pb.TypeRef{Type: rs.At(i).Type().String()})
		}
	}
	return s
}

func isByRef(typ string) bool {
	return strings.HasPrefix(typ, "*") || strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map[")
}

func spanOf(fn *ssa.Function) *pb.Span {
	if fn.Prog == nil || !fn.Pos().IsValid() {
		return nil
	}
	p := fn.Prog.Fset.Position(fn.Pos())
	return &pb.Span{File: p.Filename, Line: int32(p.Line), Col: int32(p.Column)}
}

func inScopeSet(pkgs []*ssa.Package) map[*ssa.Package]bool {
	m := make(map[*ssa.Package]bool, len(pkgs))
	for _, p := range pkgs {
		m[p] = true
	}
	return m
}

// requestParamIdxs: InParam indices (receiver excluded — same numbering the
// LocalFlow vertices use) of a gRPC handler's request-message params: pointer
// to a named struct living in a pb/api package. ctx and stream objects don't
// match; unary (ctx, req) yields [1], server-stream (req, stream) yields [0].
func requestParamIdxs(fn *ssa.Function, pbp pkgclass.PbPaths) []uint32 {
	var out []uint32
	hasRecv := fn.Signature.Recv() != nil
	idx := uint32(0)
	for i, p := range fn.Params {
		if i == 0 && hasRecv {
			continue
		}
		t := p.Type()
		if ptr, ok := t.Underlying().(*types.Pointer); ok {
			if named, ok := ptr.Elem().(*types.Named); ok {
				if _, isStruct := named.Underlying().(*types.Struct); isStruct {
					if pkg := named.Obj().Pkg(); pkg != nil && pbp.Match(pkg.Path()) {
						out = append(out, idx)
					}
				}
			}
		}
		idx++
	}
	return out
}

// mergeParams unions source_params index sets, sorted (deterministic CGF bytes
// and stable summary_key input).
func mergeParams(a, b []uint32) []uint32 {
	set := map[uint32]bool{}
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		set[v] = true
	}
	out := make([]uint32, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(s)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

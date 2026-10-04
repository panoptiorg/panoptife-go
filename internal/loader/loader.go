// Package loader loads Go packages with full type info and builds SSA + a VTA
// call graph. Error-tolerant: packages that fail to type-check are skipped, not
// fatal — needed to load real, partially-resolvable monorepo services (doc 02).
package loader

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
)

type Loaded struct {
	Prog      *ssa.Program
	InitPkgs  []*ssa.Package // in-scope packages (the ones we emit)
	Module    string         // module path from go.mod, e.g. gitlab.example.com/acme/ledger-svc
	CommitSHA string
	// Phase wall-clocks + counters for the `pc-fe timing:` line, which emit
	// prints once the dispatch decision (build vs cgstore reload) is known.
	TLoad  time.Duration
	TBuild time.Duration
	NMocks int
	// HasGRPCServer: the loaded closure declares at least one
	// `Unimplemented<Svc>Server` type, i.e. this repo is a gRPC server even if
	// the generated package is out of scope. Emit warns when this is true and it
	// extracted zero gRPC methods — the shape debt A3 produced three times.
	HasGRPCServer bool
}

// Load loads `scope` (comma-separated package patterns) rooted at repoDir.
// When excludeMocks is set, packages classified as mock (mockgen output —
// `/mocks/` path segment or a package whose only non-test files are `_mock.go`
// / `zzz_` generated) are dropped from the SSA-build set: they are never built,
// emitted, or fed to VTA. Mocks are test doubles — analyzing them is pure cost
// and pure noise (they balloon SSA/VTA; doc 21 §4.2).
// The dispatch call graph is NOT built here — emit decides between
// BuildCallGraph and a cgstore snapshot reload (R4 Ph1) after loading.
// allowMissingScope defeats the requested-vs-loaded guard below. It exists
// because a scope entry with no Go files must not become an unfixable stop, and
// for nothing else — a run that needs it has an incomplete corpus and every
// number taken from it is a floor, not a measurement.
// mockPaths configures the mock DIRECTORY segments only (zero value =
// "mock,mocks"); mockgen's own output naming (`mock_` prefix, `_mock.go` /
// `zzz_` files) is not configurable.
func Load(repoDir, scope string, verbose, excludeMocks, allowMissingScope bool, mockPaths pkgclass.MockPaths) (*Loaded, error) {
	patterns := splitScope(scope)
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir:   repoDir,
		Tests: false,
	}
	tLoadStart := time.Now()
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	tLoad := time.Since(tLoadStart)
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages matched scope %q in %s", scope, repoDir)
	}

	// Report (but tolerate) type errors — except the patterns that mean the
	// whole extraction is silently empty (doc 16 §3.7). A pc-fe binary built
	// with an older toolchain than the repo's go directive fails every package
	// with "requires newer Go version"; being error-tolerant here would emit a
	// near-empty CGF with exit 0 and the repo would quietly drop out of the
	// fleet. Same for a scope where most matched packages fail to type-check.
	nErr := 0
	skewMsg := ""
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			nErr++
			if skewMsg == "" && strings.Contains(e.Msg, "requires newer Go version") {
				skewMsg = p.PkgPath + ": " + e.Msg
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "load-warn %s: %v\n", p.PkgPath, e)
			}
		}
	})
	if skewMsg != "" {
		return nil, fmt.Errorf("toolchain skew: %s — rebuild pc-fe with a toolchain >= the repo's go directive (e.g. GOTOOLCHAIN=goX.Y.Z go build); refusing to emit a near-empty CGF", skewMsg)
	}
	nBadRoots := 0
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			nBadRoots++
		}
	}
	if verbose && nErr > 0 {
		fmt.Fprintf(os.Stderr, "loaded %d in-scope packages (%d type errors tolerated)\n", len(pkgs), nErr)
	}

	// Create SSA for the whole closure (for types) but BUILD only the in-scope
	// packages' function bodies. Dependency bodies are never emitted — the core
	// treats out-of-scope callees as opaque pass-through — so building the full
	// closure is wasted work and, on large services, prohibitively expensive.
	//
	// ssa.Package.Build is concurrency-safe on distinct packages (idempotent,
	// self-synchronized), so build the in-scope set in parallel — a
	// precision-free speedup of the SSA-build phase.
	tBuildStart := time.Now()
	// ssaPkgs is parallel to pkgs (same order, nil where SSA creation failed).
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	init := make([]*ssa.Package, 0, len(ssaPkgs))
	nMocks := 0
	dropped := map[string]bool{} // deliberately excluded ⇒ not "missing"
	for i, sp := range ssaPkgs {
		if sp == nil {
			continue
		}
		if excludeMocks && i < len(pkgs) && isMockPackage(pkgs[i], mockPaths) {
			nMocks++
			dropped[pkgs[i].PkgPath] = true
			continue // never build/emit/VTA a mock package
		}
		init = append(init, sp)
	}
	// Loud-fail guards (doc 16 §3.7): extraction that would be near-empty is a
	// correctness failure, not a tolerable degradation.
	if len(init) == 0 {
		return nil, fmt.Errorf("scope %q matched %d packages but none type-checked/built (%d type errors); refusing to emit an empty CGF — run with --verbose for the errors", scope, len(pkgs), nErr)
	}
	if nBadRoots*2 > len(pkgs) {
		return nil, fmt.Errorf("%d of %d matched packages have type errors (%d total); refusing to emit a near-empty CGF — run with --verbose for the errors", nBadRoots, len(pkgs), nErr)
	}
	// Debt A3, the guard the three above cannot be: every one of them counts over
	// the packages `packages.Load` RETURNED. A package that fails before it comes
	// back — an unresolvable dep, an off-VPN module cache — is simply absent, so
	// len(pkgs) is already the reduced number and every ratio looks healthy.
	// One service emitted 158 of 485 requested packages with grpc_methods=0 and
	// exit 0 that way; three siblings did 61/301, 53/138 and 38/80.
	// The only thing that catches it is comparing against what was REQUESTED.
	if missing := missingScope(patterns, init, dropped); len(missing) > 0 && !allowMissingScope {
		return nil, fmt.Errorf(
			"%d of %d requested packages never loaded (e.g. %s); refusing to emit a partial CGF — "+
				"usually an unresolvable dependency or an incomplete module cache (try --verbose, "+
				"or --allow-missing-scope to extract anyway, knowing every number is then a floor)",
			len(missing), len(patterns), strings.Join(firstN(missing, 5), ", "))
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, sp := range init {
		wg.Add(1)
		sem <- struct{}{}
		go func(p *ssa.Package) {
			defer wg.Done()
			defer func() { <-sem }()
			p.Build()
		}(sp)
	}
	wg.Wait()
	tBuild := time.Since(tBuildStart)

	mod := moduleOf(pkgs)
	return &Loaded{
		HasGRPCServer: hasGRPCServer(pkgs),
		Prog:          prog,
		InitPkgs:      init,
		Module:        mod,
		CommitSHA:     gitHead(repoDir),
		TLoad:         tLoad,
		TBuild:        tBuild,
		NMocks:        nMocks,
	}, nil
}

// BuildCallGraph resolves interface/dynamic dispatch over the built SSA. VTA
// (default) is precise but whole-program and superlinear — the dominant
// extraction cost on large repos; CHA is near-linear but over-approximates
// (doc 21 §4.3b); "off" skips the build entirely — every dynamic site stays
// opaque, the pre-dispatch-wiring behavior.
func BuildCallGraph(prog *ssa.Program, dispatch string) (*callgraph.Graph, time.Duration) {
	t0 := time.Now()
	var cg *callgraph.Graph
	func() {
		defer func() { _ = recover() }() // can panic on incomplete SSA; degrade gracefully
		switch dispatch {
		case "off":
		case "cha":
			cg = cha.CallGraph(prog)
		default:
			cg = vta.CallGraph(ssautil.AllFunctions(prog), nil)
		}
	}()
	return cg, time.Since(t0)
}

// isMockPackage reports whether p is mockgen output: a `/mocks/` (or `/mock/`)
// path segment, a `mock_*` path segment or package name (mockgen's
// one-package-per-directory layout, e.g. internal/test/mock_events), or a
// package whose every non-test source file is a mock-generated file
// (`_mock.go` / `zzz_*`). Test doubles only — excluded from analysis entirely
// when excludeMocks is set.
func isMockPackage(p *packages.Package, mockPaths pkgclass.MockPaths) bool {
	if p == nil {
		return false
	}
	if mockPaths.Match(p.PkgPath) ||
		hasPathSegmentPrefix(p.PkgPath, "mock_") || strings.HasPrefix(p.Name, "mock_") {
		return true
	}
	files := p.GoFiles
	if len(files) == 0 {
		files = p.CompiledGoFiles
	}
	sawSource := false
	for _, f := range files {
		base := f[strings.LastIndex(f, "/")+1:]
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		sawSource = true
		if !strings.HasSuffix(base, "_mock.go") && !strings.HasPrefix(base, "zzz_") {
			return false // a real source file ⇒ not a pure mock package
		}
	}
	return sawSource
}

func hasPathSegmentPrefix(path, prefix string) bool {
	for _, s := range strings.Split(path, "/") {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// missingScope reports the requested patterns that name an exact package and
// yet produced no built SSA package. Wildcards (`...`) and relative patterns
// (`./x`) are skipped: they legitimately match any number of packages, so there
// is nothing to compare 1:1. Deliberate exclusions (mocks) are not missing.
//
// This is deliberately checked against `init` — the BUILT set — not against
// what `packages.Load` returned. A package that came back with type errors and
// no SSA is exactly as absent from the analysis as one that never came back.
func missingScope(patterns []string, init []*ssa.Package, dropped map[string]bool) []string {
	built := make(map[string]bool, len(init))
	for _, sp := range init {
		if sp != nil && sp.Pkg != nil {
			built[sp.Pkg.Path()] = true
		}
	}
	var missing []string
	for _, p := range patterns {
		if strings.Contains(p, "...") || strings.HasPrefix(p, "./") ||
			strings.HasPrefix(p, "../") || p == "." {
			continue
		}
		if !built[p] && !dropped[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	return missing
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// hasGRPCServer reports whether the loaded closure declares any
// `Unimplemented<Svc>Server` type — the generated embed every real handler
// carries. Checked over the whole closure, not just the roots, because the
// generated package is normally OUT of scope (run.sh's expansion drops `/pb`)
// while the service it serves is in it. Names only: no type resolution.
func hasGRPCServer(pkgs []*packages.Package) bool {
	found := false
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if found || p.Types == nil || p.Types.Scope() == nil {
			return
		}
		for _, n := range p.Types.Scope().Names() {
			if strings.HasPrefix(n, "Unimplemented") && strings.HasSuffix(n, "Server") {
				found = true
				return
			}
		}
	})
	return found
}

func splitScope(scope string) []string {
	var out []string
	for _, s := range strings.Split(scope, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		out = []string{"./..."}
	}
	return out
}

func moduleOf(pkgs []*packages.Package) string {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path != "" {
			return p.Module.Path
		}
	}
	return ""
}

func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitParent returns HEAD's first parent ("" if none/non-git) — stamped into
// cgstore meta so Ph2 can locate the base snapshot.
func GitParent(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD^").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitDirty reports uncommitted changes. A dirty tree makes the commit SHA a
// lie as a content key, so cgstore must bypass both read and write on it.
func GitDirty(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return true // unknown state: treat as dirty, never serve a stale graph
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// Package loader loads Go packages with full type info and builds SSA;
// BuildCallGraph builds the call graph separately (VTA, CHA or none).
// Error-tolerant: packages that fail to type-check are skipped, not fatal —
// needed to load real, partially-resolvable monorepo services.
package loader

import (
	"errors"
	"fmt"
	"go/types"
	"io/fs"
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
	Prog     *ssa.Program
	InitPkgs []*ssa.Package // in-scope packages (the ones we emit)
	Module   string         // module path from go.mod, e.g. gitlab.example.com/acme/ledger-svc
	// Modules maps the import path of every loaded package, in-scope or not, to
	// the module path that declares it. The standard library has no entry.
	Modules   map[string]string
	CommitSHA string
	// deps holds the dependency packages: see DepPackages.
	deps []*ssa.Package
	// vtaFuncs is the function set VTA runs over once BuildDeps has run; nil
	// means every function ssautil.AllFunctions finds.
	vtaFuncs map[*ssa.Function]bool
	// Phase wall-clocks + counters for the `pc-fe timing:` line, which emit
	// prints once the dispatch decision (build vs cgstore reload) is known.
	TLoad  time.Duration
	TBuild time.Duration
	// TDeps is the time BuildDeps took; zero when it did not run.
	TDeps  time.Duration
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
	if err := checkModule(repoDir); err != nil {
		return nil, err
	}
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
		if pat := unmatchedPattern(pkgs); pat != "" {
			return nil, fmt.Errorf("scope %q matched no packages in %s", pat, repoDir)
		}
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
	// A package with type errors gets no SSA package, so it is left out of the
	// CGF. The guards above tolerate a minority of them; say so even without
	// --verbose, or a wildcard scope loses packages without a word.
	var left []string
	for i, sp := range ssaPkgs {
		if sp == nil && i < len(pkgs) && len(pkgs[i].Errors) > 0 {
			left = append(left, pkgs[i].PkgPath)
		}
	}
	if len(left) > 0 {
		fmt.Fprintf(os.Stderr, "load-warn: %d of %d packages have type errors and are left out of the CGF (e.g. %s); run with --verbose for the errors\n",
			len(left), len(pkgs), strings.Join(firstN(left, 3), ", "))
	}
	buildPackages(init)
	tBuild := time.Since(tBuildStart)

	mod := moduleOf(pkgs)
	return &Loaded{
		Modules:       moduleMap(pkgs),
		deps:          depPackages(pkgs, prog, mockPaths),
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

// buildPackages builds the function bodies of pkgs in parallel.
//
// ssa.Package.Build is concurrency-safe on distinct packages (idempotent,
// self-synchronized), so this is a precision-free speedup of the SSA-build
// phase.
func buildPackages(pkgs []*ssa.Package) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, sp := range pkgs {
		wg.Add(1)
		sem <- struct{}{}
		go func(p *ssa.Package) {
			defer wg.Done()
			defer func() { <-sem }()
			p.Build()
		}(sp)
	}
	wg.Wait()
}

// DepPackages lists the dependency packages, sorted by import path: the ones
// that the scope did not match, that a module declares (so not the standard
// library) and that are not mock output (isMockPackage). Their functions have
// no emitted body, but a call can still reach them. Dispatch uses the list to
// name those targets.
func (l *Loaded) DepPackages() []*ssa.Package { return l.deps }

// BuildDeps builds the function bodies of DepPackages, so the call graph sees
// the flows inside them. It must run before CallGraph. Building does not make a
// dependency package part of the scope: nothing in it is emitted.
//
// ssautil.AllFunctions panics in x/tools v0.30.0 on some dependency bodies (a
// type parameter reaches Program.RuntimeTypes). So the function set is taken
// before the build, as AllFunctions finds it, and the functions of the
// dependency packages are added by reading the packages' own tables: package
// members, the declared methods of their named types and the anonymous
// functions nested in these. Nothing here creates a function.
func (l *Loaded) BuildDeps() {
	t0 := time.Now()
	l.vtaFuncs = ssautil.AllFunctions(l.Prog)
	buildPackages(l.deps)
	for _, sp := range l.deps {
		for _, m := range sp.Members {
			if fn, ok := m.(*ssa.Function); ok {
				addFunc(l.vtaFuncs, fn)
			}
		}
		scope := sp.Pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			for i := 0; i < named.NumMethods(); i++ {
				addFunc(l.vtaFuncs, l.Prog.FuncValue(named.Method(i)))
			}
		}
	}
	l.TDeps = time.Since(t0)
}

// addFunc adds fn and the anonymous functions nested in it.
func addFunc(set map[*ssa.Function]bool, fn *ssa.Function) {
	if fn == nil || set[fn] {
		return
	}
	set[fn] = true
	for _, anon := range fn.AnonFuncs {
		addFunc(set, anon)
	}
}

// depPackages finds the dependency packages among everything pkgs import.
func depPackages(pkgs []*packages.Package, prog *ssa.Program, mockPaths pkgclass.MockPaths) []*ssa.Package {
	roots := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		roots[p.PkgPath] = true
	}
	var out []*ssa.Package
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if roots[p.PkgPath] || p.Module == nil || p.Module.Path == "" || p.Types == nil || isMockPackage(p, mockPaths) {
			return
		}
		if sp := prog.Package(p.Types); sp != nil {
			out = append(out, sp)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg.Path() < out[j].Pkg.Path() })
	return out
}

// checkModule fails when dir does not exist, is not a directory, or is not
// inside a Go module. `go env GOMOD` prints an empty line, or the null device
// on Windows, outside one. If the go command cannot be run at all the module
// check is skipped and packages.Load reports it.
func checkModule(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("repo not found: %s", dir)
	}
	if err == nil && !fi.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if m := strings.TrimSpace(string(out)); m == "" || m == os.DevNull || m == "NUL" {
		return fmt.Errorf("no go.mod in %s or its parents: pc-fe reads Go modules (for TypeScript, use pc-fe-ts)", dir)
	}
	return nil
}

// unmatchedPattern returns the first --scope pattern that go list reported as
// matching nothing (a missing directory or an empty wildcard), but only when
// every returned entry is such a report. These entries carry the pattern as
// their ID and a list error with no position; any other failure, such as an
// unresolvable import, keeps the generic message.
func unmatchedPattern(pkgs []*packages.Package) string {
	first := ""
	for _, p := range pkgs {
		if len(p.GoFiles) > 0 || len(p.Errors) != 1 {
			return ""
		}
		e := p.Errors[0]
		if e.Kind != packages.ListError || e.Pos != "-" && e.Pos != "" {
			return ""
		}
		m := e.Msg
		if !strings.Contains(m, "directory not found") && !strings.Contains(m, "no such file or directory") &&
			!strings.Contains(m, "matched no packages") && !strings.Contains(m, "does not contain main module") {
			return ""
		}
		if first == "" {
			first = p.ID
		}
	}
	return first
}

// BuildCallGraph resolves interface/dynamic dispatch over the built SSA. VTA
// (default) is precise but whole-program and superlinear — the dominant
// extraction cost on large repos; CHA is near-linear but over-approximates
// (doc 21 §4.3b); "off" skips the build entirely — function-value calls are
// then opaque, interface calls get no targets but are not marked opaque.
// A panic inside the builder yields a nil graph; CallGraph reports it.
func BuildCallGraph(prog *ssa.Program, dispatch string) (*callgraph.Graph, time.Duration) {
	cg, d, _ := buildCallGraph(prog, dispatch, nil)
	return cg, d
}

// CallGraph is BuildCallGraph over the loaded program. After BuildDeps, VTA
// runs over the function set that BuildDeps collected. The error is set when
// the builder panicked (it can on incomplete SSA); the graph is then nil and
// the caller must say so, because every dynamic call is left without a target.
func (l *Loaded) CallGraph(dispatch string) (*callgraph.Graph, time.Duration, error) {
	return buildCallGraph(l.Prog, dispatch, l.vtaFuncs)
}

func buildCallGraph(prog *ssa.Program, dispatch string, funcs map[*ssa.Function]bool) (cg *callgraph.Graph, d time.Duration, err error) {
	t0 := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				cg, err = nil, fmt.Errorf("%s call graph panicked: %v", dispatch, r)
			}
		}()
		switch dispatch {
		case "off":
		case "cha":
			cg = cha.CallGraph(prog)
		default:
			if funcs == nil {
				funcs = ssautil.AllFunctions(prog)
			}
			cg = vta.CallGraph(funcs, nil)
		}
	}()
	return cg, time.Since(t0), err
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

// moduleMap maps every package reachable from pkgs to the path of its module.
// A replace directive does not change it: the path is the one the module's own
// go.mod declares, which is what the module's own extraction uses.
func moduleMap(pkgs []*packages.Package) map[string]string {
	m := map[string]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.Module != nil && p.Module.Path != "" {
			m[p.PkgPath] = p.Module.Path
		}
	})
	return m
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

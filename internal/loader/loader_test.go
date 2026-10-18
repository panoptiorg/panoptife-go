package loader

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Doc 16 §3.7 guards: error tolerance must never emit an (almost) empty CGF.

func TestLoadFailsLoudWhenNothingTypeChecks(t *testing.T) {
	_, err := Load("testdata/allbroken", "./...", false, true, false, nil)
	if err == nil {
		t.Fatal("expected loud failure when zero matched packages type-check")
	}
	if !strings.Contains(err.Error(), "refusing to emit") {
		t.Errorf("error should say extraction is refused, got: %v", err)
	}
}

func TestLoadToleratesMinorityTypeErrors(t *testing.T) {
	ld, err := Load("testdata/minoritybroken", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("minority of broken packages must stay tolerated: %v", err)
	}
	if len(ld.InitPkgs) < 2 {
		t.Errorf("expected the 2 good packages to load, got %d", len(ld.InitPkgs))
	}
}

// Debt A3. The three pre-existing guards all count over what `packages.Load`
// RETURNED, so a package that never came back is invisible to every one of them
// — one service emitted 158 of 485 requested packages, with grpc_methods=0 and
// exit 0. The only thing that catches it is the requested list.
func TestLoadFailsLoudWhenARequestedPackageNeverLoads(t *testing.T) {
	scope := "example.com/minoritybroken/good1,example.com/minoritybroken/nosuchpkg"
	_, err := Load("testdata/minoritybroken", scope, false, true, false, nil)
	if err == nil {
		t.Fatal("a requested package that never loaded must fail loudly, not extract a partial CGF")
	}
	if !strings.Contains(err.Error(), "never loaded") || !strings.Contains(err.Error(), "nosuchpkg") {
		t.Errorf("the error must name what is missing, got: %v", err)
	}
	// ...and the escape hatch must actually escape.
	if _, err := Load("testdata/minoritybroken", scope, false, true, true, nil); err != nil {
		t.Errorf("--allow-missing-scope must still extract: %v", err)
	}
}

// A wildcard legitimately matches any number of packages (including, at the
// margin, zero), so there is nothing to compare 1:1 and the guard must not fire
// — every fixture and every e2e in the tree passes `./...`.
func TestWildcardScopeIsNotChecked(t *testing.T) {
	if _, err := Load("testdata/minoritybroken", "./...", false, true, false, nil); err != nil {
		t.Fatalf("wildcard scope must not trip the requested-vs-loaded guard: %v", err)
	}
}

func TestMissingScopeIgnoresWildcardsAndExclusions(t *testing.T) {
	pats := []string{"a/b/wanted", "a/b/gone", "a/b/mocked", "a/b/...", "./x", "../y", "."}
	got := missingScope(pats, nil, map[string]bool{"a/b/mocked": true})
	// nil init ⇒ nothing built, so only the literals that are not deliberate
	// exclusions may be reported.
	want := []string{"a/b/gone", "a/b/wanted"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("missingScope = %v, want %v", got, want)
	}
}

func TestIsMockPackage(t *testing.T) {
	cases := []struct {
		pkgPath, name string
		files         []string
		want          bool
	}{
		// mockgen one-package-per-directory layout (gateway/backend-a/ledger style)
		{"gitlab.example.com/acme/gateway/internal/test/mock_events", "mock_events", []string{"/x/mock_events.go"}, true},
		{"gitlab.example.com/acme/ledger-svc/pkg/mock_billing", "mock_billing", []string{"/x/api.go"}, true},
		// classic segment forms (already handled pre-fix)
		{"a/b/mocks/c", "c", []string{"/x/c.go"}, true},
		{"a/b/mock", "mock", []string{"/x/m.go"}, true},
		{"a/b/foo_mock", "foo_mock", nil, false}, // suffix segment: only _mock.go files mark it
		{"a/b/foo_mock", "foo_mock", []string{"/x/foo_mock.go"}, true},
		// legit packages must survive
		{"a/b/mockingbird", "mockingbird", []string{"/x/bird.go"}, false},
		{"a/internal/test/helpers", "helpers", []string{"/x/h.go"}, false},
	}
	for _, c := range cases {
		p := &packages.Package{PkgPath: c.pkgPath, Name: c.name, GoFiles: c.files}
		if got := isMockPackage(p, nil); got != c.want {
			t.Errorf("isMockPackage(%s name=%s files=%v) = %v, want %v", c.pkgPath, c.name, c.files, got, c.want)
		}
	}
}

func TestCheckModule(t *testing.T) {
	err := checkModule(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no go.mod in") {
		t.Fatalf("empty dir: err = %v, want a no go.mod error", err)
	}
	if err := checkModule("testdata"); err != nil {
		t.Fatalf("dir inside a module: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if err := checkModule(missing); err == nil || err.Error() != "repo not found: "+missing {
		t.Fatalf("missing dir: err = %v, want repo not found", err)
	}
	file := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(file, []byte("module m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkModule(file); err == nil || err.Error() != "not a directory: "+file {
		t.Fatalf("file: err = %v, want not a directory", err)
	}
}

// The first iid field is the module that declares the function's package, not
// the module that was extracted.
func TestOwnerIsDeclaringModule(t *testing.T) {
	ld, err := Load("../../fixtures/xmod/app", "./...", false, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	const app, lib = "example.com/xmod/app", "example.com/xmod/lib"
	owner := hash.Owner{Repo: ld.Module, Modules: ld.Modules}
	if ld.Module != app {
		t.Fatalf("module = %q, want %q", ld.Module, app)
	}

	fetch := ld.Prog.ImportedPackage(lib).Func("Fetch")
	toUpper := ld.Prog.ImportedPackage("strings").Func("ToUpper")
	user := ld.Prog.ImportedPackage(app).Func("user")
	var mapInst *ssa.Function
	for fn := range ssautil.AllFunctions(ld.Prog) {
		if strings.HasPrefix(fn.String(), lib+".Map[") {
			mapInst = fn
		}
	}
	if fetch == nil || toUpper == nil || user == nil || mapInst == nil {
		t.Fatalf("missing function: Fetch=%v ToUpper=%v user=%v Map instance=%v", fetch, toUpper, user, mapInst)
	}
	// The app requires lib through a replace directive; the owner is still the
	// module path lib declares in its own go.mod.
	if m := ld.Modules[lib]; m != lib {
		t.Errorf("Modules[%s] = %q, want %q", lib, m, lib)
	}
	for _, c := range []struct {
		name string
		fn   *ssa.Function
		want string
	}{
		{"function of the extracted module", user, app},
		{"function of another module", fetch, lib},
		{"generic instance, no package of its own", mapInst, lib},
		{"standard library", toUpper, app},
	} {
		if got := owner.Of(c.fn); got != c.want {
			t.Errorf("%s: owner %q, want %q", c.name, got, c.want)
		}
		if !bytes.Equal(owner.IID(c.fn), hash.IID(c.want, c.fn)) {
			t.Errorf("%s: Owner.IID differs from IID(%q)", c.name, c.want)
		}
	}
	// Without a module map every function belongs to the extracted repo.
	if got := (hash.Owner{Repo: "r"}).Of(fetch); got != "r" {
		t.Errorf("no module map: owner %q, want r", got)
	}
}

// Several modules in one go.work workspace: each package is owned by its own
// module, whichever module comes first.
func TestOwnerInWorkspace(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.work", "go 1.25\n\nuse (\n\t./a\n\t./b\n)\n")
	write("a/go.mod", "module example.com/ws/a\n\ngo 1.25\n")
	write("a/a.go", "package a\n\nimport \"example.com/ws/b\"\n\nfunc A() string { return b.B() }\n")
	write("b/go.mod", "module example.com/ws/b\n\ngo 1.25\n")
	write("b/b.go", "package b\n\nfunc B() string { return \"b\" }\n")

	ld, err := Load(filepath.Join(dir, "a"), "./...", false, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := hash.Owner{Repo: ld.Module, Modules: ld.Modules}
	for pkg, want := range map[string]string{"example.com/ws/a": "example.com/ws/a", "example.com/ws/b": "example.com/ws/b"} {
		sp := ld.Prog.ImportedPackage(pkg)
		if sp == nil {
			t.Fatalf("package %s not loaded", pkg)
		}
		name := strings.ToUpper(pkg[len(pkg)-1:])
		if got := owner.Of(sp.Func(name)); got != want {
			t.Errorf("%s.%s: owner %q, want %q", pkg, name, got, want)
		}
	}
}

// With -mod=vendor go list still reports the module of a vendored package,
// from vendor/modules.txt.
func TestOwnerInVendorMode(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dep/go.mod", "module example.com/vend/dep\n\ngo 1.25\n")
	write("dep/dep.go", "package dep\n\nfunc D() string { return \"d\" }\n")
	write("app/go.mod", "module example.com/vend/app\n\ngo 1.25\n\nrequire example.com/vend/dep v0.0.0\n\nreplace example.com/vend/dep => ../dep\n")
	write("app/app.go", "package app\n\nimport \"example.com/vend/dep\"\n\nfunc A() string { return dep.D() }\n")
	cmd := exec.Command("go", "mod", "vendor")
	cmd.Dir = filepath.Join(dir, "app")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go mod vendor: %v\n%s", err, out)
	}

	t.Setenv("GOFLAGS", "-mod=vendor")
	ld, err := Load(filepath.Join(dir, "app"), "./...", false, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := ld.Modules["example.com/vend/dep"]; m != "example.com/vend/dep" {
		t.Errorf("vendored package: module %q, want example.com/vend/dep", m)
	}
}

// The scope's own packages are built; dependency packages are listed and built
// only on request. Mock packages and the standard library are not dependency
// packages here.
func TestBuildDeps(t *testing.T) {
	ld, err := Load("../../fixtures/xmod/app", "./...", false, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var deps []string
	for _, sp := range ld.DepPackages() {
		deps = append(deps, sp.Pkg.Path())
	}
	if strings.Join(deps, ",") != "example.com/xmod/lib,example.com/xmod/lib/pb" {
		t.Fatalf("dependency packages %v, want lib and lib/pb", deps)
	}
	fetch := ld.Prog.ImportedPackage("example.com/xmod/lib").Func("Fetch")
	if len(fetch.Blocks) != 0 {
		t.Fatal("a dependency package must not be built by Load")
	}
	ld.BuildDeps()
	if len(fetch.Blocks) == 0 || ld.TDeps <= 0 {
		t.Errorf("after BuildDeps: %d blocks, took %v", len(fetch.Blocks), ld.TDeps)
	}
	if len(ld.InitPkgs) != 1 {
		t.Errorf("BuildDeps changed the scope: %d in-scope packages", len(ld.InitPkgs))
	}
}

// A panic inside the call-graph builder is returned, not swallowed: the caller
// must say that dispatch is gone.
func TestCallGraphPanicIsReported(t *testing.T) {
	ld, err := Load("../../fixtures/xmod/app", "./...", false, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ld.vtaFuncs = map[*ssa.Function]bool{nil: true}
	cg, _, err := ld.CallGraph("vta")
	if err == nil || cg != nil || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("CallGraph over a broken function set = (%v, %v), want a nil graph and a panic error", cg, err)
	}
	ld.vtaFuncs = nil
	if cg, _, err := ld.CallGraph("vta"); err != nil || cg == nil {
		t.Errorf("CallGraph = (%v, %v), want a graph", cg, err)
	}
}

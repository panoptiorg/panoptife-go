package loader

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
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

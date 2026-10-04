package pkgclass

import "testing"

func TestPbPathsDefaultReproducesHistoricalRule(t *testing.T) {
	// The pre-flag rule, verbatim: Contains("/pb/") || HasSuffix("/pb") ||
	// Contains("/api/") || HasSuffix("/api").
	cases := []struct {
		path string
		want bool
	}{
		{"gitlab.example.com/acme/ledger-svc/pb", true},          // trailing segment
		{"gitlab.example.com/acme/ledger-svc/pb/ledger", true},   // inner segment
		{"gitlab.example.com/acme/ledger-svc/api", true},         // trailing segment
		{"gitlab.example.com/acme/ledger-svc/api/v1", true},      // inner segment
		{"gitlab.example.com/acme/ledger-svc/internal/pb", true}, // deep trailing
		{"gitlab.example.com/acme/ledger-svc/gen/ledger", false}, // not in the default list
		{"gitlab.example.com/acme/ledger-svc/store", false},
		{"gitlab.example.com/acme/pbx", false},     // segment prefix is not a segment
		{"gitlab.example.com/acme/apis", false},    // ditto
		{"gitlab.example.com/acme/rapid/pb", true}, // suffix, not substring, of the last element
		{"pb", false}, // bare top-level path: the old rule missed it too
	}
	var zero PbPaths // zero value must be the default
	for _, c := range cases {
		if got := zero.Match(c.path); got != c.want {
			t.Errorf("default PbPaths.Match(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if zero.String() != "pb,api" {
		t.Errorf("default PbPaths.String() = %q, want %q", zero.String(), "pb,api")
	}
}

func TestPbPathsCustomList(t *testing.T) {
	p := PbPaths{"gen", "genproto"}
	if !p.Match("github.com/acme/svc/gen/foo") {
		t.Error("custom list must match its own segments")
	}
	if !p.Match("github.com/acme/svc/genproto") {
		t.Error("custom list must match a trailing segment")
	}
	if p.Match("github.com/acme/svc/pb") {
		t.Error("a custom list REPLACES the defaults; pb must no longer match")
	}
	if p.String() != "gen,genproto" {
		t.Errorf("String() = %q", p.String())
	}
}

func TestMockPaths(t *testing.T) {
	var zero MockPaths
	for _, p := range []string{"a/b/mocks/c", "a/b/mock", "mocks/x"} {
		if !zero.Match(p) {
			t.Errorf("default MockPaths.Match(%q) = false, want true", p)
		}
	}
	// Whole path ELEMENTS only — the loader's historical rule.
	for _, p := range []string{"a/b/mockingbird", "a/b/foo_mock", "a/b/mock_events"} {
		if zero.Match(p) {
			t.Errorf("default MockPaths.Match(%q) = true, want false", p)
		}
	}
	if zero.String() != "mock,mocks" {
		t.Errorf("default MockPaths.String() = %q", zero.String())
	}
	if (MockPaths{"testdouble"}).Match("a/b/mocks/c") {
		t.Error("a custom list replaces the defaults")
	}
}

func TestParseSegments(t *testing.T) {
	got, err := ParsePb("pb, api ,/gen/")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "pb,api,gen" {
		t.Errorf("ParsePb = %q, want %q (trimmed, slashes stripped)", got.String(), "pb,api,gen")
	}
	// Empty input keeps the defaults rather than matching nothing.
	empty, err := ParsePb("  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 || empty.String() != "pb,api" {
		t.Errorf("ParsePb(blank) = %v (%q), want the defaults", empty, empty.String())
	}
	if _, err := ParsePb("pb,a/b"); err == nil {
		t.Error("a multi-element segment must be rejected, not silently mismatched")
	}
	if _, err := ParseMock("mock,x/y"); err == nil {
		t.Error("--mock-paths must reject multi-element segments too")
	}
}

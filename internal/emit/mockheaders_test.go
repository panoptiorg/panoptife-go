package emit

import (
	"sort"
	"strings"
	"testing"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

const mockFixture = "../../fixtures/mockheaders"

func fqns(pkgs map[string]*pb.CgfPackage) []string {
	var out []string
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			out = append(out, f.Fqn)
		}
	}
	sort.Strings(out)
	return out
}

func hasFqn(pkgs map[string]*pb.CgfPackage, fqn string) bool { return fnByFqn(pkgs, fqn) != nil }

// A mock file is found by its generator header, not by its name. Each of the
// three generators puts a mock into the real package `store` under a name the
// file-name rules miss; none of them may be emitted, and the real functions
// of the same package must stay.
func TestHeaderMocksExcludedRealCodeKept(t *testing.T) {
	for _, mode := range []string{"vta", "cha"} {
		for _, include := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "", true: "/include-mocks"}[include], func(t *testing.T) {
				pkgs, _ := runCGF(t, Options{RepoDir: mockFixture, Dispatch: mode, IncludeMocks: include})
				for _, mock := range []string{
					"(*example.com/mockheaders/store.FakeGen).Get",
					"(*example.com/mockheaders/store.FakeMockery).Get",
					"(*example.com/mockheaders/store.FakeMinimock).Get",
					"(*example.com/mockheaders/storetest.Double).Get",
				} {
					if hasFqn(pkgs, mock) {
						t.Errorf("mock function %s was emitted", mock)
					}
				}
				for _, real := range []string{
					"(*example.com/mockheaders/store.SQL).Get",
					"example.com/mockheaders/store.New",
					"example.com/mockheaders/app.Lookup",
					"example.com/mockheaders/app.Describe",
					"example.com/mockheaders/mockingbird.Sing",
				} {
					if !hasFqn(pkgs, real) {
						t.Errorf("real function %s is missing; emitted: %v", real, fqns(pkgs))
					}
				}
			})
		}
	}
}

// Without --include-mocks, a package whose every file has a mock header is a
// mock package, whatever its name. A package named like a mock but holding
// real code is not.
func TestHeaderMockPackageDropped(t *testing.T) {
	pkgs, stderr := runCGF(t, Options{RepoDir: mockFixture})
	if _, ok := pkgs["example.com/mockheaders/storetest"]; ok {
		t.Error("storetest holds only generated mocks and must be dropped")
	}
	if _, ok := pkgs["example.com/mockheaders/mockingbird"]; !ok {
		t.Error("mockingbird is real code and must be kept")
	}
	if !strings.Contains(stderr, "mocks-excluded=1") {
		t.Errorf("want exactly one mock package excluded, timing line: %s", line(t, stderr, "pc-fe timing:"))
	}
}

// A mock file in a real package is also gone from dispatch: class hierarchy
// analysis finds every implementation of store.Store, but the interface call in
// Lookup may only resolve to the real one.
func TestHeaderMocksAreNotDispatchTargets(t *testing.T) {
	pkgs, _ := runCGF(t, Options{RepoDir: mockFixture, Dispatch: "cha"})
	f := fnByFqn(pkgs, "example.com/mockheaders/app.Lookup")
	var got []string
	for _, cs := range f.Flow.Callsites {
		if cs.Kind != pb.CallSite_VIRTUAL {
			continue
		}
		if len(cs.CalleeIids) != 1 {
			t.Errorf("interface call has %d targets, want only the real implementation", len(cs.CalleeIids))
		}
		got = append(got, cs.CalleeFqn)
	}
	if len(got) != 1 {
		t.Fatalf("want one interface call site in Lookup, got %v", got)
	}
}

// Generated code that is not a mock stays emitted and flagged; a generator
// word in a hand-written file's comments does not make it a mock.
func TestGeneratedNonMockKept(t *testing.T) {
	pkgs, _ := runCGF(t, Options{RepoDir: mockFixture})
	g := fnByFqn(pkgs, "(*example.com/mockheaders/pb.Account).GetName")
	if g == nil {
		t.Fatal("protoc-generated getter must stay emitted")
	}
	if !g.Generated || g.Generator != pb.Generator_GEN_PROTOC {
		t.Errorf("generated=%v generator=%v, want true GEN_PROTOC", g.Generated, g.Generator)
	}
	d := fnByFqn(pkgs, "example.com/mockheaders/app.Describe")
	if d == nil || d.Generated {
		t.Errorf("hand-written Describe must be emitted and not generated, got %+v", d)
	}
}

func TestGeneratorOfAndExcludedFileUseOnePredicate(t *testing.T) {
	isMock := func(p string) bool { return strings.HasSuffix(p, "/fake_gen.go") }
	if !excludedFile("/x/fake_gen.go", isMock) || generatorOf("/x/fake_gen.go", isMock) != pb.Generator_GEN_MOCKGEN {
		t.Error("a file the predicate calls a mock must be excluded and marked GEN_MOCKGEN")
	}
	if excludedFile("/x/real.go", isMock) || generatorOf("/x/real.go", isMock) != pb.Generator_GEN_NONE {
		t.Error("a file the predicate does not call a mock must be kept")
	}
	if generatorOf("/x/a.pb.go", isMock) != pb.Generator_GEN_PROTOC {
		t.Error("protoc naming must still win")
	}
}

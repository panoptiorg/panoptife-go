package flow

import (
	"strings"
	"testing"

	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// fixtures/pbgetter pins the two proofs behind trivialPbGetter on one fixture:
//
//   - pb in scope: the SSA body is `return x.F` modulo the generated
//     nil-check — the historical rule (must not change);
//   - pb out of scope (a --scope that drops every /pb/ package — the common
//     extraction shape): the getters have no body, and the go/types rule must
//     still emit the projection, or the opaque call's default leaf taints the
//     clean field's read from the whole request.
//
// A canonicalized getter keeps a ZERO-ARG shell call site: the projection
// carries the field-precise flow (no arg ports, so no whole-object smear),
// and the shell's callee fqn + result port stay as the hook the core's
// catalog [[sources]] rules match — dropping the call site entirely made
// every getter rule inert under field paths.
//
// The negative controls keep their FULL call sites (arg ports intact) when
// the pb package is out of scope: HandRec (in the pb-path package but not
// generated protobuf output) and FieldRec.GetH (result type ≠ same-named
// field's type). With the bodies built, HandRec's trivial getter is covered
// by the SSA rule and GetH keeps its barrier by the body check (it builds a
// literal).
func TestPbGetterCanonicalized(t *testing.T) {
	for _, tc := range []struct {
		scope     string
		wantShell string   // getter FQNs that must be shell calls (argc 0)
		wantKept  []string // call sites that must keep their full barrier
		wantPaths []string
	}{
		{
			// All bodies built: the SSA rule covers the trivial getters,
			// including HandRec's (its body is trivial too).
			scope:     "./...",
			wantShell: "/pb.Req).Get",
			wantKept:  []string{"/pb.FieldRec).GetH"},
			wantPaths: []string{"A", "B", "Choice"},
		},
		{
			// Only ./app built: the pb getters are bodiless — the go/types
			// rule applies; the non-generated and type-mismatched getters
			// stay real calls.
			scope:     "./app",
			wantShell: "/pb.Req).Get",
			wantKept:  []string{"/pb.HandRec).GetA", "/pb.FieldRec).GetH"},
			wantPaths: []string{"A", "B", "Choice"},
		},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			ld, err := loader.Load("../../fixtures/pbgetter", tc.scope, false, true, false, nil)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			handler := func(fqn string) *Result {
				t.Helper()
				_, res := buildFnFor(t, ld, fqn, true)
				return res
			}
			all := []*Result{handler("example.com/pbgetter/app.Handler"),
				handler("example.com/pbgetter/app.Handler2")}
			var shells, kept []string
			for _, res := range all {
				for _, cs := range res.Flow.Callsites {
					switch {
					case strings.Contains(cs.CalleeFqn, tc.wantShell):
						if cs.Argc != 0 {
							t.Errorf("getter call site %q kept its %d arg ports — must be a zero-arg shell", cs.CalleeFqn, cs.Argc)
						}
						shells = append(shells, cs.CalleeFqn)
					default:
						kept = append(kept, cs.CalleeFqn)
					}
				}
			}
			if len(shells) == 0 {
				t.Errorf("no shell call site for %q — the getter source hook is gone", tc.wantShell)
			}
			// path variants are asserted on Handler (all three getters read there)
			for _, want := range tc.wantPaths {
				if !hasFieldPathVariant(all[0], want) {
					t.Errorf("no source path variant [%s] — the getter projection did not materialize", want)
				}
			}
			// barrier keepers are asserted across both handlers
			for _, want := range tc.wantKept {
				found := false
				for _, got := range kept {
					if strings.Contains(got, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected barrier call site %q missing among %v", want, kept)
				}
			}
		})
	}
}

// hasFieldPathVariant reports whether any source path-variant vertex carries
// exactly the one-field path [name].
func hasFieldPathVariant(res *Result, name string) bool {
	for _, v := range res.Flow.Vertices {
		if len(v.FieldNames) == 1 && v.FieldNames[0] == name {
			return true
		}
	}
	return false
}

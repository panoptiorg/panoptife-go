package flow

import (
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// A16 tags must compare type IDENTITY: the reader asserts `.(string)` and
// every writer in testdata/ifacealias stores a string-identical value, one of
// them through an alias-typed parameter and two through a generic instance
// whose T was first spelled with the alias. With HeapIfaceIdentity every
// writer's tag equals the reader's; with the old spelling-based tags the alias
// writers differ — which is what made the core cut true flows.
func TestHeapIfaceTagsCompareIdentity(t *testing.T) {
	ld, err := loader.Load("testdata/ifacealias", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fns := map[string]*ssa.Function{}
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.Blocks != nil {
			fns[f.String()] = f
		}
	}
	find := func(sub string) *ssa.Function {
		t.Helper()
		for name, f := range fns {
			if strings.Contains(name, sub) {
				return f
			}
		}
		t.Fatalf("no function matching %q", sub)
		return nil
	}
	tags := func(f *ssa.Function, kind pb.VertexKind, identity bool) []string {
		t.Helper()
		o := Opts{FieldPaths: true, ClosureFlow: true, HeapSlots: true, HeapAllFields: true,
			HeapIfaceNarrow: true, HeapIfaceIdentity: identity}
		var out []string
		for _, v := range Build(f, nil, o).Flow.Vertices {
			if v.Kind == kind && len(v.IfaceType) > 0 {
				out = append(out, hex.EncodeToString(v.IfaceType))
			}
		}
		return out
	}
	reader := find("ifacealias/app.Read")
	writers := map[string]*ssa.Function{
		"alias-typed parameter": find("ifacealias/app.StoreAlias"),
		"generic instance":      find("ifacealias/gen.Store["),
	}
	for _, identity := range []bool{true, false} {
		rt := tags(reader, pb.VertexKind_IN_GLOBAL, identity)
		if len(rt) != 1 {
			t.Fatalf("identity=%v: reader must carry exactly one assertion tag, got %v", identity, rt)
		}
		mismatched := 0
		for what, w := range writers {
			wt := tags(w, pb.VertexKind_OUT_FIELD, identity)
			if len(wt) == 0 {
				t.Fatalf("identity=%v: %s writer carries no tag", identity, what)
			}
			for _, tag := range wt {
				if tag != rt[0] {
					mismatched++
					if identity {
						t.Errorf("%s: writer tag %s != reader tag %s — the core would cut a true flow", what, tag[:12], rt[0][:12])
					}
				}
			}
		}
		if !identity && mismatched == 0 {
			t.Fatal("fixture broken: the spelling-based tags agree too, so this test proves nothing")
		}
	}
}

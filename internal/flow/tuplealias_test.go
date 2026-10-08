package flow

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa/ssautil"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// edgeDescs names an edge by what its endpoints ARE, not by vertex id: ids
// shift when a variant appears or disappears, descriptors do not.
func edgeDescs(fl *pb.LocalFlow) map[string]bool {
	vx := map[uint32]*pb.FlowVertex{}
	for _, v := range fl.Vertices {
		vx[v.Id] = v
	}
	name := func(v *pb.FlowVertex) string {
		callee := ""
		if v.Kind == pb.VertexKind_CALL_ARG_PORT || v.Kind == pb.VertexKind_CALL_RESULT_PORT {
			callee = fl.Callsites[v.CallsiteId].CalleeFqn
		}
		return fmt.Sprintf("%s#%d%v@%s", v.Kind, v.Index, v.FieldPath, callee)
	}
	out := map[string]bool{}
	for _, e := range fl.Edges {
		out[name(vx[e.From])+" -> "+name(vx[e.To])] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// doc 31 §6a measured --error-results-strict at "+79 FP keys removed, 2 REAL
// keys lost", attributing the loss to result-0 path variants only the tuple
// walk built. On the current builder, wireExtracts registers Extract #0 for
// variant generation too, so strict should remove exactly the err leak and
// nothing else. This pins that: if strict ever removes anything beyond result
// 0's edges into the consumers of `err` — in particular the result-0 field
// variant — the doc-31 cost is back and must be re-measured
// (tech-debt B1r; the internal re-measurement is pending).
func TestErrorResultsStrictRemovesOnlyTheLeak(t *testing.T) {
	ld, err := loader.Load("testdata/tuplealias", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	build := func(fqn string, strict bool) map[string]bool {
		t.Helper()
		o := Opts{FieldPaths: true, ClosureFlow: true, ErrorResults: true, ErrorResultsStrict: strict}
		for f := range ssautil.AllFunctions(ld.Prog) {
			if f.String() == fqn && f.Blocks != nil {
				return edgeDescs(Build(f, nil, o).Flow)
			}
		}
		t.Fatalf("function %q not found", fqn)
		return nil
	}
	// leak edges: result 0 of the producer -> an arg port fed only by err
	leaks := map[string][2]string{
		"example.com/tuplealias.ErrOnly": {"encoding/json.Marshal", "example.com/tuplealias.sinkErr"},
		"example.com/tuplealias.Both":    {"strconv.Atoi", "(error).Error"},
		"example.com/tuplealias.Field":   {"net/url.Parse", "example.com/tuplealias.sinkErr"},
	}
	for fqn, lk := range leaks {
		def, str := build(fqn, false), build(fqn, true)
		isLeak := func(d string) bool {
			from, to, _ := strings.Cut(d, " -> ")
			return strings.HasPrefix(from, "CALL_RESULT_PORT#0") && strings.HasSuffix(from, "@"+lk[0]) &&
				strings.HasSuffix(to, "@"+lk[1])
		}
		had := false
		for d := range def {
			if isLeak(d) {
				had = true
			}
		}
		if !had {
			t.Fatalf("%s: fixture broken — the default emission must show the leak %s -> %s; edges:\n%s",
				fqn, lk[0], lk[1], strings.Join(sortedKeys(def), "\n"))
		}
		for d := range str {
			if isLeak(d) {
				t.Errorf("%s: strict still leaks: %s", fqn, d)
			}
			if !def[d] {
				t.Errorf("%s: strict ADDED an edge (it may only remove): %s", fqn, d)
			}
		}
		for d := range def {
			if !str[d] && !isLeak(d) {
				t.Errorf("%s: strict removed an edge that is not the err leak: %s", fqn, d)
			}
		}
		// the error's own port still reaches the error's consumer
		ownPort := false
		for d := range str {
			from, to, _ := strings.Cut(d, " -> ")
			if strings.HasPrefix(from, "CALL_RESULT_PORT#1") && strings.HasSuffix(to, "@"+lk[1]) {
				ownPort = true
			}
		}
		if !ownPort {
			t.Errorf("%s: result 1 must still reach %s through its own port", fqn, lk[1])
		}
	}
	// Field: the result-0 path variant ([Host]) and its edge to sink survive.
	str := build("example.com/tuplealias.Field", true)
	variant := false
	for d := range str {
		from, to, _ := strings.Cut(d, " -> ")
		if strings.HasPrefix(from, "CALL_RESULT_PORT#0[") && !strings.HasPrefix(from, "CALL_RESULT_PORT#0[]") &&
			strings.HasSuffix(to, "@example.com/tuplealias.sink") {
			variant = true
		}
	}
	if !variant {
		t.Errorf("Field: under strict the result-0 field variant must still reach sink(u.Host); edges:\n%s",
			strings.Join(sortedKeys(str), "\n"))
	}
}

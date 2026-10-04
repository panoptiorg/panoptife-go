package callgraph

import (
	"testing"

	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// snapshotFixture builds a live resolver over fixtures/dispatch, captures the
// narrow 2-target site as persisted rows, and returns everything a snapshot
// replay needs.
func snapshotFixture(t *testing.T) (site ssa.CallInstruction, fn *ssa.Function, targets []*ssa.Function, meta *pb.Meta, shards map[string]*pb.PkgShard, byIID map[string]*ssa.Function) {
	t.Helper()
	ld, cg := loadFixture(t)
	site = findInvoke(t, ld.Prog, "HandleNarrow", "Save")
	fn = site.Parent()
	var conf float32
	targets, conf, _ = NewResolver(cg, DefaultFanoutCap, nil).TargetsAt(site)
	if len(targets) != 2 {
		t.Fatalf("fixture drift: narrow site has %d targets", len(targets))
	}

	byIID = map[string]*ssa.Function{string(hash.IID("r", fn)): fn}
	var calleeIIDs [][]byte
	for _, tg := range targets {
		id := hash.IID("r", tg)
		byIID[string(id)] = tg
		calleeIIDs = append(calleeIIDs, id)
	}
	calls := callInstructions(fn, nil)
	ord := -1
	for i, c := range calls {
		if c == site {
			ord = i
		}
	}
	if ord < 0 {
		t.Fatal("site not found in enumeration")
	}
	meta = &pb.Meta{GenericEdges: 1, SitesAllGeneric: 2, SitesSomeGeneric: 3}
	shards = map[string]*pb.PkgShard{"p": {Pkg: "p", Callers: []*pb.CallerEdges{{
		CallerIid:  hash.IID("r", fn),
		NCallsites: uint32(len(calls)),
		Sites: []*pb.SiteEdge{{
			CallsiteId: uint32(ord), CalleeIids: calleeIIDs, Confidence: conf,
		}},
	}}}}
	return
}

func TestSnapshotReplayMatchesLive(t *testing.T) {
	site, _, want, meta, shards, byIID := snapshotFixture(t)
	sr, err := NewSnapshotResolver(meta, shards, byIID, nil)
	if err != nil {
		t.Fatalf("NewSnapshotResolver: %v", err)
	}
	got, conf, capped := sr.TargetsAt(site)
	if capped || conf != 0.5 || len(got) != len(want) {
		t.Fatalf("replay = (%d targets, %v, %v), want (%d, 0.5, false)", len(got), conf, capped, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replay target %d = %s, want %s (order must be preserved)", i, got[i], want[i])
		}
	}
	if sr.GenericEdges != 1 || sr.SitesAllGeneric != 2 || sr.SitesSomeGeneric != 3 {
		t.Fatal("generics counters not replayed from meta")
	}
	// unpersisted sites replay as unresolved
	if tg, c, cap2 := sr.TargetsAt(nil); tg != nil || c != 1.0 || cap2 {
		t.Fatal("nil site must be unresolved")
	}
}

func TestSnapshotValidationFailures(t *testing.T) {
	_, _, _, meta, shards, byIID := snapshotFixture(t)
	ce := shards["p"].Callers[0]

	mutations := map[string]func() func(){
		"unknown caller": func() func() {
			old := ce.CallerIid
			ce.CallerIid = []byte("nope-nope-nope-nope-nope-nope-32")
			return func() { ce.CallerIid = old }
		},
		"callsite count drift": func() func() {
			old := ce.NCallsites
			ce.NCallsites++
			return func() { ce.NCallsites = old }
		},
		"callsite id out of range": func() func() {
			old := ce.Sites[0].CallsiteId
			ce.Sites[0].CallsiteId = ce.NCallsites + 100
			return func() { ce.Sites[0].CallsiteId = old }
		},
		"unknown callee": func() func() {
			old := ce.Sites[0].CalleeIids[0]
			ce.Sites[0].CalleeIids[0] = []byte("bogus-bogus-bogus-bogus-bogus-32")
			return func() { ce.Sites[0].CalleeIids[0] = old }
		},
	}
	for name, mutate := range mutations {
		restore := mutate()
		if _, err := NewSnapshotResolver(meta, shards, byIID, nil); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
		restore()
	}
	// sanity: restored snapshot validates again
	if _, err := NewSnapshotResolver(meta, shards, byIID, nil); err != nil {
		t.Fatalf("restored snapshot should validate: %v", err)
	}
}

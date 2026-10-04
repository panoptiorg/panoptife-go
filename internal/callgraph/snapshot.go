package callgraph

import (
	"fmt"

	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
)

// SnapshotResolver replays persisted dispatch decisions (cgstore, R4 Ph1)
// against freshly rebuilt SSA — TargetsAt answers from the snapshot, so the
// vta/cha whole-program build is skipped entirely. Construction validates the
// snapshot against the rebuilt program; ANY anomaly returns an error and the
// caller falls back to a full graph build (soundness > reuse, never guess).
type SnapshotResolver struct {
	sites map[ssa.CallInstruction]snapEntry

	// Generics-gap counters replayed from the snapshot meta (they were
	// accumulated over the same function universe at capture time).
	GenericEdges     int
	SitesAllGeneric  int
	SitesSomeGeneric int
}

type snapEntry struct {
	targets []*ssa.Function
	conf    float32
	capped  bool
}

// NewSnapshotResolver rebinds persisted (caller iid, callsite ordinal, callee
// iids) rows to live *ssa.Function / ssa.CallInstruction values. The ordinal
// key is the index of the CallInstruction in deterministic fn.Blocks/Instrs
// order — identical to the CGF CallSite.Id assignment in flow.scanInstrs, and
// run-to-run stable at an unchanged commit (the same determinism the bid
// cache and the e2e double-extract guard already rely on).
//
// Validated invariants (violation ⇒ error ⇒ full rebuild):
//   - every persisted caller iid resolves to an emittable function;
//   - the caller's re-enumerated CallInstruction count equals n_callsites;
//   - every callsite_id is in range;
//   - every persisted callee iid resolves (targets were emittable at capture,
//     so a miss means the emittable set drifted under the store key).
//
// skipCall (nil = keep all) drops CallInstructions that emit no CallSite —
// with field paths on, flow canonicalizes trivial pb getters, so the ordinal
// enumeration must apply the same filter (emit passes flow.IsCanonicalizedCall
// when meta.FieldPaths; the flag is also in the store key, so a snapshot is
// only ever replayed under the mode that captured it).
func NewSnapshotResolver(meta *pb.Meta, shards map[string]*pb.PkgShard, fnByIID map[string]*ssa.Function, skipCall func(ssa.CallInstruction) bool) (*SnapshotResolver, error) {
	r := &SnapshotResolver{
		sites:            map[ssa.CallInstruction]snapEntry{},
		GenericEdges:     int(meta.GenericEdges),
		SitesAllGeneric:  int(meta.SitesAllGeneric),
		SitesSomeGeneric: int(meta.SitesSomeGeneric),
	}
	for _, shard := range shards {
		for _, ce := range shard.Callers {
			fn := fnByIID[string(ce.CallerIid)]
			if fn == nil {
				return nil, fmt.Errorf("caller iid %x not in rebuilt fn set", ce.CallerIid[:6])
			}
			calls := callInstructions(fn, skipCall)
			if uint32(len(calls)) != ce.NCallsites {
				return nil, fmt.Errorf("%s: callsite count drift (snapshot %d, rebuilt %d)", fn, ce.NCallsites, len(calls))
			}
			for _, se := range ce.Sites {
				if se.CallsiteId >= uint32(len(calls)) {
					return nil, fmt.Errorf("%s: callsite_id %d out of range", fn, se.CallsiteId)
				}
				e := snapEntry{conf: se.Confidence, capped: se.OpaqueByCap}
				for _, iid := range se.CalleeIids {
					t := fnByIID[string(iid)]
					if t == nil {
						return nil, fmt.Errorf("%s: callee iid %x not in rebuilt fn set", fn, iid[:6])
					}
					e.targets = append(e.targets, t)
				}
				r.sites[calls[se.CallsiteId]] = e
			}
		}
	}
	return r, nil
}

// callInstructions enumerates fn's CallInstructions in the same deterministic
// order flow.scanInstrs assigns CallSite.Id: fn.Blocks order, Instrs order.
func callInstructions(fn *ssa.Function, skip func(ssa.CallInstruction) bool) []ssa.CallInstruction {
	var out []ssa.CallInstruction
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if call, ok := instr.(ssa.CallInstruction); ok {
				if skip != nil && skip(call) {
					continue
				}
				out = append(out, call)
			}
		}
	}
	return out
}

// TargetsAt replays the persisted decision for site; sites the live resolver
// left unresolved are absent and replay as (nil, 1.0, false) — the exact live
// behavior, byte-for-byte in the emitted CGF.
func (r *SnapshotResolver) TargetsAt(site ssa.CallInstruction) (targets []*ssa.Function, confidence float32, cappedOpaque bool) {
	if r == nil || site == nil {
		return nil, 1.0, false
	}
	e, ok := r.sites[site]
	if !ok {
		return nil, 1.0, false
	}
	if e.capped {
		return nil, 1.0, true
	}
	return e.targets, e.conf, false
}

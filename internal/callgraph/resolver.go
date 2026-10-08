// Package callgraph resolves virtual-dispatch targets at individual call sites
// from a whole-program call graph (VTA or CHA). Sites whose fan-out
// exceeds the cap are reported opaque so emit falls back to the default-leaf
// iid, keeping wide interfaces exactly as cheap (and as over-approximate) as
// before dispatch wiring.
package callgraph

import (
	"sort"

	xcg "golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// DefaultFanoutCap bounds per-site target enumeration. Beyond it a site is
// opaque: the summary-set union over that many impls costs more precision-wise
// than the default leaf buys back (in a large Go+gRPC fleet, real-dispatch
// interfaces run 2-20 impls; wider ones are catch-all shapes like Stringer).
const DefaultFanoutCap = 10

// Resolver answers per-call-site dispatch queries against a built call graph.
// A nil *Resolver is valid and resolves nothing (dispatch=off).
type Resolver struct {
	g   *xcg.Graph
	cap int
	// keep is emit's emittability predicate: a target may appear in CalleeIids
	// only if emit will write that function, otherwise the iid dangles and the
	// chain silently leafs there. nil falls back to the historical local check.
	keep func(*ssa.Function) bool
	// per-function site buckets, built lazily on first query for that function
	memo map[*ssa.Function]map[ssa.CallInstruction][]*ssa.Function

	// Residual generics-gap counters: edges whose instantiation callee is still
	// dropped (unbuilt body / out-of-scope origin). Accumulated at index time
	// over every queried function; emit reports them on stderr.
	GenericEdges     int // dropped out-edges whose callee is an instantiation
	SitesAllGeneric  int // sites left with zero targets by that drop (edge invisible to taint)
	SitesSomeGeneric int // sites that lost instantiation targets but kept others
}

// NewResolver wraps g; nil g yields a nil (still usable) resolver. keep is the
// emit-side emittability predicate (nil = historical Pkg/body check).
func NewResolver(g *xcg.Graph, fanoutCap int, keep func(*ssa.Function) bool) *Resolver {
	if g == nil {
		return nil
	}
	if fanoutCap <= 0 {
		fanoutCap = DefaultFanoutCap
	}
	return &Resolver{g: g, cap: fanoutCap, keep: keep, memo: map[*ssa.Function]map[ssa.CallInstruction][]*ssa.Function{}}
}

// usable reports whether cf may appear in CalleeIids.
//
// `keep` is emit's own emittability predicate, so when it is set it is the ONLY
// authority — a target is usable exactly when emit will write a summary for it.
// That now includes synthetic wrappers ($bound method values, $thunk method
// expressions, promotion wrappers) whose declaring package is in scope: dropping
// them here left method-value call sites with zero targets, hence opaque, hence
// blind to the entire callee body (doc 24 N1).
//
// The keep == nil fallback keeps the historical standalone behaviour — reject
// synthetic non-instances outright — because every resolver/flow/snapshot test
// constructs a resolver without emit's predicate.
func (r *Resolver) usable(cf *ssa.Function) bool {
	if cf == nil {
		return false
	}
	if r.keep != nil {
		return r.keep(cf)
	}
	if cf.Synthetic != "" && len(cf.TypeArgs()) == 0 {
		return false
	}
	return cf.Pkg != nil && len(cf.Blocks) > 0
}

// TargetsAt returns the resolved targets of site, a per-site dispatch
// confidence (1/n for n>1, else 1.0), and whether the site was capped to
// opaque. Unresolvable sites return (nil, 1.0, false) — identical to the
// pre-dispatch behavior.
func (r *Resolver) TargetsAt(site ssa.CallInstruction) (targets []*ssa.Function, confidence float32, cappedOpaque bool) {
	if r == nil || site == nil {
		return nil, 1.0, false
	}
	fn := site.Parent()
	sites, ok := r.memo[fn]
	if !ok {
		sites = r.index(fn)
		r.memo[fn] = sites
	}
	ts := sites[site]
	switch n := len(ts); {
	case n == 0:
		return nil, 1.0, false
	case n > r.cap:
		return nil, 1.0, true
	case n == 1:
		return ts, 1.0, false
	default:
		return ts, 1.0 / float32(n), false
	}
}

// index buckets fn's out-edges by call site. Targets that emit can never
// produce a summary for — synthetic wrappers/thunks and bodyless or
// package-less functions (same condition as emit's function filter) — are
// dropped: their iids would perturb bids without ever matching anything.
// Buckets are deduped and sorted by FQN because CalleeIids order is
// CGF-byte-visible and call-graph edge order is not run-to-run stable.
// Once a site's distinct-target count passes the cap it is opaque whatever
// else flows in, so accumulation (and the final sort) stops there — CHA
// Out-lists at wide interface sites run to thousands of edges and the
// abandoned sort was the dominant extract cost.
func (r *Resolver) index(fn *ssa.Function) map[ssa.CallInstruction][]*ssa.Function {
	sites := map[ssa.CallInstruction][]*ssa.Function{}
	node := r.g.Nodes[fn]
	if node == nil {
		return sites
	}
	seen := map[ssa.CallInstruction]map[*ssa.Function]bool{}
	genericHit := map[ssa.CallInstruction]bool{}
	for _, e := range node.Out {
		if e.Site == nil || e.Callee == nil {
			continue
		}
		cf := e.Callee.Func
		if !r.usable(cf) {
			if cf != nil && len(cf.TypeArgs()) > 0 {
				r.GenericEdges++
				genericHit[e.Site] = true
			}
			continue
		}
		s := seen[e.Site]
		if s == nil {
			s = map[*ssa.Function]bool{}
			seen[e.Site] = s
		}
		if s[cf] || len(s) > r.cap {
			continue
		}
		s[cf] = true
		sites[e.Site] = append(sites[e.Site], cf)
	}
	for _, ts := range sites {
		if len(ts) > r.cap {
			continue // capped-opaque: target order is never observed
		}
		sort.Slice(ts, func(i, j int) bool { return hash.FQN(ts[i]) < hash.FQN(ts[j]) })
	}
	for site := range genericHit {
		if len(sites[site]) == 0 {
			r.SitesAllGeneric++
		} else {
			r.SitesSomeGeneric++
		}
	}
	return sites
}

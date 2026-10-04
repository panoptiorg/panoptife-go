// Package cgstore persists the resolved dispatch graph + satisfaction facts
// per (storeKey, commit) so a re-extract at an unchanged commit skips the
// vta/cha whole-program build (R4 Ph1, incremental_callgraph_spec §4/§8).
// Dumb persistence only: no go/types, no ssa — validation against the rebuilt
// SSA lives in callgraph.NewSnapshotResolver. Any load anomaly is a cache
// MISS (full rebuild), never a user-facing error.
package cgstore

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
)

// SchemaVersion of the snapshot content. Folded into the store key: a bump
// silently invalidates every prior snapshot (they miss; rebuild rewrites).
// v2: W1d closure-binding CallSites. n_callsites now counts only the sites
// backed by a real ssa.CallInstruction (flow.Result.NCallInstrs), so a v1
// snapshot's count means something different and must never be replayed.
const SchemaVersion = 2

// keepCommits bounds snapshots retained per namespace (GC keeps the newest N).
const keepCommits = 8

// Config carries every input that affects snapshot content EXCEPT the commit —
// the commit is the directory level below the key namespace, so one namespace
// holds the last keepCommits snapshots of one (repo, scope, binary, mode…).
type Config struct {
	Root         string // store root dir, e.g. <repo>/../output/cgstore
	Repo         string // module path
	Scope        string // resolved scope string (post-expansion)
	PcfeVersion  string // hex sha256 of the running pc-fe binary
	GoToolchain  string // runtime.Version()
	CgMode       string // "vta" | "cha"
	CapN         uint32
	ExcludeMocks bool
	FieldPaths   bool // --field-paths (changes CGF bytes AND CallSite.Id alignment)
	ClosureFlow  bool // --closure-flow (changes CGF bytes; W1d)
	// ContainerWrites: --container-writes. CallSite.Id alignment is untouched —
	// it adds edges only — but it changes CGF bytes, so a snapshot written under
	// a different setting belongs in its own namespace.
	ContainerWrites bool
	// LibraryWriteback: --library-writeback. Adds srcVerts/edges only, no
	// CallSite.Id change — but it changes CGF bytes, so its own namespace.
	LibraryWriteback bool
	// HeapSlots / HeapAllFields: --heap-slots (W1F). Like FieldPaths these
	// change CallSite.Id alignment — the `copy` builtin stops emitting a
	// CallSite — so they MUST be in the key. They deliberately do NOT bump
	// SchemaVersion: the snapshot format is unchanged, and keying them means an
	// OFF run still reuses (and must reproduce) every pre-W1F snapshot, which is
	// exactly the byte-identity Gate A checks.
	HeapSlots     bool
	HeapAllFields bool
	// HeapIfaceNarrow / HeapIfaceDrop: --heap-iface-narrow / --heap-iface-drop
	// (A16). CallSite.Id alignment is untouched, but both change CGF bytes, so a
	// snapshot written under a different setting belongs in its own namespace.
	HeapIfaceNarrow bool
	HeapIfaceDrop   bool
	// ByRefOut: --byref-out (W1a). CallSite.Id alignment is untouched — it adds
	// vertices and srcVerts entries only — but it changes CGF bytes, so a
	// snapshot written under a different setting must live in its own namespace.
	ByRefOut bool
	// ErrorResults: --error-results (B1, doc 31). Adds CallSite.error_results, so
	// a snapshot written under a different setting belongs in its own namespace.
	ErrorResults bool
	// ErrorResultsStrict: --error-results-strict (doc 31 §6a). Drops the result
	// tuple's source alias, which changes edges — its own namespace.
	ErrorResultsStrict bool
	// PbPaths / MockPaths: the canonical --pb-paths / --mock-paths segment
	// lists (e.g. "pb,api"). PbPaths gates pb-getter canonicalization, so it
	// changes CallSite.Id ALIGNMENT as well as CGF bytes; MockPaths changes
	// which packages exist at all. A changed list must cold-start the graph
	// cache, hence the key.
	PbPaths   string
	MockPaths string
	GoSumHash string // hex sha256 of the repo's go.sum ("" if none)
}

// Key hashes the config sha256 length-prefixed (same discipline as hash.IID —
// no field concatenation ambiguity).
func (c Config) Key() []byte {
	h := sha256.New()
	for _, f := range []string{
		c.Repo, c.Scope, c.PcfeVersion, c.GoToolchain, c.CgMode,
		fmt.Sprintf("%d", c.CapN), fmt.Sprintf("%t", c.ExcludeMocks),
		fmt.Sprintf("%t", c.FieldPaths), fmt.Sprintf("%t", c.ClosureFlow),
		fmt.Sprintf("%t", c.HeapSlots), fmt.Sprintf("%t", c.HeapAllFields),
		fmt.Sprintf("%t", c.HeapIfaceNarrow), fmt.Sprintf("%t", c.HeapIfaceDrop),
		fmt.Sprintf("%t", c.ByRefOut), fmt.Sprintf("%t", c.ErrorResults), fmt.Sprintf("%t", c.ErrorResultsStrict),
		fmt.Sprintf("%t", c.ContainerWrites), fmt.Sprintf("%t", c.LibraryWriteback),
		c.PbPaths, c.MockPaths, c.GoSumHash, fmt.Sprintf("%d", SchemaVersion),
	} {
		writeField(h, f)
	}
	return h.Sum(nil)
}

func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}

// Snapshot is one persisted graph: Meta + one PkgShard per package.
type Snapshot struct {
	Meta   *pb.Meta
	Shards map[string]*pb.PkgShard // keyed by package import path
}

type Store struct {
	cfg   Config
	nsDir string
}

// Open computes the key namespace. It creates nothing until Write.
func Open(cfg Config) *Store {
	key := cfg.Key()
	return &Store{
		cfg:   cfg,
		nsDir: filepath.Join(cfg.Root, sanitize(cfg.Repo), fmt.Sprintf("%x", key)[:12]),
	}
}

// Probe loads the snapshot for commit. Returns (nil, reason) on any miss or
// anomaly — decode failure, schema/commit mismatch, shard hash mismatch —
// because a questionable snapshot must rebuild, never guess.
func (s *Store) Probe(commit string) (*Snapshot, string) {
	if commit == "" {
		return nil, "no commit"
	}
	dir := filepath.Join(s.nsDir, short(commit))
	blob, err := os.ReadFile(filepath.Join(dir, "meta.pb"))
	if err != nil {
		return nil, "no snapshot"
	}
	meta := &pb.Meta{}
	if err := proto.Unmarshal(blob, meta); err != nil {
		return nil, "meta corrupt"
	}
	if meta.SchemaVersion != SchemaVersion || meta.CommitSha != commit ||
		meta.CgMode != s.cfg.CgMode || meta.CapN != s.cfg.CapN {
		return nil, "meta mismatch"
	}
	snap := &Snapshot{Meta: meta, Shards: make(map[string]*pb.PkgShard, len(meta.Shards))}
	for _, sh := range meta.Shards {
		b, err := os.ReadFile(filepath.Join(dir, sanitize(sh.Pkg)+".pb"))
		if err != nil {
			return nil, "shard missing: " + sh.Pkg
		}
		if sum := sha256.Sum256(b); string(sum[:]) != string(sh.Sha256) {
			return nil, "shard hash mismatch: " + sh.Pkg
		}
		ps := &pb.PkgShard{}
		if err := proto.Unmarshal(b, ps); err != nil || ps.Pkg != sh.Pkg {
			return nil, "shard corrupt: " + sh.Pkg
		}
		snap.Shards[sh.Pkg] = ps
	}
	return snap, ""
}

// Write persists snap atomically (temp dir + rename; meta carries per-shard
// content hashes and is marshaled last) and then GCs old commit dirs.
func (s *Store) Write(snap *Snapshot) error {
	commit := snap.Meta.CommitSha
	if commit == "" {
		return fmt.Errorf("refusing to write snapshot without a commit")
	}
	tmp := filepath.Join(s.nsDir, fmt.Sprintf(".tmp-%s-%d", short(commit), os.Getpid()))
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	pkgs := make([]string, 0, len(snap.Shards))
	for p := range snap.Shards {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	snap.Meta.Shards = snap.Meta.Shards[:0]
	for _, p := range pkgs {
		blob, err := proto.MarshalOptions{Deterministic: true}.Marshal(snap.Shards[p])
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, sanitize(p)+".pb"), blob, 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(blob)
		snap.Meta.Shards = append(snap.Meta.Shards, &pb.Meta_Shard{Pkg: p, Sha256: sum[:]})
	}
	blob, err := proto.MarshalOptions{Deterministic: true}.Marshal(snap.Meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "meta.pb"), blob, 0o644); err != nil {
		return err
	}

	final := filepath.Join(s.nsDir, short(commit))
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	s.gc()
	return nil
}

// gc keeps the newest keepCommits commit dirs (by mtime — created_unix lives
// inside meta, but mtime is set by the rename and is cheaper than N decodes).
func (s *Store) gc() {
	entries, err := os.ReadDir(s.nsDir)
	if err != nil {
		return
	}
	type ent struct {
		name string
		mod  int64
	}
	var dirs []ent
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, ent{e.Name(), info.ModTime().UnixNano()})
	}
	if len(dirs) <= keepCommits {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod > dirs[j].mod })
	for _, d := range dirs[keepCommits:] {
		os.RemoveAll(filepath.Join(s.nsDir, d.name))
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(s)
}

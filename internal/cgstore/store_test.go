package cgstore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
)

func testCfg(root string) Config {
	return Config{
		Root: root, Repo: "example.com/repo", Scope: "./...",
		PcfeVersion: "deadbeef", GoToolchain: "go1.26.0", CgMode: "vta",
		CapN: 10, ExcludeMocks: true, GoSumHash: "abc",
	}
}

func testSnap(commit string) *Snapshot {
	return &Snapshot{
		Meta: &pb.Meta{
			SchemaVersion: SchemaVersion, Repo: "example.com/repo", CommitSha: commit,
			CgMode: "vta", CapN: 10, FnSetHash: []byte{1, 2, 3},
		},
		Shards: map[string]*pb.PkgShard{
			"example.com/repo/a": {Pkg: "example.com/repo/a", Callers: []*pb.CallerEdges{{
				CallerIid: []byte{9, 9}, NCallsites: 3,
				Sites: []*pb.SiteEdge{{CallsiteId: 1, CalleeIids: [][]byte{{7}}, Confidence: 0.5}},
			}}},
			"example.com/repo/b": {Pkg: "example.com/repo/b", SatPairs: []*pb.SatPair{{TypeKey: "T", IfaceKey: "I"}}},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	s := Open(testCfg(t.TempDir()))
	commit := "0123456789abcdef0123456789abcdef01234567"
	if err := s.Write(testSnap(commit)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, reason := s.Probe(commit)
	if got == nil {
		t.Fatalf("probe miss: %s", reason)
	}
	want := testSnap(commit)
	if len(got.Shards) != 2 {
		t.Fatalf("shards = %d, want 2", len(got.Shards))
	}
	for pkg, sh := range want.Shards {
		if !proto.Equal(got.Shards[pkg], sh) {
			t.Fatalf("shard %s round-trip mismatch", pkg)
		}
	}
	if got.Meta.CommitSha != commit || len(got.Meta.Shards) != 2 {
		t.Fatalf("meta round-trip mismatch: %+v", got.Meta)
	}
}

func TestProbeMisses(t *testing.T) {
	root := t.TempDir()
	s := Open(testCfg(root))
	commit := "0123456789abcdef0123456789abcdef01234567"
	if err := s.Write(testSnap(commit)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if snap, _ := s.Probe("ffffffffffffffffffffffffffffffffffffffff"); snap != nil {
		t.Fatal("probe of unknown commit must miss")
	}
	// corrupt one shard byte → miss (hash mismatch), never a partial load
	dir := filepath.Join(s.nsDir, short(commit))
	shardPath := filepath.Join(dir, sanitize("example.com/repo/a")+".pb")
	b, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xff
	os.WriteFile(shardPath, b, 0o644)
	if snap, reason := s.Probe(commit); snap != nil || reason == "" {
		t.Fatalf("corrupt shard must miss with a reason, got snap=%v reason=%q", snap != nil, reason)
	}
	// different config → different namespace → miss
	cfg2 := testCfg(root)
	cfg2.CgMode = "cha"
	if snap, _ := Open(cfg2).Probe(commit); snap != nil {
		t.Fatal("different cg mode must not share a namespace")
	}
}

func TestGCKeepsNewest(t *testing.T) {
	s := Open(testCfg(t.TempDir()))
	for i := 0; i < keepCommits+3; i++ {
		commit := fmt.Sprintf("%-040d", i) // distinct within the first 12 chars (dir name)
		if err := s.Write(testSnap(commit)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(s.nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != keepCommits {
		t.Fatalf("after GC: %d commit dirs, want %d", len(entries), keepCommits)
	}
	// the newest must have survived
	last := fmt.Sprintf("%-040d", keepCommits+2)
	if snap, reason := s.Probe(last); snap == nil {
		t.Fatalf("newest snapshot GC'd away: %s", reason)
	}
}

func TestKeyChangesWithInputs(t *testing.T) {
	base := testCfg("/x")
	k0 := fmt.Sprintf("%x", base.Key())
	for name, mut := range map[string]func(*Config){
		"repo":      func(c *Config) { c.Repo = "other" },
		"scope":     func(c *Config) { c.Scope = "./a/..." },
		"pcfe":      func(c *Config) { c.PcfeVersion = "cafe" },
		"toolchain": func(c *Config) { c.GoToolchain = "go1.27.0" },
		"mode":      func(c *Config) { c.CgMode = "cha" },
		"cap":       func(c *Config) { c.CapN = 5 },
		"mocks":     func(c *Config) { c.ExcludeMocks = false },
		"gosum":     func(c *Config) { c.GoSumHash = "zzz" },
	} {
		c := base
		mut(&c)
		if fmt.Sprintf("%x", c.Key()) == k0 {
			t.Errorf("key insensitive to %s", name)
		}
	}
}

// A changed --pb-paths / --mock-paths list must land in its own key namespace:
// pb-paths gates pb-getter canonicalization, so it moves CallSite.Id ordinals,
// and a replayed snapshot would misalign every edge. Mock-paths changes which
// packages exist at all.
func TestKeyChangesWithPathHeuristics(t *testing.T) {
	base := testCfg("root")
	baseKey := fmt.Sprintf("%x", base.Key())

	pbChanged := testCfg("root")
	pbChanged.PbPaths = "pb,api,gen"
	if fmt.Sprintf("%x", pbChanged.Key()) == baseKey {
		t.Error("--pb-paths change did not move the store key: a stale snapshot would replay with shifted CallSite ids")
	}

	mockChanged := testCfg("root")
	mockChanged.MockPaths = "mock,mocks,testdouble"
	if fmt.Sprintf("%x", mockChanged.Key()) == baseKey {
		t.Error("--mock-paths change did not move the store key")
	}
	if fmt.Sprintf("%x", mockChanged.Key()) == fmt.Sprintf("%x", pbChanged.Key()) {
		t.Error("pb-paths and mock-paths must not collide in the key")
	}

	// The store root is not hashed (it is the directory the namespace lives
	// in), so an identical config keeps an identical key.
	if fmt.Sprintf("%x", testCfg("other-root").Key()) != baseKey {
		t.Error("Key() must be stable for an unchanged config")
	}
}

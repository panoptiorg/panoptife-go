// Package pkgclass holds the repo-LAYOUT heuristics: which import paths are
// generated protobuf/gRPC output, and which are mockgen output.
//
// Both are conventions of a repository, not properties of a generator.
// `option go_package` may point anywhere — `gen/`, `proto/`, `genproto/`,
// `internal/desc` are all common in the wild — so the pb rule ships as a
// configurable segment list (`--pb-paths`) whose default reproduces the
// historical hard-coded behaviour exactly (`pb,api`).
//
// One definition, several callers: flow's remote-call / stream-port / trivial
// pb-getter recognition and emit's gRPC handler request-param sourcing must
// agree, or a request param stops being a source while its getter callsite is
// still canonicalized away. Keep every caller on PbPaths.Match.
package pkgclass

import (
	"fmt"
	"strings"
)

// DefaultPbSegments is the historical `isPbPackage` rule: a path segment "pb"
// or "api", matched inside the path or as its last element.
var DefaultPbSegments = []string{"pb", "api"}

// DefaultMockSegments is the mockgen directory convention.
var DefaultMockSegments = []string{"mock", "mocks"}

// PbPaths is the set of path segments marking a generated protobuf/gRPC
// package. The zero value means DefaultPbSegments, so a zero-valued flow.Opts
// keeps the historical behaviour.
type PbPaths []string

// Segments returns the configured segments, or the defaults when unset.
func (p PbPaths) Segments() []string {
	if len(p) == 0 {
		return DefaultPbSegments
	}
	return p
}

// Match reports whether path looks like generated-protobuf output: some
// configured segment appears inside the path ("/pb/") or ends it ("/pb").
// A bare top-level path equal to a segment does NOT match — that was the
// original rule and widening it would change emission.
func (p PbPaths) Match(path string) bool {
	for _, seg := range p.Segments() {
		if strings.Contains(path, "/"+seg+"/") || strings.HasSuffix(path, "/"+seg) {
			return true
		}
	}
	return false
}

// String is the canonical, order-preserving rendering — it rides in the
// cgstore key, so a changed list cold-starts the call-graph cache.
func (p PbPaths) String() string { return strings.Join(p.Segments(), ",") }

// MockPaths is the set of path segments marking a mockgen package. The zero
// value means DefaultMockSegments.
type MockPaths []string

// Segments returns the configured segments, or the defaults when unset.
func (m MockPaths) Segments() []string {
	if len(m) == 0 {
		return DefaultMockSegments
	}
	return m
}

// Match reports whether path has a configured segment as a WHOLE path element
// ("a/mocks/b"), which is the historical loader rule. The `mock_` segment
// prefix and the `_mock.go` / `zzz_` file rules are not configurable — they
// are mockgen's own output naming, checked separately by the loader.
func (m MockPaths) Match(path string) bool {
	segs := m.Segments()
	for _, s := range strings.Split(path, "/") {
		for _, want := range segs {
			if s == want {
				return true
			}
		}
	}
	return false
}

// String is the canonical rendering (cgstore key input).
func (m MockPaths) String() string { return strings.Join(m.Segments(), ",") }

// ParseSegments splits a comma-separated flag value into path segments.
// Empty (or all-blank) input means "use the defaults", signalled by a nil
// result. A segment containing "/" is rejected: these are single path
// elements, not path fragments.
func ParseSegments(flagName, v string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(v, ",") {
		s := strings.TrimSpace(strings.Trim(strings.TrimSpace(part), "/"))
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			return nil, fmt.Errorf("%s: %q is not a single path segment", flagName, part)
		}
		out = append(out, s)
	}
	return out, nil
}

// ParsePb parses the --pb-paths value.
func ParsePb(v string) (PbPaths, error) {
	segs, err := ParseSegments("--pb-paths", v)
	return PbPaths(segs), err
}

// ParseMock parses the --mock-paths value.
func ParseMock(v string) (MockPaths, error) {
	segs, err := ParseSegments("--mock-paths", v)
	return MockPaths(segs), err
}

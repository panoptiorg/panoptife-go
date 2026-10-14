// Package relpath turns the absolute file names go/token reports into paths
// relative to the extracted directory, so the same checkout yields the same
// CGF wherever it sits on disk.
package relpath

import (
	"path/filepath"
	"strings"
	"sync"
)

// Rel maps file names to paths relative to a root directory.
type Rel struct {
	abs, real string // root as given (made absolute) and with symlinks resolved
	cache     sync.Map
}

// New returns a Rel for root. A root that cannot be made absolute yields a
// Rel that leaves every name unchanged.
func New(root string) *Rel {
	abs, err := filepath.Abs(root)
	if err != nil {
		return &Rel{}
	}
	real := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		real = r
	}
	return &Rel{abs: abs, real: real}
}

// File returns name relative to the root with "/" separators. A name outside
// the root, or a nil receiver, returns name unchanged: never a "../" path.
// Safe for concurrent use.
func (r *Rel) File(name string) string {
	if r == nil || r.abs == "" || name == "" || !filepath.IsAbs(name) {
		return name
	}
	if v, ok := r.cache.Load(name); ok {
		return v.(string)
	}
	out := name
	// go/packages may report /private/tmp/x for a root given as /tmp/x (macOS),
	// so try the symlink-resolved pair first, then the paths as written.
	real := name
	if p, err := filepath.EvalSymlinks(name); err == nil {
		real = p
	}
	for _, c := range [][2]string{{r.real, real}, {r.abs, name}} {
		rel, err := filepath.Rel(c[0], c[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = filepath.ToSlash(rel)
			break
		}
	}
	r.cache.Store(name, out)
	return out
}

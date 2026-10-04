// MR-mode diff manifest (MVP-5). mr mode still extracts the full working tree
// (the core's summary cache makes reuse content-addressed, not manifest-driven);
// the manifest only tells the core WHICH functions the MR touched, for the
// changed_functions list and chain classification in the impact report.
package emit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

const manifestName = "manifest.json"

type ChangedFn struct {
	Iid  string `json:"iid"`
	Bid  string `json:"bid"`
	Fqn  string `json:"fqn"`
	File string `json:"file"` // repo-relative
}

type Manifest struct {
	Mode         string      `json:"mode"`
	Base         string      `json:"base"`
	Head         string      `json:"head"`
	ChangedFiles []string    `json:"changed_files"`
	ChangedFns   []ChangedFn `json:"changed_fns"`
}

// changedFiles lists paths touched between base and head, relative to repoDir
// (--relative handles repoDir being a subdir of the git toplevel, e.g. a monorepo checkout).
func changedFiles(repoDir, base, head string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoDir, "diff", "--name-only", "--relative", base, head).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git diff %s..%s in %s: %s", base, head, repoDir, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git diff %s..%s in %s: %w", base, head, repoDir, err)
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func buildManifest(o Options, byPkg map[string]*pb.CgfPackage) (*Manifest, error) {
	files, err := changedFiles(o.RepoDir, o.Base, o.Head)
	if err != nil {
		return nil, err
	}
	warnIfNotHead(o.RepoDir, o.Head)

	changed := make(map[string]bool, len(files))
	for _, f := range files {
		changed[f] = true
	}

	// fset filenames are absolute (and macOS /tmp is a /private/tmp symlink);
	// normalize both sides before Rel.
	root, err := filepath.Abs(o.RepoDir)
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	relCache := map[string]string{}
	relOf := func(abs string) string {
		if r, ok := relCache[abs]; ok {
			return r
		}
		p := abs
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			p = r
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = ""
		}
		relCache[abs] = rel
		return rel
	}

	m := &Manifest{Mode: o.Mode, Base: o.Base, Head: o.Head, ChangedFiles: files}
	for _, cp := range byPkg {
		for _, f := range cp.Functions {
			if f.Span == nil || f.Span.File == "" {
				continue
			}
			rel := relOf(f.Span.File)
			if rel == "" || !changed[rel] {
				continue
			}
			m.ChangedFns = append(m.ChangedFns, ChangedFn{
				Iid:  fmt.Sprintf("%x", f.Id.Iid),
				Bid:  fmt.Sprintf("%x", f.Id.Bid),
				Fqn:  f.Fqn,
				File: rel,
			})
		}
	}
	return m, nil
}

func writeManifest(outDir string, m *Manifest) error {
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, manifestName), blob, 0o644)
}

// mr mode analyzes the working tree and trusts it is checked out at --head;
// --base is only an input to the diff. CI checkouts satisfy this; warn locally.
func warnIfNotHead(repoDir, head string) {
	cur, err1 := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	want, err2 := exec.Command("git", "-C", repoDir, "rev-parse", head+"^{commit}").Output()
	if err1 != nil || err2 != nil {
		return
	}
	c, w := strings.TrimSpace(string(cur)), strings.TrimSpace(string(want))
	if c != w {
		fmt.Fprintf(os.Stderr, "warning: working tree HEAD %s != --head %s; extraction reflects the working tree\n", c[:8], w[:8])
	}
}

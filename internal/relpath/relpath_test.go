package relpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFile(t *testing.T) {
	tmp := t.TempDir()
	real, _ := filepath.EvalSymlinks(tmp)
	if err := os.MkdirAll(filepath.Join(real, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a", "b", "x.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(real, "link")
	if err := os.Symlink(filepath.Join(real, "a"), link); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(real, "a")
	for _, c := range []struct{ root, name, want string }{
		{root, filepath.Join(root, "b", "x.go"), "b/x.go"},
		{link, filepath.Join(root, "b", "x.go"), "b/x.go"},                       // root given via symlink
		{root, filepath.Join(link, "b", "x.go"), "b/x.go"},                       // name via symlink
		{root, filepath.Join(real, "other.go"), filepath.Join(real, "other.go")}, // outside: absolute
		{root, "rel.go", "rel.go"},
		{root, "", ""},
	} {
		if got := New(c.root).File(c.name); got != c.want {
			t.Errorf("New(%q).File(%q) = %q, want %q", c.root, c.name, got, c.want)
		}
	}
	var nilRel *Rel
	if got := nilRel.File("/x/y.go"); got != "/x/y.go" {
		t.Errorf("nil Rel changed name: %q", got)
	}
}

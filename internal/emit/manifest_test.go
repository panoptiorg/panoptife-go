package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeAndCommit(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", msg, "--no-gpg-sign")
	sha := gitOut(t, dir, "rev-parse", "HEAD")
	return sha[:len(sha)-1]
}

func TestChangedFilesAndManifest(t *testing.T) {
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q")

	base := writeAndCommit(t, dir, "a.go", "package a\n", "base")
	_ = writeAndCommit(t, dir, "b.go", "package a\nfunc B() {}\n", "add b")
	head := writeAndCommit(t, dir, "a.go", "package a\nfunc A() {}\n", "edit a")

	files, err := changedFiles(dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a.go": true, "b.go": true}
	if len(files) != 2 || !want[files[0]] || !want[files[1]] {
		t.Fatalf("changedFiles = %v, want a.go + b.go", files)
	}

	// manifest selects only fns whose file is in the diff
	abs, _ := filepath.EvalSymlinks(dir)
	byPkg := map[string]*pb.CgfPackage{
		"a": {Functions: []*pb.Function{
			{
				Id:   &pb.Ident{Iid: []byte{0xAA}, Bid: []byte{0x01}},
				Fqn:  "a.A",
				Span: &pb.Span{File: filepath.Join(abs, "a.go"), Line: 2},
			},
			{
				Id:   &pb.Ident{Iid: []byte{0xCC}, Bid: []byte{0x02}},
				Fqn:  "a.Untouched",
				Span: &pb.Span{File: filepath.Join(abs, "c.go"), Line: 1},
			},
		}},
	}
	m, err := buildManifest(Options{RepoDir: dir, Mode: "mr", Base: base, Head: head}, byPkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ChangedFns) != 1 {
		t.Fatalf("ChangedFns = %+v, want exactly a.A", m.ChangedFns)
	}
	fn := m.ChangedFns[0]
	if fn.Fqn != "a.A" || fn.File != "a.go" || fn.Iid != "aa" || fn.Bid != "01" {
		t.Fatalf("unexpected ChangedFn: %+v", fn)
	}
}

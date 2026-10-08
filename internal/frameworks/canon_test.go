package frameworks

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type canonVectors struct {
	Vectors []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"vectors"`
}

func checkVectors(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cv canonVectors
	if err := json.Unmarshal(raw, &cv); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(cv.Vectors) == 0 {
		t.Fatalf("%s: no vectors", path)
	}
	for _, v := range cv.Vectors {
		if got := CanonPath(v.In); got != v.Out {
			t.Errorf("CanonPath(%q) = %q, want %q", v.In, got, v.Out)
		}
	}
}

// The embedded copy: CI checks out no engine, so the vectors must be tested
// without the sibling.
func TestCanonPathVectors(t *testing.T) {
	checkVectors(t, "testdata/http-canon-vectors.json")
}

// The canonical copy in a sibling engine checkout — the cross-language pin
// (§1.1). Skipped when absent, like scripts/check-proto.sh; when present the
// embedded copy must not have drifted from it either.
func TestCanonPathVectorsSibling(t *testing.T) {
	canon := "../../../panopticode/testdata/http-canon-vectors.json"
	if _, err := os.Stat(canon); err != nil {
		t.Skipf("no sibling engine checkout at %s", canon)
	}
	checkVectors(t, canon)
	a, _ := os.ReadFile(canon)
	b, _ := os.ReadFile("testdata/http-canon-vectors.json")
	if !bytes.Equal(a, b) {
		t.Errorf("testdata/http-canon-vectors.json drifted from %s: re-copy it", canon)
	}
}

// Idempotence: the core canonicalises again at link time, so a canonical path
// must be a fixed point.
func TestCanonPathIdempotent(t *testing.T) {
	for _, in := range []string{"/api/users/{id}", "/files/{path...}", "{}/x/{}", "/a/{*}/b", "/"} {
		once := CanonPath(in)
		if twice := CanonPath(once); twice != once {
			t.Errorf("CanonPath not idempotent on %q: %q then %q", in, once, twice)
		}
	}
}

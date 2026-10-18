package emit

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

const (
	xmodLib = "example.com/xmod/lib"
	xmodApp = "example.com/xmod/app"
)

// A call into another module must carry the iid that module's own extraction
// gives the callee, or the core can never link the call to the callee's
// summary.
func TestCrossModuleCalleeIID(t *testing.T) {
	libPkgs, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/lib"})
	appPkgs, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app"})

	libIID := map[string][]byte{}
	for _, cp := range libPkgs {
		for _, f := range cp.Functions {
			libIID[f.Fqn] = f.Id.Iid
		}
	}
	if libIID[xmodLib+".Fetch"] == nil || libIID[xmodLib+".Map"] == nil {
		t.Fatalf("lib must emit Fetch and Map, got %d functions", len(libIID))
	}

	user := fnByFqn(appPkgs, xmodApp+".user")
	if user == nil {
		t.Fatal("app must emit user")
	}
	linked, mapSite, stdSite := 0, 0, 0
	for _, cs := range user.Flow.Callsites {
		if len(cs.CalleeIids) != 1 {
			t.Errorf("site %s: %d callee iids, want 1", cs.CalleeFqn, len(cs.CalleeIids))
			continue
		}
		got := cs.CalleeIids[0]
		switch {
		case strings.HasPrefix(cs.CalleeFqn, xmodLib+".Map["):
			// An instance of a generic function: lib's CGF has only the
			// generic function itself, so there is no iid to compare with.
			mapSite++
			if len(got) != 32 {
				t.Errorf("%s: callee iid has %d bytes", cs.CalleeFqn, len(got))
			}
		case strings.HasPrefix(cs.CalleeFqn, xmodLib+"."):
			want, ok := libIID[cs.CalleeFqn]
			if !ok {
				continue
			}
			linked++
			if !bytes.Equal(got, want) {
				t.Errorf("%s: callee iid %x, lib's own iid %x", cs.CalleeFqn, got[:6], want[:6])
			}
		case cs.CalleeFqn == "strings.ToUpper":
			// No module declares it: the extracted module stays the owner.
			stdSite++
			want := hash.IIDFromParts(xmodApp, "strings", "strings.ToUpper", "func(s string) string")
			if !bytes.Equal(got, want) {
				t.Errorf("strings.ToUpper: callee iid %x, want %x", got[:6], want[:6])
			}
		}
	}
	if linked == 0 || mapSite != 1 || stdSite != 1 {
		t.Errorf("sites: lib functions linked=%d lib.Map=%d strings.ToUpper=%d, want >0/1/1", linked, mapSite, stdSite)
	}
}

// Extracting a module on its own, or with the module as a dependency of
// another module, gives its own functions the same iids.
func TestOwnFunctionIIDsUnchanged(t *testing.T) {
	var cgfs [2]map[string]*pb.CgfPackage
	for i, dir := range []string{"../../fixtures/xmod/lib", "../../fixtures/xmod/app"} {
		cgfs[i], _ = runCGF(t, Options{RepoDir: dir})
	}
	user := fnByFqn(cgfs[1], xmodApp+".user")
	want := hash.IIDFromParts(xmodApp, xmodApp, xmodApp+".user", "func(w net/http.ResponseWriter, r *net/http.Request)")
	if user == nil || !bytes.Equal(user.Id.Iid, want) {
		t.Errorf("app.user iid %x, want %x", user.GetId().GetIid(), want)
	}
	fetch := fnByFqn(cgfs[0], xmodLib+".Fetch")
	want = hash.IIDFromParts(xmodLib, xmodLib, xmodLib+".Fetch", "func(id string) string")
	if fetch == nil || !bytes.Equal(fetch.Id.Iid, want) {
		t.Errorf("lib.Fetch iid %x, want %x", fetch.GetId().GetIid(), want)
	}
}

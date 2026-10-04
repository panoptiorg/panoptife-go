package hash

import (
	"bytes"
	"testing"
)

func TestIIDFromPartsDeterministic(t *testing.T) {
	a := IIDFromParts("repo", "pkg", "Sym", "func()")
	b := IIDFromParts("repo", "pkg", "Sym", "func()")
	if !bytes.Equal(a, b) {
		t.Fatal("iid not deterministic")
	}
	if bytes.Equal(a, IIDFromParts("repo", "pkg", "Sym2", "func()")) {
		t.Fatal("different symbol must change iid")
	}
	if bytes.Equal(a, IIDFromParts("repo", "pkg", "Sym", "func(int)")) {
		t.Fatal("different signature must change iid")
	}
}

// bid embeds callee IIDs, not bids: a callee body refactor (which would change
// its bid but not its iid) must NOT change this function's bid.
func TestBIDEmbedsCalleeIIDsNotBIDs(t *testing.T) {
	flow := []byte("structural-flow-bytes")
	calleeIID := []byte("callee-iid-stable")

	base := BID(flow, [][]byte{calleeIID})
	// same iid, regardless of any callee body change -> same bid
	same := BID(flow, [][]byte{calleeIID})
	if !bytes.Equal(base, same) {
		t.Fatal("bid must be stable when callee iid unchanged")
	}
	// own body edit changes bid
	if bytes.Equal(base, BID([]byte("edited-flow"), [][]byte{calleeIID})) {
		t.Fatal("own body edit must change bid")
	}
	// callee signature change (iid flips) changes bid
	if bytes.Equal(base, BID(flow, [][]byte{[]byte("callee-iid-changed")})) {
		t.Fatal("callee signature change must change bid")
	}
}

func TestBIDCalleeOrderIndependent(t *testing.T) {
	flow := []byte("f")
	a := BID(flow, [][]byte{[]byte("x"), []byte("y")})
	b := BID(flow, [][]byte{[]byte("y"), []byte("x")})
	if !bytes.Equal(a, b) {
		t.Fatal("bid must be independent of callee order")
	}
}

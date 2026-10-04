// Package hash computes the two-level content addresses (doc 03 §2).
//   iid = sha256(repo, package_path, exported_symbol_path, normalized_signature)
//   bid = sha256(canonical LocalFlow) embedding callees' iids (NOT bids)
package hash

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// FQN returns a stable fully-qualified name for a function, e.g.
// "(*gitlab.example.com/acme/ledger-svc/internal/pkg/store.Storage).Selectx".
func FQN(fn *ssa.Function) string {
	return fn.String()
}

// PackagePath returns the import path owning fn ("" for synthetic). Generic
// instantiations (Pkg==nil) and closures nested in them resolve to the
// declaring object's package, walking parents — plain reads only, never
// Origin() (it Build()s packages and has CGF-visible side effects).
func PackagePath(fn *ssa.Function) string {
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	for f := fn; f != nil; f = f.Parent() {
		if obj := f.Object(); obj != nil && obj.Pkg() != nil {
			return obj.Pkg().Path()
		}
	}
	return ""
}

// ContractIID is the cross-repo join key for a gRPC method, derived from the
// proto-style full name "pkg.Service/Method". Both the defining repo (server)
// and the calling repo (client) compute the SAME value from Go type-name
// conventions, so invokes_remote links to the handler summary with no fuzzy
// matching (doc 02 §4, doc 04 §4).
func ContractIID(fullName string) []byte {
	return IIDFromParts("", "", fullName, "grpc")
}

// IID is signature identity: stable across body edits.
func IID(repo string, fn *ssa.Function) []byte {
	h := sha256.New()
	writeField(h, []byte(repo))
	writeField(h, []byte(PackagePath(fn)))
	writeField(h, []byte(FQN(fn)))
	writeField(h, []byte(fn.Signature.String()))
	return h.Sum(nil)
}

// IIDFromParts builds an iid for a symbol we only know by name (e.g. an
// unresolved / external callee or a contract endpoint).
func IIDFromParts(repo, pkgPath, symbol, signature string) []byte {
	h := sha256.New()
	writeField(h, []byte(repo))
	writeField(h, []byte(pkgPath))
	writeField(h, []byte(symbol))
	writeField(h, []byte(signature))
	return h.Sum(nil)
}

// BID is body identity: bytes of the canonical LocalFlow plus the SORTED set of
// callee iids (contracts), not their bids (Merkle-DAG, git-tree style).
func BID(canonicalFlow []byte, calleeIIDs [][]byte) []byte {
	sorted := make([][]byte, len(calleeIIDs))
	copy(sorted, calleeIIDs)
	sort.Slice(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
	h := sha256.New()
	writeField(h, canonicalFlow)
	for _, c := range sorted {
		writeField(h, c)
	}
	return h.Sum(nil)
}

func writeField(h interface{ Write([]byte) (int, error) }, b []byte) {
	var l [8]byte
	binary.LittleEndian.PutUint64(l[:], uint64(len(b)))
	_, _ = h.Write(l[:])
	_, _ = h.Write(b)
}

func less(a, b []byte) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

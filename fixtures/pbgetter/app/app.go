// Package app reads every request field through its getter into one sink —
// the shape of a gRPC handler under a --scope that leaves the /pb/ packages
// out. Field A carries tainted request data; field B is clean (the server
// writes it from the session). With the getters bodiless, an opaque getter
// call's default leaf would taint the B read from the whole request and the
// sink would fire from A's taint; the go/types projection keeps the reads on
// their own field paths, so the core needs a tainted [B] fact to fire for B.
package app

import (
	"os/exec"

	"example.com/pbgetter/pb"
)

func Handler(req *pb.Req) {
	sink(req.GetA())                    // A: the field that carries tainted data
	sink(req.GetB())                    // B: clean — the defect this fixture pins
	sink(req.GetChoice().(*pb.Req_C).C) // oneof read through its getter
}

func Handler2(rec *pb.HandRec, fr *pb.FieldRec) {
	sink(fr.GetH().A) // not a projection: result type ≠ field type
	sink(rec.GetA())  // not generated protobuf output — the barrier stays
}

func sink(s string) {
	cmd := exec.Command("audit-log", s)
	_ = cmd
}

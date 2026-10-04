// Package thirdparty stands in for a dependency: e2e-libwrites.sh extracts
// WITHOUT it in scope, so Fill is a body-less library call to the core, and the
// only thing that can say it writes into dst is a catalog propagator.
package thirdparty

type Out struct{ V string }

func Fill(dst *Out, s string) { dst.V = s }

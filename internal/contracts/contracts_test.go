package contracts

import (
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/loader"
)

func TestUnimplRe(t *testing.T) {
	cases := []struct {
		name string
		svc  string // "" = no match
	}{
		{"UnimplementedAccountServer", "Account"},
		{"UnsafeCbreportsServer", "Cbreports"},
		{"MockUnsafeCbreportsServer", ""},
		{"MockUnimplementedAccountServer", ""},
		{"UnimplementedServer", ""},
		{"UnsafeServer", ""},
		{"AccountServer", ""},
	}
	for _, c := range cases {
		m := unimplRe.FindStringSubmatch(c.name)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != c.svc {
			t.Errorf("unimplRe(%s): got %q, want %q", c.name, got, c.svc)
		}
	}
}

func TestExtractStreamsvc(t *testing.T) {
	ld, err := loader.Load("testdata/streamsvc", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inScope := map[*ssa.Package]bool{}
	for _, sp := range ld.InitPkgs {
		inScope[sp] = true
	}
	gms := Extract(ld.Prog, inScope, "test")

	type st struct{ client, server bool }
	want := map[string]st{
		"pb.Echo/Get":      {false, false},
		"pb.Feed/Download": {false, true},
		"pb.Feed/Upload":   {true, false},
		"pb.Feed/Chat":     {true, true},
	}
	got := map[string]st{}
	for _, gm := range gms {
		got[gm.FullName] = st{gm.ClientStreaming, gm.ServerStreaming}
		if len(gm.HandlerIID) == 0 {
			t.Errorf("%s: empty HandlerIID", gm.FullName)
		}
		if gm.HandlerFn == nil {
			t.Errorf("%s: nil HandlerFn", gm.FullName)
		}
	}
	for full, w := range want {
		g, ok := got[full]
		if !ok {
			t.Errorf("missing method %s", full)
			continue
		}
		if g != w {
			t.Errorf("%s: streaming = %+v, want %+v", full, g, w)
		}
	}
	for full := range got {
		if _, ok := want[full]; !ok {
			t.Errorf("unexpected method extracted: %s", full)
		}
	}
}

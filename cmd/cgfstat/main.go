// cgfstat prints call-site dispatch statistics for a CGF directory: fan-out
// histogram (CalleeIids length), dispatch-confidence histogram, opaque counts.
// Debug/measurement tool for dispatch wiring (R3) and the VTA-vs-CHA gap (R4
// Phase 0).
//
// usage: cgfstat <cgf-dir> [<cgf-dir>...]
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"google.golang.org/protobuf/proto"

	"github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cgfstat <cgf-dir> [<cgf-dir>...]")
		os.Exit(2)
	}
	for _, dir := range os.Args[1:] {
		if err := run(dir); err != nil {
			fmt.Fprintf(os.Stderr, "cgfstat: %s: %v\n", dir, err)
			os.Exit(1)
		}
	}
}

func run(dir string) error {
	shards, err := filepath.Glob(filepath.Join(dir, "*.pb"))
	if err != nil {
		return err
	}
	var (
		fns, sites, opaque int
		fanout             = map[int]int{} // len(CalleeIids) → sites
		conf               = map[string]int{}
	)
	for _, sh := range shards {
		blob, err := os.ReadFile(sh)
		if err != nil {
			return err
		}
		var cp cgfpb.CgfPackage
		if err := proto.Unmarshal(blob, &cp); err != nil {
			return fmt.Errorf("%s: %w", sh, err)
		}
		for _, f := range cp.Functions {
			fns++
			if f.Flow == nil {
				continue
			}
			for _, cs := range f.Flow.Callsites {
				sites++
				if cs.Opaque {
					opaque++
				}
				fanout[len(cs.CalleeIids)]++
				conf[fmt.Sprintf("%.4f", cs.DispatchConfidence)]++
			}
		}
	}
	fmt.Printf("%s: shards=%d fns=%d sites=%d opaque=%d\n", dir, len(shards), fns, sites, opaque)
	fmt.Printf("  fan-out (len CalleeIids → sites):\n")
	ks := make([]int, 0, len(fanout))
	for k := range fanout {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	for _, k := range ks {
		fmt.Printf("    %3d → %d\n", k, fanout[k])
	}
	fmt.Printf("  dispatch_confidence → sites:\n")
	cs := make([]string, 0, len(conf))
	for k := range conf {
		cs = append(cs, k)
	}
	sort.Strings(cs)
	for _, k := range cs {
		fmt.Printf("    %s → %d\n", k, conf[k])
	}
	return nil
}

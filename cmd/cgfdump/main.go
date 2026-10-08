// cgfdump prints decoded CGF shard content: functions, call sites, and (with
// -flow <fn substring>) local flow vertices/edges. -sites emits one TSV row
// per call site (caller fqn, cs id, opaque, confidence, full callee iids)
// over a shard file or a whole CGF dir — the per-site diff input for the
// R4-Ph0 VTA-vs-CHA gap measurement. -contracts lists the contract facts
// (endpoints, gRPC methods, GraphQL fields, HTTP routes) instead. Debug tool.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

func main() {
	flowFn := flag.String("flow", "", "also print LocalFlow for functions whose fqn contains this")
	sites := flag.Bool("sites", false, "TSV per call site: fqn, cs, opaque, conf, sorted callee iids (arg may be a CGF dir)")
	contracts := flag.Bool("contracts", false, "list endpoints, gRPC methods, GraphQL fields and HTTP routes instead (arg may be a CGF dir)")
	flag.Parse()
	arg := flag.Arg(0)
	if *contracts {
		paths := []string{arg}
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			paths, _ = filepath.Glob(filepath.Join(arg, "*.pb"))
		}
		for _, p := range paths {
			dumpContracts(p)
		}
		return
	}
	if *sites {
		paths := []string{arg}
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			paths, _ = filepath.Glob(filepath.Join(arg, "*.pb"))
		}
		for _, p := range paths {
			dumpSites(p)
		}
		return
	}
	blob, err := os.ReadFile(arg)
	if err != nil {
		panic(err)
	}
	var cp cgfpb.CgfPackage
	if err := proto.Unmarshal(blob, &cp); err != nil {
		panic(err)
	}
	for _, f := range cp.Functions {
		fmt.Printf("FN %s iid=%x src_params=%v binds=%d\n", f.Fqn, f.Id.Iid[:6], f.SourceParams, len(f.BindsTo))
		if f.Flow == nil {
			continue
		}
		for _, cs := range f.Flow.Callsites {
			iids := ""
			for _, iid := range cs.CalleeIids {
				iids += fmt.Sprintf(" %x", iid[:6])
			}
			http := ""
			if hc := cs.HttpCall; hc != nil { // coverage wave 1 §2.3 client site
				http = fmt.Sprintf(" http=%q %s", hc.Method, hc.Path)
			}
			fmt.Printf("  cs=%d kind=%v callee=%s n=%d opaque=%v conf=%.4f argc=%d iids=[%s]%s\n",
				cs.Id, cs.Kind, cs.CalleeFqn, len(cs.CalleeIids), cs.Opaque, cs.DispatchConfidence, cs.Argc, iids, http)
		}
		if *flowFn != "" && strings.Contains(f.Fqn, *flowFn) {
			for _, v := range f.Flow.Vertices {
				sym := ""
				if len(v.Sym) > 0 { // W1F heap cell
					sym = fmt.Sprintf(" sym=%x[%s]", v.Sym[:6], v.SymName)
				}
				path := ""
				if len(v.FieldPath) > 0 {
					path = fmt.Sprintf(" path=%v%v", v.FieldPath, v.FieldNames)
				}
				fmt.Printf("  v=%d kind=%v idx=%d cs=%d type=%s%s%s\n",
					v.Id, v.Kind, v.Index, v.CallsiteId, v.Type, path, sym)
			}
			for _, e := range f.Flow.Edges {
				fmt.Printf("  e %d->%d alias=%v\n", e.From, e.To, e.ViaAlias)
			}
		}
	}
}

// dumpSites prints one TSV row per call site. Callee iids are emitted in full
// (they are FQN-sorted in the CGF already) so set comparison across dispatch
// modes is collision-free.
func dumpSites(path string) {
	blob, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var cp cgfpb.CgfPackage
	if err := proto.Unmarshal(blob, &cp); err != nil {
		panic(err)
	}
	for _, f := range cp.Functions {
		if f.Flow == nil {
			continue
		}
		for _, cs := range f.Flow.Callsites {
			iids := make([]string, 0, len(cs.CalleeIids))
			for _, iid := range cs.CalleeIids {
				iids = append(iids, fmt.Sprintf("%x", iid))
			}
			fmt.Printf("%s\t%d\t%v\t%.4f\t%s\n",
				f.Fqn, cs.Id, cs.Opaque, cs.DispatchConfidence, strings.Join(iids, ","))
		}
	}
}

// dumpContracts prints one line per contract fact of a shard.
func dumpContracts(path string) {
	blob, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var cp cgfpb.CgfPackage
	if err := proto.Unmarshal(blob, &cp); err != nil {
		panic(err)
	}
	fnName := map[string]string{}
	for _, f := range cp.Functions {
		fnName[string(f.Id.Iid)] = f.Fqn
	}
	for _, e := range cp.Endpoints {
		fmt.Printf("ENDPOINT %v %q iid=%x untrusted=%v\n", e.Kind, e.Name, e.Iid[:6], e.UntrustedInput)
	}
	for _, g := range cp.GrpcMethods {
		fmt.Printf("GRPC %s handler=%s\n", g.FullName, fnName[string(g.HandlerIid)])
	}
	for _, g := range cp.GraphqlFields {
		fmt.Printf("GRAPHQL %s resolver=%s\n", g.TypeField, fnName[string(g.ResolverIid)])
	}
	for _, r := range cp.HttpRoutes {
		fmt.Printf("HTTP %s %s display=%s framework=%s request_params=%v handler=%s\n",
			r.Method, r.Path, r.Display, r.Framework, r.RequestParams, fnName[string(r.HandlerIid)])
	}
}

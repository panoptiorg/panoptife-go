// pc-fe — the Panopticode Go frontend. Loads Go packages, builds SSA + a
// dispatch call graph (VTA/CHA), extracts the CGF (code graph facts + LocalFlow
// value-flow sidecar + contract facts), and writes one protobuf file per
// package.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/panoptiorg/panoptife-go/internal/emit"
	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
	"github.com/spf13/cobra"
)

func main() {
	var (
		scope        string
		outDir       string
		mode         string
		base         string
		head         string
		verbose      bool
		includeMocks bool
		dispatch     string
		cgStoreDir   string
		fieldPaths   bool
		closureFlow  bool
		containers   bool
		libWrite     bool
		heapSlots    bool
		byRefOut     bool
		heapScope    string
		heapNarrow   bool
		heapDrop     bool
		errResults   bool
		errStrict    bool
		allowMissing bool
		pbPaths      string
		mockPaths    string
		requireContr bool
	)

	root := &cobra.Command{
		Use:   "pc-fe",
		Short: "Panopticode Go frontend: source → CGF protobuf",
		// cobra's default Execute() already prints "Error: <err>" to stderr on a
		// non-nil RunE return; without this the error was printed a SECOND time
		// below, so a --require-contracts CI failure showed the same line twice.
		SilenceErrors: true,
	}

	buildCmd := &cobra.Command{
		Use:   "build [repo-dir]",
		Short: "Extract CGF for a Go repo (module dir)",
		Args:  cobra.ExactArgs(1),
		// A failed extraction is not a usage error: dumping the flag list
		// would bury the diagnostic that explains it (see noContractsHint).
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if heapSlots && heapScope != "chan" && heapScope != "all" {
				return fmt.Errorf("--heap-slots-scope must be chan|all, got %q", heapScope)
			}
			pbp, err := pkgclass.ParsePb(pbPaths)
			if err != nil {
				return err
			}
			mkp, err := pkgclass.ParseMock(mockPaths)
			if err != nil {
				return err
			}
			opts := emit.Options{
				RepoDir:            args[0],
				Scope:              scope,
				OutDir:             outDir,
				Mode:               mode,
				Base:               base,
				Head:               head,
				Verbose:            verbose,
				IncludeMocks:       includeMocks,
				AllowMissingScope:  allowMissing,
				Dispatch:           dispatch,
				CgStoreDir:         cgStoreDir,
				NoFieldPaths:       !fieldPaths,
				NoClosureFlow:      !closureFlow,
				NoContainerWrites:  !containers,
				NoLibraryWriteback: !libWrite,
				HeapSlots:          heapSlots,
				HeapAllFields:      heapScope == "all",
				HeapIfaceNarrow:    heapNarrow,
				HeapIfaceDrop:      heapDrop,
				ByRefOut:           byRefOut,
				ErrorResults:       errResults,
				ErrorResultsStrict: errStrict,
				PbPaths:            pbp,
				MockPaths:          mkp,
				RequireContracts:   requireContr,
			}
			return emit.Run(opts)
		},
	}
	buildCmd.Flags().StringVar(&scope, "scope", "./...", "comma-separated package patterns to load")
	buildCmd.Flags().StringVar(&outDir, "out", "", "output dir for CGF .pb files (default <repo>/../output/cgf/<repo>)")
	buildCmd.Flags().StringVar(&mode, "mode", "main", "extraction mode: main | mr")
	buildCmd.Flags().StringVar(&base, "base", "", "mr mode: base SHA")
	buildCmd.Flags().StringVar(&head, "head", "", "mr mode: head SHA")
	buildCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "verbose logging")
	buildCmd.Flags().BoolVar(&allowMissing, "allow-missing-scope", false, "extract even when requested scope packages never loaded (debt A3). ESCAPE HATCH: the corpus is then partial and every number off it is a floor, not a measurement")
	buildCmd.Flags().BoolVar(&includeMocks, "include-mocks", false, "analyze mockgen packages too (default: exclude them entirely)")
	buildCmd.Flags().StringVar(&dispatch, "dispatch", "vta", "virtual-dispatch resolution: vta (precise) | cha (near-linear, over-approx) | off (skip call-graph build; dynamic sites opaque)")
	buildCmd.Flags().StringVar(&cgStoreDir, "cgstore", "", "call-graph snapshot store dir: re-extracting an unchanged commit replays dispatch instead of rebuilding vta/cha (empty or 'off' disables; bypassed on dirty trees)")
	buildCmd.Flags().BoolVar(&fieldPaths, "field-paths", true, "k=2 field-path facts in LocalFlow (doc 20 §2); false restores field-insensitive emission")
	buildCmd.Flags().BoolVar(&closureFlow, "closure-flow", true, "closure free-variable slots + MakeClosure binding sites (W1d); false restores pre-W1d emission")
	buildCmd.Flags().BoolVar(&containers, "container-writes", true, "map writes (`m[k] = v`) and channel sends (`ch <- v`, select send cases) put the value into the map/channel, so later reads in the function see it; false restores the previous emission, which drops both")
	buildCmd.Flags().BoolVar(&libWrite, "library-writeback", true, "at calls into library code (no emitted body), a pointer/slice/map/chan argument's port also flows back into the caller's value, so a catalog [[propagators]] rule (e.g. sb.WriteString fills sb) reaches later uses; inert unless a rule fires. false restores the previous emission")
	buildCmd.Flags().BoolVar(&heapSlots, "heap-slots", false, "heap-cell in/out slots + ssa.Send + precise ssa.Select + the copy builtin (W1F, doc 24 N5); DEFAULT OFF — a program-wide object-insensitive join, opt-in until measured")
	buildCmd.Flags().StringVar(&heapScope, "heap-slots-scope", "chan", "which struct fields become heap cells: chan (channel-typed only) | all (every field). Ignored without --heap-slots")
	buildCmd.Flags().BoolVar(&heapNarrow, "heap-iface-narrow", false, "A16 (doc 30 §6.1): tag interface-typed heap cells with the concrete type on both sides (reader's type assertion, writer's MakeInterface) so the core drops pairings the assertion cannot accept. Ignored without --heap-slots; DEFAULT OFF — emission change, off must reproduce the previous CGF byte-for-byte")
	buildCmd.Flags().BoolVar(&heapDrop, "heap-iface-drop", false, "MEASUREMENT INSTRUMENT ONLY, never ship on: never open a heap cell for an interface-typed field. doc 30 §7b sized this blunt filter and rejected it — it disables 20-31% of the sink-carrying cell population. Exists to A/B against --heap-iface-narrow")
	buildCmd.Flags().BoolVar(&errResults, "error-results", false, "B1 (doc 31): mark which callsite results are `error`-typed (CallSite.error_results) so the core's default leaf can refuse to taint them — the largest FP class in the tree (doc 30 §6.2). Needs the core's --no-error-leaf to have any effect; DEFAULT OFF — emission change, off must reproduce the previous CGF byte-for-byte")
	buildCmd.Flags().BoolVar(&errStrict, "error-results-strict", false, "B1 (doc 31 §6a): also drop the result-TUPLE source alias for multi-result calls, so the core's error filter reaches `x, err := f()` producers and not only 1-result ones. Ignored without --error-results; DEFAULT OFF — measured at +79 FP keys removed and 2 real keys lost on backend-a, so the recall cost stays attributable")
	buildCmd.Flags().StringVar(&pbPaths, "pb-paths", "pb,api", "comma-separated PATH SEGMENTS that mark a generated protobuf/gRPC package (matched as \"/<seg>/\" or a trailing \"/<seg>\"). This one list gates remote-call recognition, stream ports, pb-getter canonicalization and gRPC handler request-param sourcing — a layout it does not match silently loses the cross-repo join. Add your own: --pb-paths=pb,api,gen,proto,genproto. Rides in the cgstore key, so a change cold-starts the graph cache")
	buildCmd.Flags().StringVar(&mockPaths, "mock-paths", "mock,mocks", "comma-separated path segments that mark a mockgen package (dropped unless --include-mocks). mockgen's own naming — a `mock_` package prefix, `_mock.go` / `zzz_` files — is always recognised and not configurable")
	buildCmd.Flags().BoolVar(&requireContr, "require-contracts", false, "exit 3 when the extraction finds no gRPC method, no GraphQL field and no remote call site, instead of only warning. The monoculture failure mode (protoc-gen-go-grpc / gqlgen only, plus --pb-paths) otherwise looks like a successful run with an empty result — use this in CI")
	buildCmd.Flags().BoolVar(&byRefOut, "byref-out", false, "by-ref param/receiver out-slots + the caller-side arg back-edge (W1a, doc 29); DEFAULT OFF — an emission change, so off must reproduce the previous CGF byte-for-byte")

	root.AddCommand(buildCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, emit.ErrNoContracts) {
			os.Exit(3) // --require-contracts: distinct from a real failure
		}
		os.Exit(1)
	}
}

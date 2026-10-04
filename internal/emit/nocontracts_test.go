package emit

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
)

// testdata/clientonly has exactly one boundary: a call on pb.SearchClient.
// Under the default --pb-paths it is an INVOKES_REMOTE site, so the run is
// productive; under a list that does not match its layout the extraction
// recognises nothing at all — the silent-empty-result failure of assessment
// §4/§9, which --require-contracts must turn into an error.
func runClientOnly(t *testing.T, pbPaths pkgclass.PbPaths, require bool) error {
	t.Helper()
	return Run(Options{
		RepoDir:          "testdata/clientonly",
		Scope:            "./...",
		OutDir:           t.TempDir(),
		PbPaths:          pbPaths,
		RequireContracts: require,
	})
}

func TestNoContractsGuard(t *testing.T) {
	if err := runClientOnly(t, nil, true); err != nil {
		t.Fatalf("default --pb-paths: the remote call site must be recognised, got %v", err)
	}
	miss := pkgclass.PbPaths{"nonexistent"}
	if err := runClientOnly(t, miss, false); err != nil {
		t.Fatalf("without --require-contracts an empty extraction stays a warning, got %v", err)
	}
	err := runClientOnly(t, miss, true)
	if !errors.Is(err, ErrNoContracts) {
		t.Fatalf("--require-contracts with a mismatched --pb-paths: err = %v, want ErrNoContracts", err)
	}
}

// TestNoContractsGuardStillWritesCGF is the CI regression this reorder fixes:
// ErrNoContracts used to return BEFORE the CGF write loop, so a
// --require-contracts failure in CI produced exit 3 and no artifact to
// diagnose. The write loop must run first; ErrNoContracts comes only after.
func TestNoContractsGuardStillWritesCGF(t *testing.T) {
	out := t.TempDir()
	miss := pkgclass.PbPaths{"nonexistent"}
	err := Run(Options{
		RepoDir:          "testdata/clientonly",
		Scope:            "./...",
		OutDir:           out,
		PbPaths:          miss,
		RequireContracts: true,
	})
	if !errors.Is(err, ErrNoContracts) {
		t.Fatalf("err = %v, want ErrNoContracts", err)
	}
	entries, rerr := os.ReadDir(out)
	if rerr != nil {
		t.Fatalf("reading out dir: %v", rerr)
	}
	if len(entries) == 0 {
		t.Fatal("ErrNoContracts must still leave the CGF written to --out; out dir is empty")
	}
}

func TestNoContractsHintNamesGeneratorsAndFlag(t *testing.T) {
	h := noContractsHint(Options{PbPaths: pkgclass.PbPaths{"gen"}})
	for _, want := range []string{
		"NO CONTRACTS AND NO REMOTE CALLS",
		"protoc-gen-go-grpc",
		"gqlgen v2",
		"--pb-paths",
		"currently: gen",
		"--require-contracts",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("hint does not mention %q:\n%s", want, h)
		}
	}
	if strings.Count(h, "\n") < 5 {
		t.Error("the hint must be multi-line")
	}
}

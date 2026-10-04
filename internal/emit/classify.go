package emit

import (
	"strings"

	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
)

// excludedFile reports whether a file is a test or mock (excluded from analysis,
// doc 02 §5). Generated proto/gqlgen is NOT excluded here — it is kept, flagged.
func excludedFile(path string) bool {
	base := path[strings.LastIndex(path, "/")+1:]
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, "_mock.go"):
		return true
	case strings.HasPrefix(base, "zzz_"):
		return true
	}
	return false
}

func generatorOf(path string) pb.Generator {
	base := path[strings.LastIndex(path, "/")+1:]
	switch {
	case strings.HasSuffix(base, ".pb.go"),
		strings.HasSuffix(base, "_grpc.pb.go"),
		strings.HasSuffix(base, "_vtproto.pb.go"):
		return pb.Generator_GEN_PROTOC
	case strings.HasSuffix(base, ".generated.go"),
		strings.HasSuffix(base, "models_gen.go"):
		return pb.Generator_GEN_GQLGEN
	case strings.HasSuffix(base, "_mock.go"), strings.HasPrefix(base, "zzz_"):
		return pb.Generator_GEN_MOCKGEN
	}
	return pb.Generator_GEN_NONE
}

// originOf classifies a package path relative to the analyzed module.
func originOf(pkgPath, module string) pb.Origin {
	if module != "" && (pkgPath == module || strings.HasPrefix(pkgPath, module+"/")) {
		return pb.Origin_ORIGIN_USER
	}
	// stdlib packages have no dot in their first path segment.
	first := pkgPath
	if i := strings.IndexByte(pkgPath, '/'); i >= 0 {
		first = pkgPath[:i]
	}
	if !strings.Contains(first, ".") {
		return pb.Origin_ORIGIN_STDLIB
	}
	return pb.Origin_ORIGIN_DEP
}

func fileOf(fn *ssa.Function) string {
	if fn.Prog == nil || !fn.Pos().IsValid() {
		return ""
	}
	return fn.Prog.Fset.Position(fn.Pos()).Filename
}

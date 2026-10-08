package frameworks

import (
	"testing"
	"time"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/loader"
)

func loadStrs(t *testing.T) *loader.Loaded {
	t.Helper()
	ld, err := loader.Load("testdata/strs", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return ld
}

// argOf returns argument i of the first static call to callee in fn.
func argOf(t *testing.T, ld *loader.Loaded, fn, callee string, i int) ssa.Value {
	t.Helper()
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.String() != fn || f.Blocks == nil {
			continue
		}
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				c, ok := in.(*ssa.Call)
				if !ok {
					continue
				}
				if sc := c.Call.StaticCallee(); sc != nil && sc.String() == callee {
					return c.Call.Args[i]
				}
			}
		}
	}
	t.Fatalf("no call to %s in %s", callee, fn)
	return nil
}

func TestEvalTemplates(t *testing.T) {
	ld := loadStrs(t)
	cases := []struct {
		fn, template string
		known        bool
		canon        string // ClientPath then CanonPath
	}{
		{"Const", "/api/users", true, "/api/users"},
		{"Concat", "{}/api/users/{}", false, "/{}/api/users/{}"},
		{"Sprintf", "{}/api/users/{}?n={}&p=100%", false, "/{}/api/users/{}"},
		{"JoinPath", "{}/api/users/{}", false, "/{}/api/users/{}"},
		{"PathJoin", "/api/users/{}", false, "/api/users/{}"},
		{"PhiSame", "/same", true, "/same"},
		{"PhiDiff", "{}", false, "/{}"},
		{"Env", "{}", false, "/{}"}, // HTTP evaluation: the environment is a hole
		{"FromConfig", "{}", false, "/{}"},
		{"FromParam", "{}", false, "/{}"},
		{"Captured$1", "{}", false, "/{}"},
	}
	for _, c := range cases {
		v := argOf(t, ld, "example.com/strs."+c.fn, "example.com/strs.Sink", 0)
		s := Eval{}.Of(v)
		if got := s.Template(); got != c.template {
			t.Errorf("%s: template %q, want %q", c.fn, got, c.template)
		}
		if _, ok := s.Known(); ok != c.known {
			t.Errorf("%s: known=%v, want %v", c.fn, ok, c.known)
		}
		if got := CanonPath(ClientPath(s)); got != c.canon {
			t.Errorf("%s: canonical %q, want %q", c.fn, got, c.canon)
		}
	}
}

// Topics: os.Getenv is a symbol, and the census split (§2.4) needs to know
// whether a hole came from a parameter or a config field.
func TestEvalTopicSymbols(t *testing.T) {
	ld := loadStrs(t)
	env := Eval{EnvSymbols: true}
	cases := []struct {
		fn, symbol string
		ok         bool
		cause      HoleCause
	}{
		{"Const", "/api/users", true, CauseOther},
		// review item 10: a symbol is ${env:NAME}, which no topic name can
		// spell, so the literal topic "env:ORDERS_TOPIC" stays distinct
		{"Env", "${env:ORDERS_TOPIC}", true, CauseOther},
		{"EnvConcat", "${env:ENV}.orders", true, CauseOther},
		{"LiteralEnv", "env:ORDERS_TOPIC", true, CauseOther},
		{"FromParam", "", false, CauseParam},
		{"FromConfig", "", false, CauseField},
		{"PhiDiff", "", false, CauseOther},
	}
	for _, c := range cases {
		s := env.Of(argOf(t, ld, "example.com/strs."+c.fn, "example.com/strs.Sink", 0))
		sym, ok := s.Symbol()
		if ok != c.ok || sym != c.symbol {
			t.Errorf("%s: symbol (%q, %v), want (%q, %v)", c.fn, sym, ok, c.symbol, c.ok)
		}
		if cause, _ := s.FirstCause(); cause != c.cause {
			t.Errorf("%s: cause %v, want %v", c.fn, cause, c.cause)
		}
	}
	_, pkg := env.Of(argOf(t, ld, "example.com/strs.FromConfig", "example.com/strs.Sink", 0)).FirstCause()
	if pkg != "example.com/strs" {
		t.Errorf("config field hole names package %q, want example.com/strs", pkg)
	}
}

func TestFieldStoresAndSliceElems(t *testing.T) {
	ld := loadStrs(t)
	field := func(fn, callee, name string) (string, bool, int) {
		vals, ok := FieldStores(argOf(t, ld, "example.com/strs."+fn, "example.com/strs."+callee, 0), name)
		if !ok || len(vals) != 1 {
			return "", ok, len(vals)
		}
		s, _ := Eval{}.Of(vals[0]).Known()
		return s, ok, 1
	}
	if s, ok, n := field("Literal", "UseWriter", "Topic"); !ok || n != 1 || s != "orders" {
		t.Errorf("&Writer{Topic}: (%q, %v, %d), want orders", s, ok, n)
	}
	if s, ok, n := field("ValueLiteral", "UseConfig", "Topic"); !ok || n != 1 || s != "events" {
		t.Errorf("Writer{Topic} by value: (%q, %v, %d), want events", s, ok, n)
	}
	if _, ok, n := field("ZeroField", "UseWriter", "Topic"); !ok || n != 0 {
		t.Errorf("literal leaving Topic zero: ok=%v n=%d, want ok with no stores", ok, n)
	}
	if _, ok, _ := field("FromConfig", "Sink", "Topic"); ok {
		t.Errorf("a string is not a struct literal")
	}

	elems := func(fn, callee string) []string {
		vs, ok := SliceElems(argOf(t, ld, "example.com/strs."+fn, "example.com/strs."+callee, 0))
		if !ok {
			return nil
		}
		out := []string{}
		for _, v := range vs {
			s, _ := Eval{}.Of(v).Known()
			out = append(out, s)
		}
		return out
	}
	if got := elems("Elems", "UseList"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("[]string literal: %v, want [a b]", got)
	}
	if got := elems("Variadic", "UseVariadic"); len(got) != 3 || got[2] != "z" {
		t.Errorf("variadic: %v, want [x y z]", got)
	}
	if got := elems("NoVariadic", "UseVariadic"); got == nil || len(got) != 0 {
		t.Errorf("empty variadic: %v, want []", got)
	}
}

// Review item 6: the evaluator memoises per value, so a chain of switches
// appending to one variable costs one visit per value, not one per path.
func TestEvalPhiChainIsLinear(t *testing.T) {
	ld := loadStrs(t)
	v := argOf(t, ld, "example.com/strs.Blowup", "example.com/strs.Sink", 0)
	start := time.Now()
	s := Eval{}.Of(v)
	d := time.Since(start)
	t.Logf("8x10 switch chain evaluated in %v", d)
	if d > 200*time.Millisecond {
		t.Errorf("evaluating the 8x10 switch chain took %v", d)
	}
	if s.Template() != "{}" || s.Exhausted() {
		t.Errorf("Blowup = %q (exhausted %v), want a plain hole: the switch arms differ", s.Template(), s.Exhausted())
	}
}

// The step budget is the backstop: when it runs out the value is a hole that
// says so, for the census's eval_budget count.
func TestEvalBudgetExhaustion(t *testing.T) {
	ld := loadStrs(t)
	old := maxEvalSteps
	maxEvalSteps = 3
	defer func() { maxEvalSteps = old }()
	s := Eval{}.Of(argOf(t, ld, "example.com/strs.Concat", "example.com/strs.Sink", 0))
	if !s.Exhausted() {
		t.Errorf("Concat under a 3-step budget = %q, want an exhausted hole", s.Template())
	}
	maxEvalSteps = old
	if s := (Eval{}).Of(argOf(t, ld, "example.com/strs.Concat", "example.com/strs.Sink", 0)); s.Exhausted() {
		t.Error("the default budget must not be exhausted by a three-piece concatenation")
	}
}

// Review items 5 and 7: joining a prefix and a path keeps segment
// boundaries; gin also separates literals (path.Join), the others do not.
func TestJoinPath(t *testing.T) {
	hole := Unknown(CauseOther)
	cases := []struct {
		a, b  Str
		slash bool
		want  string
	}{
		{Lit("/api"), hole, false, "/api/{}"},
		{JoinPath(Lit("/api"), hole, false), Lit("/mid"), false, "/api/{}/mid"},
		{Concat(Lit("/v"), hole), Lit("/x"), false, "/v{}/x"}, // a string concat IS one segment
		{Lit("/v1"), Lit("users"), true, "/v1/users"},         // gin
		{Lit("/eg"), Lit("users"), false, "/egusers"},         // echo concatenates
		{Lit("/v1/"), Lit("/users"), true, "/v1//users"},      // CanonPath drops the empty segment
		{Lit(""), Lit("/x"), false, "/x"},
		{hole, Lit("/x"), false, "{}/x"}, // still a leading hole
		{Lit("/a"), Lit(""), true, "/a"},
	}
	for _, c := range cases {
		got := JoinPath(c.a, c.b, c.slash)
		if got.Template() != c.want {
			t.Errorf("JoinPath(%q, %q, %v) = %q, want %q", c.a.Template(), c.b.Template(), c.slash, got.Template(), c.want)
		}
	}
	if !JoinPath(hole, Lit("/x"), false).LeadingHole() {
		t.Error("an unknown prefix must stay a leading hole")
	}
}

// Review item 11: net/http subtree patterns are catch-alls.
func TestSubtree(t *testing.T) {
	for in, want := range map[string]string{
		"/static/": "/static/{*}",
		"/":        "/{*}",
		"/x":       "/x",
		"/x/{$}":   "/x/{$}",
	} {
		if got := Subtree(Lit(in)).Template(); got != want {
			t.Errorf("Subtree(%q) = %q, want %q", in, got, want)
		}
	}
	_, p := SplitPattern(Lit("example.com/"))
	if got := CanonPath(Subtree(p).Template()); got != "/{*}" {
		t.Errorf("host-only pattern: %q, want /{*}", got)
	}
}

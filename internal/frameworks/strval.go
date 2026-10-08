package frameworks

import (
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Str is an abstract string value: literal pieces and holes, in order. It is
// what the evaluator recovers for a route pattern, a client URL or a topic, so
// "base + "/api/users"" is the hole followed by the literal "/api/users".
type Str struct {
	parts []piece
	// exhausted: some piece is a hole only because the evaluation ran out of
	// its step budget (review item 6) — counted in the census lines.
	exhausted bool
}

type piece struct {
	lit string
	// hole: an unresolved piece. cause says why — only the topic census reads
	// it (§2.4 census split: from_param / from_config / other).
	hole  bool
	cause HoleCause
	// causePkg: for CauseField, the import path of the struct read from.
	causePkg string
	// env: os.Getenv(env) under Eval.EnvSymbols — a symbol, not a hole.
	env string
}

// HoleCause classifies the first unresolved piece of a value.
type HoleCause uint8

const (
	// CauseOther: a call result, a package variable, a recursion or depth cut.
	CauseOther HoleCause = iota
	// CauseParam: a parameter of the enclosing function — an in-house wrapper
	// forwards the value (`NewWriter(topic string)`), which is wave 2.
	CauseParam
	// CauseField: a field load on a struct — typically a config struct
	// (`cfg.Kafka.Topic`). FirstCause also names the struct's package, so the
	// census can tell a config struct from a library handle.
	CauseField
)

// Lit is the constant string s. The empty string has no pieces, so a
// leading hole after an empty prefix is still leading.
func Lit(s string) Str {
	if s == "" {
		return Str{}
	}
	return Str{parts: []piece{{lit: s}}}
}

// Unknown is a single hole with the given cause.
func Unknown(c HoleCause) Str { return Str{parts: []piece{{hole: true, cause: c}}} }

// Concat appends b to a, merging adjacent literals and collapsing adjacent
// holes (two holes in a row cannot be told apart in a path anyway).
func Concat(a, b Str) Str {
	out := Str{exhausted: a.exhausted || b.exhausted}
	for _, p := range append(append([]piece{}, a.parts...), b.parts...) {
		n := len(out.parts)
		switch {
		case p.hole && n > 0 && out.parts[n-1].hole:
			continue
		case !p.hole && p.env == "" && n > 0 && !out.parts[n-1].hole && out.parts[n-1].env == "":
			out.parts[n-1].lit += p.lit
		case !p.hole && p.env == "" && p.lit == "":
			continue
		default:
			out.parts = append(out.parts, p)
		}
	}
	return out
}

// Template renders holes (and env symbols) as {} — the form CanonPath takes.
func (s Str) Template() string {
	var b strings.Builder
	for _, p := range s.parts {
		if p.hole || p.env != "" {
			b.WriteString(Hole)
		} else {
			b.WriteString(p.lit)
		}
	}
	return b.String()
}

// Known returns the string when it is fully constant.
func (s Str) Known() (string, bool) {
	var b strings.Builder
	for _, p := range s.parts {
		if p.hole || p.env != "" {
			return "", false
		}
		b.WriteString(p.lit)
	}
	return b.String(), true
}

// Symbol returns the string with every os.Getenv piece written as
// `${env:NAME}`, or false when any piece is unresolved. Two services
// configured by the same variable name therefore produce the same symbol
// (§2.4). `$`, `{` and `}` cannot occur in a Kafka topic name, so a literal
// topic never collides with a symbolic one (a topic named "env:X" is just
// that literal).
func (s Str) Symbol() (string, bool) {
	var b strings.Builder
	for _, p := range s.parts {
		switch {
		case p.hole:
			return "", false
		case p.env != "":
			b.WriteString("${env:" + p.env + "}")
		default:
			b.WriteString(p.lit)
		}
	}
	return b.String(), true
}

// HasLiteral reports whether any piece is a non-empty literal.
func (s Str) HasLiteral() bool {
	for _, p := range s.parts {
		if !p.hole && p.env == "" && p.lit != "" {
			return true
		}
	}
	return false
}

// LeadingHole reports whether the value starts with an unresolved piece — an
// unresolved base URL or mount prefix.
func (s Str) LeadingHole() bool {
	return len(s.parts) > 0 && (s.parts[0].hole || s.parts[0].env != "")
}

// TrimLeadingHole drops a leading unresolved piece.
func (s Str) TrimLeadingHole() Str {
	if !s.LeadingHole() {
		return s
	}
	return Str{parts: append([]piece{}, s.parts[1:]...)}
}

// FirstCause is the cause of the first hole (CauseOther when there is none)
// and, for CauseField, the import path of the struct the field was read from.
func (s Str) FirstCause() (HoleCause, string) {
	for _, p := range s.parts {
		if p.hole {
			return p.cause, p.causePkg
		}
	}
	return CauseOther, ""
}

// Equal compares two values piece by piece.
func (s Str) Equal(o Str) bool {
	if len(s.parts) != len(o.parts) {
		return false
	}
	for i := range s.parts {
		a, b := s.parts[i], o.parts[i]
		if a.hole != b.hole || a.lit != b.lit || a.env != b.env {
			return false
		}
	}
	return true
}

// Exhausted reports that part of the value is unknown only because the
// evaluator's step budget ran out.
func (s Str) Exhausted() bool { return s.exhausted }

// EndsWithSlash reports whether the value's last piece is a literal ending
// in "/" — a net/http subtree pattern.
func (s Str) EndsWithSlash() bool {
	n := len(s.parts)
	return n > 0 && !s.parts[n-1].hole && s.parts[n-1].env == "" && strings.HasSuffix(s.parts[n-1].lit, "/")
}

// JoinPath appends path b to router prefix a, keeping segment boundaries a
// plain string concatenation would lose. A hole that starts b is a whole
// path piece (a chi `Route(ver, …)` pattern must itself begin with "/"), so
// "/api" + hole gives "/api/{}", not the single segment "api{}". With slash
// set, a literal also gets a separator: gin joins with path.Join semantics,
// so Group("/v1").GET("users") serves /v1/users. Without it the frameworks
// concatenate as written (echo, gorilla; chi patterns begin with "/").
func JoinPath(a, b Str, slash bool) Str {
	if len(b.parts) == 0 {
		return Str{parts: a.parts, exhausted: a.exhausted || b.exhausted}
	}
	if len(a.parts) == 0 {
		return Str{parts: b.parts, exhausted: a.exhausted || b.exhausted}
	}
	last, first := a.parts[len(a.parts)-1], b.parts[0]
	leftSlash := !last.hole && last.env == "" && strings.HasSuffix(last.lit, "/")
	firstLit := !first.hole && first.env == ""
	rightSlash := firstLit && strings.HasPrefix(first.lit, "/")
	if !leftSlash && !rightSlash && (!firstLit || slash) {
		a = Concat(a, Lit("/"))
	}
	return Concat(a, b)
}

// ClientPath renders a client URL for CanonPath. A leading unresolved base
// stays one {} segment (§1.1 rule 5); a base followed directly by a relative
// literal (`base + "api/users"`, the base carrying the slash) gets the
// separator, or the hole would swallow the first literal segment.
func ClientPath(s Str) string {
	t := s.Template()
	if s.LeadingHole() && len(s.parts) > 1 && !s.parts[1].hole && s.parts[1].env == "" &&
		s.parts[1].lit != "" && !strings.HasPrefix(s.parts[1].lit, "/") &&
		!strings.HasPrefix(s.parts[1].lit, "?") && !strings.HasPrefix(s.parts[1].lit, "#") {
		return Hole + "/" + strings.TrimPrefix(t, Hole)
	}
	return t
}

// Eval is the bounded abstract string evaluator of §2.3: constants; `+`;
// fmt.Sprintf with a constant format (each verb a hole); url.JoinPath and
// path.Join; a Phi whose edges agree; a local variable with one store; and,
// under EnvSymbols, os.Getenv("NAME") as the symbol env:NAME (§2.4). Anything
// else is a hole. It never builds or mutates SSA.
type Eval struct {
	// EnvSymbols evaluates os.Getenv with a constant name to a symbol instead
	// of a hole. Topics only: a base URL from the environment is still an
	// unknown prefix of a path.
	EnvSymbols bool
}

// maxEvalDepth bounds the walk: real templates are a handful of `+` deep.
const maxEvalDepth = 16

// maxEvalSteps is the per-evaluation step budget. Values are memoised, so a
// chain of switch statements each appending to the same variable is linear,
// not exponential in the Phi fan-in (review item 6); the budget is the
// backstop for whatever the memo does not cover. Exhaustion is a hole, and
// Str.Exhausted reports it for the census.
var maxEvalSteps = 4096 // a var only so a test can exhaust it

// evalState is one evaluation's memo and budget.
type evalState struct {
	memo     map[ssa.Value]Str
	visiting map[ssa.Value]bool
	steps    int
}

// Of evaluates v.
func (e Eval) Of(v ssa.Value) Str {
	return e.of(v, 0, &evalState{memo: map[ssa.Value]Str{}, visiting: map[ssa.Value]bool{}})
}

func (e Eval) of(v ssa.Value, depth int, st *evalState) Str {
	if v == nil || depth > maxEvalDepth || st.visiting[v] {
		return Unknown(CauseOther)
	}
	if s, ok := st.memo[v]; ok {
		return s
	}
	st.steps++
	if st.steps > maxEvalSteps {
		s := Unknown(CauseOther)
		s.exhausted = true
		return s
	}
	s := e.eval(v, depth, st)
	st.memo[v] = s
	return s
}

func (e Eval) eval(v ssa.Value, depth int, st *evalState) Str {
	switch v := v.(type) {
	case *ssa.Const:
		if v.Value == nil {
			if isString(v.Type()) {
				return Lit("") // the zero string
			}
			return Unknown(CauseOther)
		}
		if v.Value.Kind() == constant.String {
			return Lit(constant.StringVal(v.Value))
		}
	case *ssa.BinOp:
		if v.Op == token.ADD && isString(v.Type()) {
			return Concat(e.of(v.X, depth+1, st), e.of(v.Y, depth+1, st))
		}
	case *ssa.ChangeType:
		return e.of(v.X, depth+1, st)
	case *ssa.Phi:
		st.visiting[v] = true
		defer delete(st.visiting, v)
		var out Str
		for i, ed := range v.Edges {
			s := e.of(ed, depth+1, st)
			if i == 0 {
				out = s
			} else if !s.Equal(out) {
				u := Unknown(CauseOther)
				u.exhausted = s.exhausted || out.exhausted
				return u
			}
		}
		if len(v.Edges) > 0 {
			return out
		}
	case *ssa.Call:
		return e.call(v, depth, st)
	case *ssa.Extract:
		// url.JoinPath returns (string, error): the value is extract #0.
		if call, ok := v.Tuple.(*ssa.Call); ok && v.Index == 0 && calleeIs(call, "net/url", "JoinPath") {
			return e.joined(call.Call.Args, depth, st, true)
		}
	case *ssa.UnOp:
		if v.Op == token.MUL {
			switch x := v.X.(type) {
			case *ssa.Alloc:
				if stored := singleStore(x); stored != nil {
					return e.of(stored, depth+1, st)
				}
			case *ssa.FieldAddr:
				return fieldHole(x.X.Type())
			}
		}
	case *ssa.Field:
		return fieldHole(v.X.Type())
	case *ssa.Parameter:
		return Unknown(CauseParam)
	}
	return Unknown(CauseOther)
}

func (e Eval) call(c *ssa.Call, depth int, st *evalState) Str {
	args := c.Call.Args
	switch {
	case calleeIs(c, "fmt", "Sprintf") && len(args) >= 1:
		f, ok := e.of(args[0], depth+1, st).Known()
		if !ok {
			return Unknown(CauseOther)
		}
		return sprintfTemplate(f)
	case calleeIs(c, "os", "Getenv") && len(args) == 1:
		name, ok := e.of(args[0], depth+1, st).Known()
		if ok && e.EnvSymbols {
			return Str{parts: []piece{{env: name}}}
		}
	case calleeIs(c, "path", "Join"):
		return e.joined(args, depth, st, false)
	}
	return Unknown(CauseOther)
}

// joined evaluates url.JoinPath(base, elem...) (withBase) or path.Join(elem...)
// as the pieces separated by "/". CanonPath drops the empty segments a doubled
// separator leaves.
func (e Eval) joined(args []ssa.Value, depth int, st *evalState, withBase bool) Str {
	var out Str
	first := true
	add := func(v ssa.Value) {
		s := e.of(v, depth+1, st)
		if !first {
			out = Concat(out, Lit("/"))
		}
		out = Concat(out, s)
		first = false
	}
	rest := args
	if withBase {
		if len(args) == 0 {
			return Unknown(CauseOther)
		}
		add(args[0])
		rest = args[1:]
	}
	for _, v := range rest {
		elems, ok := SliceElems(v)
		if !ok {
			return Concat(out, Unknown(CauseOther))
		}
		for _, el := range elems {
			add(el)
		}
	}
	return out
}

// sprintfTemplate turns a constant format into a template: every verb is a
// hole, `%%` is a literal percent sign. Flags, width, precision and explicit
// argument indexes (`%[1]s`) belong to the verb.
func sprintfTemplate(f string) Str {
	var out Str
	var lit strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			lit.WriteByte(f[i])
			continue
		}
		if i+1 < len(f) && f[i+1] == '%' {
			lit.WriteByte('%')
			i++
			continue
		}
		j := i + 1
		for j < len(f) && !isVerb(f[j]) {
			j++
		}
		out = Concat(out, Lit(lit.String()))
		lit.Reset()
		out = Concat(out, Unknown(CauseOther))
		i = j
	}
	return Concat(out, Lit(lit.String()))
}

func isVerb(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// fieldHole is the hole a field load leaves, tagged with the package of the
// struct (pointer or value) it was read from.
func fieldHole(x types.Type) Str {
	pkg := ""
	if p, ok := x.Underlying().(*types.Pointer); ok {
		x = p.Elem()
	}
	if n, ok := x.(*types.Named); ok && n.Obj().Pkg() != nil {
		pkg = n.Obj().Pkg().Path()
	}
	return Str{parts: []piece{{hole: true, cause: CauseField, causePkg: pkg}}}
}

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// calleeIs reports whether c statically calls the package-level function
// pkg.name.
func calleeIs(c *ssa.Call, pkg, name string) bool {
	sc := c.Call.StaticCallee()
	if sc == nil || sc.Signature.Recv() != nil || sc.Pkg == nil || sc.Pkg.Pkg == nil {
		return false
	}
	return sc.Pkg.Pkg.Path() == pkg && sc.Name() == name
}

// singleStore returns the value stored into a local variable when that is
// its only write and its address goes nowhere else. Anything more — a second
// store, a closure binding the address, a call taking it — and the variable's
// content is not knowable locally.
func singleStore(a *ssa.Alloc) ssa.Value {
	refs := a.Referrers()
	if refs == nil {
		return nil
	}
	var val ssa.Value
	for _, r := range *refs {
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr != a || val != nil {
				return nil
			}
			val = r.Val
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return nil
			}
		case *ssa.DebugRef:
		default:
			return nil
		}
	}
	return val
}

// SliceElems returns the elements of a slice built from a literal or a
// variadic call (`[]string{"a","b"}`, `f(x, y)`), in index order: SSA writes
// such a slice as stores into the elements of a fresh array. A nil slice is
// empty. ok=false: the slice came from anywhere else.
func SliceElems(v ssa.Value) ([]ssa.Value, bool) {
	if c, ok := v.(*ssa.Const); ok && c.Value == nil {
		return nil, true
	}
	sl, ok := v.(*ssa.Slice)
	if !ok {
		return nil, false
	}
	arr, ok := sl.X.(*ssa.Alloc)
	if !ok || sl.Low != nil || sl.High != nil {
		return nil, false
	}
	at, ok := arr.Type().Underlying().(*types.Pointer)
	if !ok {
		return nil, false
	}
	array, ok := at.Elem().Underlying().(*types.Array)
	if !ok {
		return nil, false
	}
	n := int(array.Len())
	out := make([]ssa.Value, n)
	refs := arr.Referrers()
	if refs == nil {
		return nil, false
	}
	for _, r := range *refs {
		ia, ok := r.(*ssa.IndexAddr)
		if !ok {
			continue
		}
		idx, ok := ia.Index.(*ssa.Const)
		if !ok {
			return nil, false
		}
		i := int(idx.Int64())
		if i < 0 || i >= n || ia.Referrers() == nil {
			return nil, false
		}
		for _, w := range *ia.Referrers() {
			if st, ok := w.(*ssa.Store); ok && st.Addr == ia {
				if out[i] != nil {
					return nil, false
				}
				out[i] = st.Val
			}
		}
	}
	for _, el := range out {
		if el == nil {
			return nil, false
		}
	}
	return out, true
}

// FieldStores returns the values stored into field name of a struct built by
// a local composite literal (`&kafka.Writer{Topic: "x"}`, `kafka.ReaderConfig{
// Topic: "x"}` passed by value), peeling the boxing and the load SSA puts
// between the literal and its use. ok=false: v is not such a literal. ok with
// no values: the literal leaves the field zero.
func FieldStores(v ssa.Value, name string) ([]ssa.Value, bool) {
	alloc := literalAlloc(v)
	if alloc == nil {
		return nil, false
	}
	st, _ := structFields(alloc.Type())
	if st == nil {
		return nil, false
	}
	field := -1
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name {
			field = i
			break
		}
	}
	if field < 0 {
		return nil, false
	}
	var out []ssa.Value
	for _, r := range *alloc.Referrers() {
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr == alloc {
				return nil, false // a whole-struct overwrite: not a literal
			}
		case *ssa.FieldAddr:
			if r.Field != field || r.Referrers() == nil {
				continue
			}
			for _, w := range *r.Referrers() {
				if s, ok := w.(*ssa.Store); ok && s.Addr == r {
					out = append(out, s.Val)
				}
			}
		}
	}
	return out, true
}

// literalAlloc finds the *ssa.Alloc of a struct literal behind v.
func literalAlloc(v ssa.Value) *ssa.Alloc {
	for i := 0; i < 8; i++ {
		switch x := v.(type) {
		case *ssa.Alloc:
			if x.Referrers() == nil {
				return nil
			}
			return x
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil
			}
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		default:
			return nil
		}
	}
	return nil
}

func structFields(ptr types.Type) (*types.Struct, *types.Named) {
	p, ok := ptr.Underlying().(*types.Pointer)
	if !ok {
		return nil, nil
	}
	named, _ := p.Elem().(*types.Named)
	st, _ := p.Elem().Underlying().(*types.Struct)
	return st, named
}

// DefCall returns the call that produced v through local def-use — the
// constructor of a handle (`kafka.NewReader(cfg)`), seen through the boxing,
// a tuple extract and a local variable with one store.
func DefCall(v ssa.Value) *ssa.Call {
	for i := 0; i < 8; i++ {
		switch x := v.(type) {
		case *ssa.Call:
			return x
		case *ssa.Extract:
			v = x.Tuple
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.UnOp:
			a, ok := x.X.(*ssa.Alloc)
			if x.Op != token.MUL || !ok {
				return nil
			}
			if v = singleStore(a); v == nil {
				return nil
			}
		default:
			return nil
		}
	}
	return nil
}

// SplitPattern splits a Go 1.22 ServeMux pattern `[METHOD ][HOST]/path` into
// its method ("" when absent) and path. A host is dropped: routes are matched
// by path. Only a literal prefix can carry a method or a host; a pattern that
// starts with a hole (`base + "/x"`) is all path.
func SplitPattern(s Str) (string, Str) {
	if len(s.parts) == 0 || s.parts[0].hole || s.parts[0].env != "" {
		return "", s
	}
	head := s.parts[0].lit
	rest := Str{parts: append([]piece{}, s.parts[1:]...)}
	method := ""
	if i := strings.IndexAny(head, " \t"); i >= 0 && !strings.Contains(head[:i], "/") {
		method, head = head[:i], strings.TrimLeft(head[i:], " \t")
	}
	if head != "" && !strings.HasPrefix(head, "/") {
		if i := strings.IndexByte(head, '/'); i >= 0 {
			head = head[i:] // `example.com/x` -> `/x`
		} else if len(rest.parts) == 0 {
			head = "" // a bare host matches its whole tree
		}
	}
	return method, Concat(Lit(head), rest)
}

// Subtree turns a net/http subtree pattern — one ending in "/" — into the
// catch-all it is: "/static/" serves everything below it, so it is
// "/static/{*}", and a host-only "example.com/" is "/{*}" (review item 11).
// net/http semantics, so the frontend translates before canonicalising; an
// exact pattern ("/x", "/x/{$}") is unchanged.
func Subtree(path Str) Str {
	if !path.EndsWithSlash() {
		return path
	}
	return Concat(path, Lit(CatchAll))
}

// LocalValue sees through a local variable with a single store to the value
// stored (nil when v is not such a load).
func LocalValue(v ssa.Value) ssa.Value {
	if u, ok := v.(*ssa.UnOp); ok && u.Op == token.MUL {
		if a, ok := u.X.(*ssa.Alloc); ok {
			return singleStore(a)
		}
	}
	return nil
}

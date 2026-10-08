package frameworks

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// ---------------------------------------------------------------------------
// §2.1 surface reads
// ---------------------------------------------------------------------------

// SurfaceField lists the fields of one struct whose LOAD is an untrusted-input
// surface. A catalog matches call sites, and `r.Body` is a FieldAddr plus a
// load, so without a synthetic `read:` site the dominant Go JSON-API input
// pattern has no source at all (coverage wave 1, E2).
type SurfaceField struct {
	Pkg, Type string
	Fields    []string
}

// SurfaceReads is the §2.1 list. The request object itself is deliberately
// absent: seeding it whole taints every ctx it hands out (E3).
var SurfaceReads = []SurfaceField{
	{Pkg: "net/http", Type: "Request", Fields: []string{
		"Body", "Form", "PostForm", "MultipartForm", "Header", "Trailer", "URL", "Host", "RequestURI",
	}},
}

// SurfaceRead returns the synthetic callee of a load of field number field of
// the struct t (or pointer to it): `read:<import path>.<Type>.<Field>`.
func SurfaceRead(t types.Type, field int) (string, bool) {
	pkg, name, st := namedStruct(t)
	if st == nil || field < 0 || field >= st.NumFields() {
		return "", false
	}
	fname := st.Field(field).Name()
	for _, sf := range SurfaceReads {
		if sf.Pkg != pkg || sf.Type != name {
			continue
		}
		for _, f := range sf.Fields {
			if f == fname {
				return "read:" + pkg + "." + name + "." + fname, true
			}
		}
	}
	return "", false
}

// OutgoingRequests are where a *net/http.Request this process SENDS comes
// from: a `read:` site on one would be a false source (review item 12). A
// request built by these calls, a `&http.Request{}` literal, or the Request
// field of a *net/http.Response is outgoing; Clone and WithContext keep their
// receiver's origin (a handler's `r.WithContext(ctx)` is still the inbound
// request).
var OutgoingRequests = struct {
	Calls         []ClientCall // matched by Pkg, Recv, Name
	Literal       [2]string    // a composite literal of this type
	ResponseField [3]string    // {pkg, type, field}
	Derive        []string     // (*Request) methods returning a copy of the receiver
}{
	Calls: []ClientCall{
		{Pkg: "net/http", Name: "NewRequest"},
		{Pkg: "net/http", Name: "NewRequestWithContext"},
	},
	Literal:       [2]string{"net/http", "Request"},
	ResponseField: [3]string{"net/http", "Response", "Request"},
	Derive:        []string{"Clone", "WithContext"},
}

func namedStruct(t types.Type) (pkg, name string, st *types.Struct) {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", "", nil
	}
	st, _ = n.Underlying().(*types.Struct)
	return n.Obj().Pkg().Path(), n.Obj().Name(), st
}

// ---------------------------------------------------------------------------
// §2.2 server routes
// ---------------------------------------------------------------------------

// Framework labels, as written to HttpRoute.framework and the census.
const (
	NetHTTP = "net/http"
	Chi     = "chi"
	Gin     = "gin"
	Echo    = "echo"
	Gorilla = "gorilla"
)

// RouterOp is what a router-library call does to the routing tree.
type RouterOp uint8

const (
	// OpNew constructs a root router.
	OpNew RouterOp = iota
	// OpRoute registers a handler at a path.
	OpRoute
	// OpGroup returns a router at the receiver's prefix plus PathArg.
	OpGroup
	// OpSame returns the receiver (or a view of it) at the same prefix.
	OpSame
	// OpSub calls FnArg (a func(Router)) with a router at the receiver's
	// prefix plus PathArg (none when PathArg < 0), and returns that router.
	OpSub
	// OpMount serves the router HandlerArg under the receiver's prefix plus
	// PathArg, with the prefix stripped before it routes.
	OpMount
	// OpStrip is net/http.StripPrefix(prefix, h): h served with prefix
	// stripped — a mount when h is a router.
	OpStrip
)

// RouterCall is one row of the registration table: a method (or package
// function) of a router library and how its arguments read. Argument indices
// never count the receiver.
type RouterCall struct {
	Framework string
	Pkgs      []string // import paths; major-version variants are separate entries
	Recv      []string // receiver type names, concrete or interface; nil: package function
	Names     []string
	Op        RouterOp

	Method     string // OpRoute: the fixed method; "" = MethodArg, MethodsArg, the pattern, or "*"
	MethodArg  int    // a method-string argument, -1 none
	MethodsArg int    // a []string (or variadic) argument of methods, -1 none
	PathArg    int    // -1 none
	HandlerArg int    // -1 none
	// LastHandler: HandlerArg is a variadic handler chain whose LAST element
	// is the endpoint and the rest middleware (gin; §2.2 "handler = last func
	// arg").
	LastHandler bool
	FnArg       int // OpSub
	// PatternMethod: the path argument is a Go 1.22 "[METHOD ][HOST]/path"
	// pattern.
	PatternMethod bool
	// DefaultMux: a package-level registration on http.DefaultServeMux.
	DefaultMux bool
	// RouteChain: methods live on the *Route value the call returns or is
	// called on (gorilla `HandleFunc(..).Methods("GET")`).
	RouteChain bool
}

var (
	pkgNetHTTP = []string{"net/http"}
	pkgChi     = []string{"github.com/go-chi/chi/v5", "github.com/go-chi/chi"}
	pkgGin     = []string{"github.com/gin-gonic/gin"}
	pkgEcho    = []string{"github.com/labstack/echo/v4"}
	pkgGorilla = []string{"github.com/gorilla/mux"}
)

var (
	chiRecv      = []string{"Mux", "Router"}
	ginRecv      = []string{"Engine", "RouterGroup", "IRouter", "IRoutes"}
	echoRecv     = []string{"Echo", "Group"}
	gorillaRtr   = []string{"Router"}
	gorillaRoute = []string{"Route"}
)

// verbRoutes expands one registration row per HTTP verb, the method name
// spelled as the library spells it (chi `Get`, gin and echo `GET`).
func verbRoutes(fw string, pkgs, recv []string, verbs []string, base RouterCall) []RouterCall {
	var out []RouterCall
	for _, v := range verbs {
		r := base
		r.Framework, r.Pkgs, r.Recv, r.Names, r.Op = fw, pkgs, recv, []string{v}, OpRoute
		r.Method = strings.ToUpper(v)
		out = append(out, r)
	}
	return out
}

func row(fw string, pkgs, recv, names []string, op RouterOp) RouterCall {
	return RouterCall{Framework: fw, Pkgs: pkgs, Recv: recv, Names: names, Op: op,
		MethodArg: -1, MethodsArg: -1, PathArg: -1, HandlerArg: -1, FnArg: -1}
}

func with(r RouterCall, f func(*RouterCall)) RouterCall { f(&r); return r }

// RouterCalls is the §2.2 registration table.
var RouterCalls = func() []RouterCall {
	var t []RouterCall
	add := func(rs ...RouterCall) { t = append(t, rs...) }

	// net/http ServeMux, incl. Go 1.22 method patterns.
	add(row(NetHTTP, pkgNetHTTP, nil, []string{"NewServeMux"}, OpNew))
	add(with(row(NetHTTP, pkgNetHTTP, []string{"ServeMux"}, []string{"Handle", "HandleFunc"}, OpRoute),
		func(r *RouterCall) { r.PathArg, r.HandlerArg, r.PatternMethod = 0, 1, true }))
	add(with(row(NetHTTP, pkgNetHTTP, nil, []string{"Handle", "HandleFunc"}, OpRoute),
		func(r *RouterCall) { r.PathArg, r.HandlerArg, r.PatternMethod, r.DefaultMux = 0, 1, true, true }))
	add(with(row(NetHTTP, pkgNetHTTP, nil, []string{"StripPrefix"}, OpStrip),
		func(r *RouterCall) { r.PathArg, r.HandlerArg = 0, 1 }))

	// chi v5 (and the v1-v4 import path).
	add(row(Chi, pkgChi, nil, []string{"NewRouter", "NewMux"}, OpNew))
	add(verbRoutes(Chi, pkgChi, chiRecv,
		[]string{"Get", "Post", "Put", "Patch", "Delete", "Head", "Options", "Connect", "Trace"},
		with(row("", nil, nil, nil, 0), func(r *RouterCall) { r.PathArg, r.HandlerArg = 0, 1 }))...)
	add(with(row(Chi, pkgChi, chiRecv, []string{"Handle", "HandleFunc"}, OpRoute),
		func(r *RouterCall) { r.Method, r.PathArg, r.HandlerArg = "*", 0, 1 }))
	add(with(row(Chi, pkgChi, chiRecv, []string{"Method", "MethodFunc"}, OpRoute),
		func(r *RouterCall) { r.MethodArg, r.PathArg, r.HandlerArg = 0, 1, 2 }))
	add(with(row(Chi, pkgChi, chiRecv, []string{"Route"}, OpSub), func(r *RouterCall) { r.PathArg, r.FnArg = 0, 1 }))
	add(with(row(Chi, pkgChi, chiRecv, []string{"Group"}, OpSub), func(r *RouterCall) { r.FnArg = 0 }))
	add(row(Chi, pkgChi, chiRecv, []string{"With"}, OpSame))
	add(with(row(Chi, pkgChi, chiRecv, []string{"Mount"}, OpMount), func(r *RouterCall) { r.PathArg, r.HandlerArg = 0, 1 }))

	// gin: *Engine embeds RouterGroup, so `engine.GET` is (*RouterGroup).GET
	// on &engine.RouterGroup; the interfaces cover generated and helper code.
	add(row(Gin, pkgGin, nil, []string{"New", "Default"}, OpNew))
	add(verbRoutes(Gin, pkgGin, ginRecv, []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		with(row("", nil, nil, nil, 0), func(r *RouterCall) { r.PathArg, r.HandlerArg, r.LastHandler = 0, 1, true }))...)
	add(with(row(Gin, pkgGin, ginRecv, []string{"Any"}, OpRoute),
		func(r *RouterCall) { r.Method, r.PathArg, r.HandlerArg, r.LastHandler = "*", 0, 1, true }))
	add(with(row(Gin, pkgGin, ginRecv, []string{"Handle"}, OpRoute),
		func(r *RouterCall) { r.MethodArg, r.PathArg, r.HandlerArg, r.LastHandler = 0, 1, 2, true }))
	add(with(row(Gin, pkgGin, ginRecv, []string{"Match"}, OpRoute),
		func(r *RouterCall) { r.MethodsArg, r.PathArg, r.HandlerArg, r.LastHandler = 0, 1, 2, true }))
	add(with(row(Gin, pkgGin, ginRecv, []string{"Group"}, OpGroup), func(r *RouterCall) { r.PathArg = 0 }))
	// `Use` returns the same group (IRoutes), so `g.Use(mw).GET(..)` keeps
	// g's prefix. echo, chi and gorilla `Use` return nothing; chi `With` is
	// the chaining form, above.
	add(row(Gin, pkgGin, ginRecv, []string{"Use"}, OpSame))

	// echo v4: the handler is the argument after the path; trailing variadic
	// arguments are middleware of a different type.
	add(row(Echo, pkgEcho, nil, []string{"New"}, OpNew))
	add(verbRoutes(Echo, pkgEcho, echoRecv, []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE"},
		with(row("", nil, nil, nil, 0), func(r *RouterCall) { r.PathArg, r.HandlerArg = 0, 1 }))...)
	add(with(row(Echo, pkgEcho, echoRecv, []string{"Any"}, OpRoute),
		func(r *RouterCall) { r.Method, r.PathArg, r.HandlerArg = "*", 0, 1 }))
	add(with(row(Echo, pkgEcho, echoRecv, []string{"Add"}, OpRoute),
		func(r *RouterCall) { r.MethodArg, r.PathArg, r.HandlerArg = 0, 1, 2 }))
	add(with(row(Echo, pkgEcho, echoRecv, []string{"Match"}, OpRoute),
		func(r *RouterCall) { r.MethodsArg, r.PathArg, r.HandlerArg = 0, 1, 2 }))
	add(with(row(Echo, pkgEcho, echoRecv, []string{"Group"}, OpGroup), func(r *RouterCall) { r.PathArg = 0 }))

	// gorilla/mux: a *Route carries path and methods through a fluent chain.
	add(row(Gorilla, pkgGorilla, nil, []string{"NewRouter"}, OpNew))
	add(with(row(Gorilla, pkgGorilla, gorillaRtr, []string{"HandleFunc", "Handle"}, OpRoute),
		func(r *RouterCall) { r.Method, r.PathArg, r.HandlerArg, r.RouteChain = "*", 0, 1, true }))
	add(with(row(Gorilla, pkgGorilla, gorillaRoute, []string{"HandlerFunc", "Handler"}, OpRoute),
		func(r *RouterCall) { r.Method, r.HandlerArg, r.RouteChain = "*", 0, true }))
	add(with(row(Gorilla, pkgGorilla, append(gorillaRtr, gorillaRoute...), []string{"PathPrefix", "Path"}, OpGroup),
		func(r *RouterCall) { r.PathArg = 0 }))
	add(with(row(Gorilla, pkgGorilla, append(gorillaRtr, gorillaRoute...), []string{"Methods"}, OpSame),
		func(r *RouterCall) { r.MethodsArg = 0 }))
	add(row(Gorilla, pkgGorilla, append(gorillaRtr, gorillaRoute...),
		[]string{"NewRoute", "Subrouter", "Schemes", "Host", "Headers", "Queries", "Name", "MatcherFunc"}, OpSame))
	return t
}()

// RequestTypes are the handler parameter types that carry request data — the
// HttpRoute.request_params a handler gets (§2.2: net/http `r` -> [1], gin and
// echo `c` -> [0]).
var RequestTypes = []struct {
	Pkgs []string
	Name string
}{
	{pkgNetHTTP, "Request"},
	{pkgGin, "Context"},
	{pkgEcho, "Context"},
}

// JoinSlash reports whether a framework joins a group prefix and a relative
// path with path.Join semantics (gin's joinPaths): Group("/v1").GET("users")
// is /v1/users. The others concatenate as written: echo prefixes "/" only to
// the whole path, chi patterns must begin with "/", gorilla templates and
// net/http StripPrefix are plain strings.
func JoinSlash(framework string) bool { return framework == Gin }

// HandlerShapes are the handler function types of the routers, written as
// go/types prints them: net/http's func(w, r), gin's func(*Context), echo's
// func(Context) error. A middleware or wrapper argument is followed to its
// handler only when it has one of these shapes or implements http.Handler
// (review item 1): `makeHandler(encodeJSON)` takes a func(w, v any), which is
// a dependency, not the handler — the closure makeHandler returns is.
var HandlerShapes = []string{
	"func(w net/http.ResponseWriter, r *net/http.Request)",
	"func(c *github.com/gin-gonic/gin.Context)",
	"func(c github.com/labstack/echo/v4.Context) error",
}

// IsHandlerShaped reports whether t is a handler function type (named or
// not, parameter names ignored) or implements handler (net/http.Handler;
// nil skips that test).
func IsHandlerShaped(t types.Type, handler *types.Interface) bool {
	if sig, ok := t.Underlying().(*types.Signature); ok {
		got := signatureShape(sig)
		for _, h := range handlerShapes {
			if got == h {
				return true
			}
		}
		return false
	}
	return handler != nil && types.Implements(t, handler)
}

// handlerShapes are HandlerShapes with the parameter names dropped.
var handlerShapes = func() []string {
	out := make([]string, len(HandlerShapes))
	for i, h := range HandlerShapes {
		out[i] = stripParamNames(h)
	}
	return out
}()

func signatureShape(sig *types.Signature) string {
	var b strings.Builder
	b.WriteString("func(")
	for i := 0; i < sig.Params().Len(); i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(types.TypeString(sig.Params().At(i).Type(), nil))
	}
	b.WriteString(")")
	switch n := sig.Results().Len(); {
	case n == 1:
		b.WriteString(" " + types.TypeString(sig.Results().At(0).Type(), nil))
	case n > 1:
		b.WriteString(" (…)")
	}
	return b.String()
}

// stripParamNames turns "func(w T, r U) R" into "func(T, U) R".
func stripParamNames(h string) string {
	open, close := strings.IndexByte(h, '('), strings.IndexByte(h, ')')
	params := strings.Split(h[open+1:close], ", ")
	for i, p := range params {
		if sp := strings.IndexByte(p, ' '); sp >= 0 {
			params[i] = p[sp+1:]
		}
	}
	return h[:open+1] + strings.Join(params, ", ") + h[close:]
}

// IsRequestType reports whether t (or what it points to) is a request type.
func IsRequestType(t types.Type) bool {
	pkg, name := namedOf(t)
	for _, rt := range RequestTypes {
		if rt.Name == name && contains(rt.Pkgs, pkg) {
			return true
		}
	}
	return false
}

// IsRouterType reports whether t (or what it points to) is a router value of
// a known library — a receiver type of RouterCalls — or a net/http.Handler,
// which is how routers travel between functions. It gates the router value
// analysis, so values of every other type cost nothing.
func IsRouterType(t types.Type) bool {
	pkg, name := namedOf(t)
	if pkg == "" {
		return false
	}
	if pkg == "net/http" && name == "Handler" {
		return true
	}
	return routerTypes[pkg+"."+name]
}

var routerTypes = func() map[string]bool {
	m := map[string]bool{}
	for _, rc := range RouterCalls {
		for _, p := range rc.Pkgs {
			for _, r := range rc.Recv {
				m[p+"."+r] = true
			}
		}
	}
	return m
}()

// MatchRouterCall classifies a call against RouterCalls. recv is the receiver
// (nil for a package function) and args the arguments without it.
func MatchRouterCall(cc *ssa.CallCommon) (rc *RouterCall, recv ssa.Value, args []ssa.Value, ok bool) {
	pkg, typ, name, recv, args, ok := callIdentity(cc)
	if !ok {
		return nil, nil, nil, false
	}
	for i := range RouterCalls {
		r := &RouterCalls[i]
		if !contains(r.Names, name) || !contains(r.Pkgs, pkg) {
			continue
		}
		if (typ == "") != (r.Recv == nil) || (typ != "" && !contains(r.Recv, typ)) {
			continue
		}
		return r, recv, args, true
	}
	return nil, nil, nil, false
}

// ---------------------------------------------------------------------------
// §2.3 client sites
// ---------------------------------------------------------------------------

// ClientCall is one HTTP client entry point: where its method, URL and data
// arguments are. Indices never count the receiver.
type ClientCall struct {
	Pkg, Recv, Name string // Recv "" = package function
	Method          string // fixed method; "" = MethodArg
	MethodArg       int
	URLArg          int
	DataArgs        []int // every data-bearing argument (URL, body, form)
}

// ClientCalls is the §2.3 list. The ordinary call site stays, so the
// catalog's egress rules keep firing; these add the synthetic HttpCall site.
var ClientCalls = func() []ClientCall {
	var t []ClientCall
	for _, recv := range []string{"", "Client"} {
		t = append(t,
			ClientCall{"net/http", recv, "Get", "GET", -1, 0, []int{0}},
			ClientCall{"net/http", recv, "Head", "HEAD", -1, 0, []int{0}},
			ClientCall{"net/http", recv, "Post", "POST", -1, 0, []int{0, 2}},
			ClientCall{"net/http", recv, "PostForm", "POST", -1, 0, []int{0, 1}},
		)
	}
	return append(t,
		ClientCall{"net/http", "", "NewRequest", "", 0, 1, []int{1, 2}},
		ClientCall{"net/http", "", "NewRequestWithContext", "", 1, 2, []int{2, 3}},
	)
}()

// MatchClientCall classifies a call against ClientCalls.
func MatchClientCall(cc *ssa.CallCommon) (*ClientCall, []ssa.Value, bool) {
	pkg, typ, name, _, args, ok := callIdentity(cc)
	if !ok {
		return nil, nil, false
	}
	for i := range ClientCalls {
		c := &ClientCalls[i]
		if c.Pkg == pkg && c.Recv == typ && c.Name == name {
			return c, args, true
		}
	}
	return nil, nil, false
}

// ---------------------------------------------------------------------------
// §2.4 Kafka topics
// ---------------------------------------------------------------------------

// Kafka library labels.
const (
	KafkaGo = "kafka-go"
	Sarama  = "sarama"
	Franz   = "franz-go"
)

var (
	pkgKafkaGo = []string{"github.com/segmentio/kafka-go"}
	pkgSarama  = []string{"github.com/IBM/sarama", "github.com/Shopify/sarama"}
	pkgFranz   = []string{"github.com/twmb/franz-go/pkg/kgo"}
)

// MessagePayloads are the payload field of each Kafka message type, on both
// sides (review item 4): a produce writes this field into the topic cell, a
// consume reads it out, and the message's other fields (Topic, Partition,
// Offset, Key, Headers, timestamps) carry no topic data.
var MessagePayloads = []struct {
	Pkgs        []string
	Type, Field string
}{
	{pkgKafkaGo, "Message", "Value"},
	{pkgSarama, "ProducerMessage", "Value"},
	{pkgSarama, "ConsumerMessage", "Value"},
	{pkgFranz, "Record", "Value"},
}

// PayloadField returns the index and name of the payload field of message
// type t (or what it points to).
func PayloadField(t types.Type) (int, string, bool) {
	pkg, name, st := namedStruct(t)
	if st == nil {
		return 0, "", false
	}
	for _, mp := range MessagePayloads {
		if mp.Type != name || !contains(mp.Pkgs, pkg) {
			continue
		}
		for i := 0; i < st.NumFields(); i++ {
			if st.Field(i).Name() == mp.Field {
				return i, mp.Field, true
			}
		}
	}
	return 0, "", false
}

// TopicRole says which side of a topic a call is on.
type TopicRole uint8

const (
	Produce TopicRole = iota
	// Consume: a pull consumer — the call's result is the message.
	Consume
	// ConsumeRecv: the call returns a channel; what is received from it is
	// the message (sarama `pc.Messages()`, `claim.Messages()`).
	ConsumeRecv
	// Bind: binds a callback handler to topics (sarama ConsumerGroup.Consume);
	// the handler's CallbackMethod is a push consumer.
	Bind
	// Each: hands every message of a fetched batch to a callback's first
	// parameter (franz-go `fetches.EachRecord(func(r *kgo.Record){…})`); the
	// batch came from a FromCalls consume on a handle built with Ctors.
	Each
)

// Ctor is a handle constructor whose arguments name the topic: a config
// struct literal's Field / ListField (kafka-go NewReader(ReaderConfig{..})),
// a string argument (sarama ConsumePartition), or option calls in a variadic
// argument (franz-go NewClient(kgo.ConsumeTopics(..))).
type Ctor struct {
	Pkgs      []string
	Recv      string // "" = package function
	Name      string
	Arg       int
	Field     string // Arg is a config struct literal; the topic is this field
	ListField string // ... or every element of this []string field
	Options   []string
	// Options: Arg is a variadic option list; these package functions' string
	// arguments (all of them, variadic included) are the topics.
}

// TopicCall is one row of the §2.4 table. Indices never count the receiver.
type TopicCall struct {
	Lib   string
	Pkgs  []string
	Recv  string
	Names []string
	Role  TopicRole

	// Produce: the payload argument; PayloadElems = it is a slice (variadic
	// or not) of messages.
	PayloadArg   int
	PayloadElems bool
	// Topic, in the order tried: the handle's own literal field (HandleField
	// on the receiver's literal, or on its constructor), then the payload
	// message's literal field (MessageField).
	HandleField  string
	Ctors        []Ctor
	MessageField string

	// Bind: the []string topics argument, the handler argument, the handler
	// method that consumes, and the call inside it whose received values are
	// the messages.
	TopicsArg      int
	HandlerArg     int
	CallbackMethod string
	CallbackRecv   [2]string // {receiver type, method} of the channel source

	// HandleDecisive: a resolved handle topic wins over the messages' — in
	// kafka-go Writer.Topic and Message.Topic are mutually exclusive, so a
	// writer that names its topic is decisive even when the message is a
	// parameter (review item 2). franz-go is the other way round: a
	// Record.Topic overrides the client's DefaultProduceTopic.
	HandleDecisive bool

	// Each: the callback argument, and the consume calls (on Recv's
	// receiver type FromRecv) that produced the batch.
	CallbackArg int
	FromRecv    string
	FromCalls   []string
}

// TopicCalls is the §2.4 table (shapes per research/go-messaging-rpc.md §3.1).
var TopicCalls = []TopicCall{
	// kafka-go: the topic is on the Writer (literal or NewWriter config) or,
	// when the writer has none, on each Message.
	{Lib: KafkaGo, Pkgs: pkgKafkaGo, Recv: "Writer", Names: []string{"WriteMessages"}, Role: Produce,
		PayloadArg: 1, PayloadElems: true, HandleField: "Topic", MessageField: "Topic", HandleDecisive: true,
		Ctors: []Ctor{{Pkgs: pkgKafkaGo, Name: "NewWriter", Arg: 0, Field: "Topic"}}},
	{Lib: KafkaGo, Pkgs: pkgKafkaGo, Recv: "Reader", Names: []string{"ReadMessage", "FetchMessage"}, Role: Consume,
		Ctors: []Ctor{{Pkgs: pkgKafkaGo, Name: "NewReader", Arg: 0, Field: "Topic", ListField: "GroupTopics"}}},

	// sarama (IBM, and the pre-rename Shopify path).
	{Lib: Sarama, Pkgs: pkgSarama, Recv: "SyncProducer", Names: []string{"SendMessage"}, Role: Produce,
		PayloadArg: 0, MessageField: "Topic"},
	{Lib: Sarama, Pkgs: pkgSarama, Recv: "SyncProducer", Names: []string{"SendMessages"}, Role: Produce,
		PayloadArg: 0, PayloadElems: true, MessageField: "Topic"},
	{Lib: Sarama, Pkgs: pkgSarama, Recv: "PartitionConsumer", Names: []string{"Messages"}, Role: ConsumeRecv,
		Ctors: []Ctor{{Pkgs: pkgSarama, Recv: "Consumer", Name: "ConsumePartition", Arg: 0}}},
	{Lib: Sarama, Pkgs: pkgSarama, Recv: "ConsumerGroup", Names: []string{"Consume"}, Role: Bind,
		TopicsArg: 1, HandlerArg: 2, CallbackMethod: "ConsumeClaim",
		CallbackRecv: [2]string{"ConsumerGroupClaim", "Messages"}},

	// franz-go: the topic is on each Record, or the client's
	// DefaultProduceTopic; consumers name topics in ConsumeTopics.
	{Lib: Franz, Pkgs: pkgFranz, Recv: "Client", Names: []string{"Produce", "TryProduce"}, Role: Produce,
		PayloadArg: 1, MessageField: "Topic",
		Ctors: []Ctor{{Pkgs: pkgFranz, Name: "NewClient", Arg: 0, Options: []string{"DefaultProduceTopic"}}}},
	{Lib: Franz, Pkgs: pkgFranz, Recv: "Client", Names: []string{"ProduceSync"}, Role: Produce,
		PayloadArg: 1, PayloadElems: true, MessageField: "Topic",
		Ctors: []Ctor{{Pkgs: pkgFranz, Name: "NewClient", Arg: 0, Options: []string{"DefaultProduceTopic"}}}},
	{Lib: Franz, Pkgs: pkgFranz, Recv: "Client", Names: []string{"PollFetches", "PollRecords"}, Role: Consume,
		Ctors: []Ctor{{Pkgs: pkgFranz, Name: "NewClient", Arg: 0, Options: []string{"ConsumeTopics"}}}},
	{Lib: Franz, Pkgs: pkgFranz, Recv: "Fetches", Names: []string{"EachRecord"}, Role: Each,
		CallbackArg: 0, FromRecv: "Client", FromCalls: []string{"PollFetches", "PollRecords"},
		Ctors: []Ctor{{Pkgs: pkgFranz, Name: "NewClient", Arg: 0, Options: []string{"ConsumeTopics"}}}},
}

// AsyncInputs are the channel-returning producer calls: a send on the
// returned channel is the produce (sarama `producer.Input() <- msg`).
var AsyncInputs = []TopicCall{
	{Lib: Sarama, Pkgs: pkgSarama, Recv: "AsyncProducer", Names: []string{"Input"}, Role: Produce, MessageField: "Topic"},
}

// MatchTopicCall classifies a call against table.
func MatchTopicCall(table []TopicCall, cc *ssa.CallCommon) (*TopicCall, ssa.Value, []ssa.Value, bool) {
	pkg, typ, name, recv, args, ok := callIdentity(cc)
	if !ok {
		return nil, nil, nil, false
	}
	for i := range table {
		tc := &table[i]
		if tc.Recv == typ && contains(tc.Names, name) && contains(tc.Pkgs, pkg) {
			return tc, recv, args, true
		}
	}
	return nil, nil, nil, false
}

// MatchCallee reports whether cc calls name on a receiver of type recv ("" =
// a package function) in one of pkgs.
func MatchCallee(cc *ssa.CallCommon, pkgs []string, recv, name string) bool {
	pkg, typ, n, _, _, ok := callIdentity(cc)
	return ok && n == name && typ == recv && contains(pkgs, pkg)
}

// IsKafkaLibPkg reports whether pkg is one of the Kafka libraries — a field
// read on one of their types is a handle, not a config struct (census).
func IsKafkaLibPkg(pkg string) bool {
	return contains(pkgKafkaGo, pkg) || contains(pkgSarama, pkg) || contains(pkgFranz, pkg)
}

// ---------------------------------------------------------------------------
// shared
// ---------------------------------------------------------------------------

// callIdentity names the callee of cc as (import path, receiver type name,
// method name) with the receiver split off the arguments. A static method
// call on a concrete type and an interface invoke both yield the declared
// receiver type — `(*chi.Mux).Get` gives "Mux", `(chi.Router).Get` "Router",
// and gin's promoted `engine.GET` the declaring "RouterGroup".
func callIdentity(cc *ssa.CallCommon) (pkg, typ, name string, recv ssa.Value, args []ssa.Value, ok bool) {
	if cc.IsInvoke() {
		if cc.Method == nil || cc.Method.Pkg() == nil {
			return
		}
		pkg, typ = namedOf(cc.Value.Type())
		if pkg == "" {
			return
		}
		return pkg, typ, cc.Method.Name(), cc.Value, cc.Args, true
	}
	sc := cc.StaticCallee()
	if sc == nil {
		return
	}
	if obj, isFn := sc.Object().(*types.Func); isFn && obj != nil {
		sig := obj.Type().(*types.Signature)
		if sig.Recv() != nil {
			p, t := namedOf(sig.Recv().Type())
			if p == "" || len(cc.Args) == 0 {
				return
			}
			return p, t, obj.Name(), cc.Args[0], cc.Args[1:], true
		}
		if obj.Pkg() == nil {
			return
		}
		return obj.Pkg().Path(), "", obj.Name(), nil, cc.Args, true
	}
	return
}

// namedOf names t (pointer stripped) as (import path, type name).
func namedOf(t types.Type) (string, string) {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", ""
	}
	return n.Obj().Pkg().Path(), n.Obj().Name()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

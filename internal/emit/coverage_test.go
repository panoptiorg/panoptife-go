package emit

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/flow"
	"github.com/panoptiorg/panoptife-go/internal/frameworks"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Coverage wave 1 (§2.1–§2.4) at the level the core sees: the CGF written by
// Run, and the census lines on stderr.

// runCGF runs the extraction and returns the decoded packages by path and
// everything Run printed to stderr.
func runCGF(t *testing.T, o Options) (map[string]*pb.CgfPackage, string) {
	t.Helper()
	if o.Scope == "" {
		o.Scope = "./..."
	}
	if o.OutDir == "" {
		o.OutDir = t.TempDir()
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	runErr := Run(o)
	os.Stderr = old
	w.Close()
	stderr := <-done
	if runErr != nil {
		t.Fatalf("Run(%s): %v\n%s", o.RepoDir, runErr, stderr)
	}
	return readCGF(t, o.OutDir), stderr
}

func readCGF(t *testing.T, dir string) map[string]*pb.CgfPackage {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.pb"))
	out := map[string]*pb.CgfPackage{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cp pb.CgfPackage
		if err := proto.Unmarshal(b, &cp); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[cp.PackagePath] = &cp
	}
	return out
}

func line(t *testing.T, stderr, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %q line in stderr:\n%s", prefix, stderr)
	return ""
}

func fnByFqn(pkgs map[string]*pb.CgfPackage, fqn string) *pb.Function {
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			if f.Fqn == fqn {
				return f
			}
		}
	}
	return nil
}

// §2.2: routes reach the CGF as Endpoint{HTTP} + HttpRoute in the handler's
// package, with the handler bound to the contract — and, by default, no
// whole-request source_params (E3).
func TestHTTPRoutesEmitted(t *testing.T) {
	pkgs, stderr := runCGF(t, Options{RepoDir: "../../fixtures/httpapi"})
	if got, want := line(t, stderr, "http-routes:"),
		"http-routes: 19 routes (chi=6 echo=2 gin=3 gorilla=4 net/http=4) handlers_unresolved=0 prefixes_unresolved=0 eval_budget=0"; got != want {
		t.Errorf("census:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(stderr, "NO CONTRACTS") {
		t.Error("a repo with HTTP routes must not get the no-contracts hint")
	}
	nRoutes, nEndpoints := 0, 0
	for _, cp := range pkgs {
		nRoutes += len(cp.HttpRoutes)
		for _, e := range cp.Endpoints {
			if e.Kind == pb.Endpoint_HTTP {
				nEndpoints++
				if !e.UntrustedInput {
					t.Errorf("endpoint %s: untrusted_input must be set", e.Name)
				}
			}
		}
		for _, r := range cp.HttpRoutes {
			if !bytes.Equal(r.Iid, r.EndpointIid) || !hasEndpoint(cp, r.Iid) {
				t.Errorf("route %s %s: endpoint_iid / Endpoint missing in its package", r.Method, r.Path)
			}
		}
	}
	if nRoutes != 19 || nEndpoints != 19 {
		t.Errorf("routes=%d endpoints=%d, want 19/19", nRoutes, nEndpoints)
	}

	create := fnByFqn(pkgs, "(*example.com/httpapi/stdapi.Server).createUser")
	iid := hash.ContractIID(frameworks.ContractName("POST", "/api/users"))
	if create == nil || len(create.BindsTo) != 1 || !bytes.Equal(create.BindsTo[0], iid) {
		t.Fatalf("createUser must bind exactly http:POST /api/users, got %+v", create)
	}
	if len(create.SourceParams) != 0 {
		t.Errorf("createUser source_params = %v, want none without --http-seed-request", create.SourceParams)
	}
	var route *pb.HttpRoute
	for _, r := range pkgs["example.com/httpapi/stdapi"].HttpRoutes {
		if bytes.Equal(r.Iid, iid) {
			route = r
		}
	}
	if route == nil || route.Method != "POST" || route.Path != "/api/users" || route.Display != "/api/users" ||
		route.Framework != "net/http" || !bytes.Equal(route.HandlerIid, create.Id.Iid) ||
		len(route.RequestParams) != 1 || route.RequestParams[0] != 1 {
		t.Errorf("POST /api/users route = %+v", route)
	}

	// --http-seed-request: the request params become sources (gin's c is 0)
	pkgs, _ = runCGF(t, Options{RepoDir: "../../fixtures/httpapi", HTTPSeedRequest: true})
	for fqn, want := range map[string]uint32{
		"(*example.com/httpapi/stdapi.Server).createUser": 1,
		"(example.com/httpapi/ginapi.itemHandler).create": 0,
	} {
		f := fnByFqn(pkgs, fqn)
		if f == nil || len(f.SourceParams) != 1 || f.SourceParams[0] != want {
			t.Errorf("--http-seed-request: %s source_params = %v, want [%d]", fqn, f.GetSourceParams(), want)
		}
	}

	// --http-routes=false: no trace of any of it
	pkgs, stderr = runCGF(t, Options{RepoDir: "../../fixtures/httpapi", NoHTTPRoutes: true})
	for _, cp := range pkgs {
		if len(cp.HttpRoutes) > 0 || len(cp.Endpoints) > 0 {
			t.Errorf("--http-routes=false: %s still has routes/endpoints", cp.PackagePath)
		}
	}
	if strings.Contains(stderr, "http-routes:") {
		t.Error("--http-routes=false must not print the census")
	}
}

// §2.1: `r.Body` is a `read:` site whose result port feeds the decoder, after
// every real site; with the flag off it is gone (the E2 A/B).
func TestSurfaceReadSite(t *testing.T) {
	const fqn = "(*example.com/httpapi/stdapi.Server).createUser"
	pkgs, _ := runCGF(t, Options{RepoDir: "../../fixtures/httpapi"})
	f := fnByFqn(pkgs, fqn)
	var read *pb.CallSite
	decoderArg := map[uint32]bool{}
	for _, cs := range f.Flow.Callsites {
		if cs.CalleeFqn == "read:net/http.Request.Body" {
			read = cs
		}
	}
	if read == nil {
		t.Fatalf("%s: no read:net/http.Request.Body site", fqn)
	}
	if read.Kind != pb.CallSite_STATIC || !read.Opaque || read.Argc != 0 || read.Resultc != 1 {
		t.Errorf("read site = %+v, want STATIC opaque argc 0 resultc 1", read)
	}
	if int(read.Id) != len(f.Flow.Callsites)-1 {
		t.Errorf("read site id %d: synthetic sites go after every real one", read.Id)
	}
	var port uint32
	for _, v := range f.Flow.Vertices {
		if v.CallsiteId == read.Id && v.Kind == pb.VertexKind_CALL_RESULT_PORT {
			port = v.Id
		}
		if v.Kind == pb.VertexKind_CALL_ARG_PORT && f.Flow.Callsites[v.CallsiteId].CalleeFqn == "encoding/json.NewDecoder" {
			decoderArg[v.Id] = true
		}
	}
	found := false
	for _, e := range f.Flow.Edges {
		if e.From == port && decoderArg[e.To] {
			found = true
		}
	}
	if !found {
		t.Error("the read result must flow into json.NewDecoder's argument")
	}
	// the base object's edge is kept: Param(r)[Body] still reaches the decoder
	baseEdge := false
	for _, e := range f.Flow.Edges {
		v := f.Flow.Vertices[e.From]
		if v.Kind == pb.VertexKind_IN_PARAM && v.Index == 1 && decoderArg[e.To] {
			baseEdge = true
		}
	}
	if !baseEdge {
		t.Error("the ordinary operand edge from r must be preserved")
	}

	pkgs, _ = runCGF(t, Options{RepoDir: "../../fixtures/httpapi", NoSurfaceReads: true})
	for _, cs := range fnByFqn(pkgs, fqn).Flow.Callsites {
		if strings.HasPrefix(cs.CalleeFqn, "read:") {
			t.Errorf("--surface-reads=false: %s still emitted", cs.CalleeFqn)
		}
	}
}

// §2.3: every client call of fixtures/httpclient, its method and path.
func TestHTTPCallSites(t *testing.T) {
	pkgs, stderr := runCGF(t, Options{RepoDir: "../../fixtures/httpclient"})
	if got, want := line(t, stderr, "http-calls:"),
		"http-calls: 4 sites, resolved_path=4, dynamic_base=3, unknown_method=1, eval_budget=0"; got != want {
		t.Errorf("census:\n got %s\nwant %s", got, want)
	}
	want := map[string]string{
		"example.com/httpclient.CreateUser": `POST /{}/api/users`,
		"example.com/httpclient.GetUser":    `GET /{}/api/users/{}`,
		"example.com/httpclient.Stock":      `POST /v1/stock/reserve`,
		"example.com/httpclient.Search":     ` /{}/api/search/{}`,
	}
	got := map[string]string{}
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			for _, cs := range f.Flow.GetCallsites() {
				hc := cs.HttpCall
				if hc == nil {
					continue
				}
				got[f.Fqn] = hc.Method + " " + hc.Path
				m := hc.Method
				if m == "" {
					m = "*"
				}
				if cs.CalleeFqn != "http:"+m+" "+hc.Path || cs.Argc != 1 || cs.Resultc != 0 || !cs.Opaque {
					t.Errorf("%s: synthetic site = %+v", f.Fqn, cs)
				}
				// the ordinary site stays, so egress rules keep firing
				ordinary := false
				for _, o := range f.Flow.Callsites {
					if strings.HasPrefix(o.CalleeFqn, "net/http.") || strings.HasPrefix(o.CalleeFqn, "(*net/http.Client)") {
						ordinary = true
					}
				}
				if !ordinary {
					t.Errorf("%s: the ordinary client site is gone", f.Fqn)
				}
			}
		}
	}
	for fn, w := range want {
		if got[fn] != w {
			t.Errorf("%s: http_call %q, want %q", fn, got[fn], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("http_call sites in %d functions, want %d: %v", len(got), len(want), got)
	}

	// CreateUser: the URL's base parameter and the body reach port 0; ctx does not
	f := fnByFqn(pkgs, "example.com/httpclient.CreateUser")
	var port uint32
	for _, cs := range f.Flow.Callsites {
		if cs.HttpCall != nil {
			for _, v := range f.Flow.Vertices {
				if v.CallsiteId == cs.Id && v.Kind == pb.VertexKind_CALL_ARG_PORT {
					port = v.Id
				}
			}
		}
	}
	into := map[uint32]bool{}
	for _, e := range f.Flow.Edges {
		if e.To == port {
			v := f.Flow.Vertices[e.From]
			if v.Kind == pb.VertexKind_IN_PARAM {
				into[v.Index] = true
			}
		}
	}
	if !into[1] || into[0] {
		t.Errorf("port 0 sources: %v, want baseURL (1) and not ctx (0)", into)
	}

	pkgs, _ = runCGF(t, Options{RepoDir: "../../fixtures/httpclient", NoHTTPCalls: true})
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			for _, cs := range f.Flow.GetCallsites() {
				if cs.HttpCall != nil || strings.HasPrefix(cs.CalleeFqn, "http:") {
					t.Errorf("--http-calls=false: %s still has %s", f.Fqn, cs.CalleeFqn)
				}
			}
		}
	}
}

// cells returns the topic syms of the IN_GLOBAL (reads) or OUT_FIELD
// (writes) vertices of fn, by sym name.
func cells(f *pb.Function, kind pb.VertexKind) map[string][]byte {
	out := map[string][]byte{}
	for _, v := range f.GetFlow().GetVertices() {
		if v.Kind == kind && strings.HasPrefix(v.SymName, "kafka topic ") {
			out[strings.TrimPrefix(v.SymName, "kafka topic ")] = v.Sym
		}
	}
	return out
}

// §2.4 at the extraction level: the producer's "orders" write and the
// consumer's "orders" read carry the same sym; the decoy pair does not; the
// push consumer is a MESSAGE endpoint. The cross-repo chain is the core's.
func TestTopicCells(t *testing.T) {
	prod, pErr := runCGF(t, Options{RepoDir: "../../fixtures/kafka/producer"})
	cons, cErr := runCGF(t, Options{RepoDir: "../../fixtures/kafka/consumer"})
	if got, want := line(t, pErr, "topics:"),
		"topics: produce=6 (resolved 5, from_param 1, from_config 0, other 0) consume=0 (resolved 0, from_param 0, from_config 0, other 0) cells=3 eval_budget=0"; got != want {
		t.Errorf("producer census:\n got %s\nwant %s", got, want)
	}
	if got, want := line(t, cErr, "topics:"),
		"topics: produce=0 (resolved 0, from_param 0, from_config 0, other 0) consume=4 (resolved 4, from_param 0, from_config 0, other 0) cells=2 eval_budget=0"; got != want {
		t.Errorf("consumer census:\n got %s\nwant %s", got, want)
	}

	const p, c = "example.com/kafka/producer.", "example.com/kafka/consumer."
	place := cells(fnByFqn(prod, p+"PlaceOrder"), pb.VertexKind_OUT_FIELD)
	audit := cells(fnByFqn(prod, p+"Audit"), pb.VertexKind_OUT_FIELD)
	relay := cells(fnByFqn(prod, p+"Relay"), pb.VertexKind_OUT_FIELD)
	orders := cells(fnByFqn(cons, "("+c+"ordersHandler).ConsumeClaim"), pb.VertexKind_IN_GLOBAL)
	auditLog := cells(fnByFqn(cons, "("+c+"auditHandler).ConsumeClaim"), pb.VertexKind_IN_GLOBAL)
	poll := cells(fnByFqn(cons, c+"Poll"), pb.VertexKind_IN_GLOBAL)

	wantSym, _ := flow.TopicCell("orders")
	if !bytes.Equal(place["orders"], wantSym) || !bytes.Equal(orders["orders"], wantSym) {
		t.Errorf("orders: producer %x / consumer %x, want both ContractIID(msg:kafka:orders)", place["orders"], orders["orders"])
	}
	if audit["audit-events"] == nil || auditLog["audit-log"] == nil || poll["audit-log"] == nil {
		t.Fatalf("decoy cells missing: %v %v %v", audit, auditLog, poll)
	}
	if bytes.Equal(audit["audit-events"], auditLog["audit-log"]) {
		t.Error("the decoy topics must not share a cell")
	}
	if _, ok := relay["${env:RELAY_TOPIC}"]; !ok {
		t.Errorf("os.Getenv topic: %v, want the symbol ${env:RELAY_TOPIC}", relay)
	}
	// the other library shapes, against the real APIs: sarama's async
	// Input() send, franz-go's DefaultProduceTopic and ConsumeTopics
	if enq := cells(fnByFqn(prod, p+"Enqueue"), pb.VertexKind_OUT_FIELD); !bytes.Equal(enq["orders"], wantSym) {
		t.Errorf("sarama Input() <- msg: %v, want the orders cell", enq)
	}
	if mir := cells(fnByFqn(prod, p+"Mirror"), pb.VertexKind_OUT_FIELD); !bytes.Equal(mir["audit-events"], audit["audit-events"]) {
		t.Errorf("franz-go DefaultProduceTopic: %v, want the audit-events cell", mir)
	}
	if dr := cells(fnByFqn(cons, c+"Drain"), pb.VertexKind_IN_GLOBAL); !bytes.Equal(dr["orders"], wantSym) {
		t.Errorf("franz-go ConsumeTopics + PollFetches: %v, want the orders cell", dr)
	}
	if got := cells(fnByFqn(prod, p+"Forward"), pb.VertexKind_OUT_FIELD); len(got) != 0 {
		t.Errorf("a parameter topic resolves to no cell (no wildcard), got %v", got)
	}

	// the read reaches the SQL sink: IN_GLOBAL -> Exec's query argument
	cc := fnByFqn(cons, "("+c+"ordersHandler).ConsumeClaim")
	var in uint32
	for _, v := range cc.Flow.Vertices {
		if v.Kind == pb.VertexKind_IN_GLOBAL {
			in = v.Id
		}
	}
	reachesExec := false
	for _, e := range cc.Flow.Edges {
		v := cc.Flow.Vertices[e.To]
		if e.From == in && v.Kind == pb.VertexKind_CALL_ARG_PORT && v.Index == 1 &&
			cc.Flow.Callsites[v.CallsiteId].CalleeFqn == "(*database/sql.DB).Exec" {
			reachesExec = true
		}
	}
	if !reachesExec {
		t.Error("the received message must reach db.Exec's query through the cell read")
	}

	// push consumers: Endpoint{MESSAGE} + binds_to
	var names []string
	for _, e := range cons["example.com/kafka/consumer"].Endpoints {
		if e.Kind == pb.Endpoint_MESSAGE {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "kafka topic audit-log,kafka topic orders" {
		t.Errorf("MESSAGE endpoints = %v", names)
	}
	if !containsIID(cc.BindsTo, wantSym) {
		t.Error("ConsumeClaim must bind the orders topic")
	}

	cons, _ = runCGF(t, Options{RepoDir: "../../fixtures/kafka/consumer", NoTopicCells: true})
	for _, cp := range cons {
		if len(cp.Endpoints) > 0 {
			t.Error("--topic-cells=false: MESSAGE endpoints still emitted")
		}
		for _, f := range cp.Functions {
			if len(cells(f, pb.VertexKind_IN_GLOBAL)) > 0 {
				t.Errorf("--topic-cells=false: %s still reads a topic cell", f.Fqn)
			}
		}
	}
}

// cgstore replay with every new flag on: the synthetic `read:` and `http:`
// sites sit after the real ones, so a snapshot's call-site counts still
// validate and the replayed CGF is byte-identical to the live one.
func TestCgstoreReplayWithCoverageFlags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "httpclient")
	if err := os.CopyFS(repo, os.DirFS("../../fixtures/httpclient")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	store := t.TempDir()
	o := Options{RepoDir: repo, CgStoreDir: store, HTTPSeedRequest: true}
	o.OutDir = t.TempDir()
	_, live := runCGF(t, o)
	if !strings.Contains(live, "dispatch[vta]") {
		t.Fatalf("first run should build the graph live:\n%s", live)
	}
	liveDir := o.OutDir
	o.OutDir = t.TempDir()
	_, replay := runCGF(t, o)
	if !strings.Contains(replay, "dispatch[vta-cached]") || strings.Contains(replay, "rebuilding") {
		t.Fatalf("second run must replay the snapshot:\n%s", replay)
	}
	a, b := readCGF(t, liveDir), readCGF(t, o.OutDir)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("package count %d vs %d", len(a), len(b))
	}
	// The snapshot must have had something to replay in a function that also
	// carries a synthetic site: CreateUser's interface call resolves to the
	// one in-scope encoder, and its http: site sits after it. A replay that
	// lost the row would leave the call unresolved and differ below.
	cu := fnByFqn(a, "example.com/httpclient.CreateUser")
	enc := fnByFqn(a, "(example.com/httpclient.jsonEncoder).encode")
	resolved, synthetic := false, false
	for _, cs := range cu.GetFlow().GetCallsites() {
		if cs.Kind == pb.CallSite_VIRTUAL && len(cs.CalleeIids) == 1 && enc != nil && bytes.Equal(cs.CalleeIids[0], enc.Id.Iid) {
			resolved = true
		}
		if cs.HttpCall != nil {
			synthetic = true
		}
	}
	if !resolved || !synthetic {
		t.Fatalf("fixture drift: CreateUser needs a dispatched call (%v) and an http: site (%v)", resolved, synthetic)
	}
	for path, cp := range a {
		x, _ := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
		y, _ := proto.MarshalOptions{Deterministic: true}.Marshal(b[path])
		if !bytes.Equal(x, y) {
			t.Errorf("%s: replayed CGF differs from the live one", path)
		}
	}
}

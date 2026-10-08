package flow

import (
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// Coverage wave 1 review fixes at the LocalFlow level. testdata/kafkareview
// and testdata/requests port the reviewer's probes.

var kafkaReview struct {
	sync.Once
	ld  *loader.Loaded
	idx *TopicIndex
	err error
}

// buildKafkaReview builds fn of testdata/kafkareview with topic cells on and
// the program's topic index, as emit does.
func buildKafkaReview(t *testing.T, fqn string) *Result {
	t.Helper()
	kafkaReview.Do(func() {
		kafkaReview.ld, kafkaReview.err = loader.Load("testdata/kafkareview", "./...", false, true, false, nil)
		if kafkaReview.err != nil {
			return
		}
		var fns []*ssa.Function
		for fn := range ssautil.AllFunctions(kafkaReview.ld.Prog) {
			if len(fn.Blocks) > 0 && strings.Contains(fn.String(), "example.com/kafkareview.") {
				fns = append(fns, fn)
			}
		}
		sort.Slice(fns, func(i, j int) bool { return fns[i].String() < fns[j].String() })
		kafkaReview.idx = BuildTopicIndex(kafkaReview.ld.Prog, fns)
	})
	if kafkaReview.err != nil {
		t.Fatalf("load: %v", kafkaReview.err)
	}
	for fn := range ssautil.AllFunctions(kafkaReview.ld.Prog) {
		if fn.String() == fqn && fn.Blocks != nil {
			return Build(fn, nil, Opts{FieldPaths: true, ClosureFlow: true, TopicCells: true, Topics: kafkaReview.idx})
		}
	}
	t.Fatalf("function %q not found", fqn)
	return nil
}

// topicVerts returns the cell vertices of kind by topic name.
func topicVerts(res *Result, kind pb.VertexKind) map[string]*pb.FlowVertex {
	out := map[string]*pb.FlowVertex{}
	for _, v := range res.Flow.Vertices {
		if v.Kind == kind && strings.HasPrefix(v.SymName, "kafka topic ") && len(v.FieldPath) == 0 {
			out[strings.TrimPrefix(v.SymName, "kafka topic ")] = v
		}
	}
	return out
}

// execReach reports, for the fn's (*sql.DB).Exec calls in order, whether a
// cell read reaches each one's query argument.
func execReach(res *Result) []bool {
	reads := map[uint32]bool{}
	for _, v := range res.Flow.Vertices {
		if v.Kind == pb.VertexKind_IN_GLOBAL {
			reads[v.Id] = true
		}
	}
	var execs []uint32
	for _, cs := range res.Flow.Callsites {
		if cs.CalleeFqn == "(*database/sql.DB).Exec" {
			execs = append(execs, cs.Id)
		}
	}
	out := make([]bool, len(execs))
	for _, e := range res.Flow.Edges {
		to := res.Flow.Vertices[e.To]
		if !reads[e.From] || to.Kind != pb.VertexKind_CALL_ARG_PORT || to.Index != 1 {
			continue
		}
		for i, cs := range execs {
			if to.CallsiteId == cs {
				out[i] = true
			}
		}
	}
	return out
}

// Item 2: a kafka-go writer that names its topic is decisive — even when
// the message is a parameter, and even when a message names another.
func TestKafkaWriterTopicIsDecisive(t *testing.T) {
	for fn, want := range map[string]string{
		"example.com/kafkareview.Publish": "orders",
		"example.com/kafkareview.Both":    "wtopic",
	} {
		res := buildKafkaReview(t, fn)
		got := topicVerts(res, pb.VertexKind_OUT_FIELD)
		if len(got) != 1 || got[want] == nil {
			t.Errorf("%s: cells %v, want exactly %q", fn, keys(got), want)
		}
		if res.Topics.Produce.Resolved != 1 {
			t.Errorf("%s: produce census %+v, want resolved", fn, res.Topics.Produce)
		}
	}
	// the parameter message reaches the cell through its payload field
	res := buildKafkaReview(t, "example.com/kafkareview.Publish")
	w := topicVerts(res, pb.VertexKind_OUT_FIELD)["orders"]
	reached := false
	for _, e := range res.Flow.Edges {
		v := res.Flow.Vertices[e.From]
		if e.To == w.Id && v.Kind == pb.VertexKind_IN_PARAM && v.Index == 1 &&
			len(v.FieldNames) == 1 && v.FieldNames[0] == "Value" {
			reached = true
		}
	}
	if !reached {
		t.Error("Publish: msg.Value must flow into the orders cell")
	}
}

// Item 4: the cell holds payloads. A consumer's m.Value reads it; m.Topic
// and m.Key do not — for kafka-go, sarama and (item 13) franz-go. On the
// producer side the key does not enter the cell.
func TestTopicCellIsPayloadOnly(t *testing.T) {
	for _, fn := range []string{
		"example.com/kafkareview.Read",
		"(example.com/kafkareview.handler).ConsumeClaim",
		"example.com/kafkareview.Each$1", // item 13: the EachRecord callback
	} {
		res := buildKafkaReview(t, fn)
		if topicVerts(res, pb.VertexKind_IN_GLOBAL)["orders"] == nil {
			t.Errorf("%s: no orders cell read", fn)
			continue
		}
		got := execReach(res)
		if len(got) != 3 || !got[0] || got[1] || got[2] {
			t.Errorf("%s: cell reaches Exec(Value, Topic, Key) = %v, want [true false false]", fn, got)
		}
	}

	res := buildKafkaReview(t, "example.com/kafkareview.Write")
	w := topicVerts(res, pb.VertexKind_OUT_FIELD)["orders"]
	into := map[uint32]bool{}
	for _, e := range res.Flow.Edges {
		if e.To == w.Id {
			if v := res.Flow.Vertices[e.From]; v.Kind == pb.VertexKind_IN_PARAM {
				into[v.Index] = true
			}
		}
	}
	if !into[2] || into[1] {
		t.Errorf("Write: params into the cell %v, want val (2) and not key (1)", into)
	}
}

// Item 13: a franz-go EachRecord callback reads the topics its batch was
// polled for; the binding is not counted twice in the census.
func TestFranzEachRecordCallback(t *testing.T) {
	res := buildKafkaReview(t, "example.com/kafkareview.Each$1")
	if r := topicVerts(res, pb.VertexKind_IN_GLOBAL)["orders"]; r == nil || r.Type != "[]byte" {
		t.Fatalf("EachRecord callback: orders read %+v, want the []byte payload", r)
	}
	if c := res.Topics.Consume.Sites; c != 0 {
		t.Errorf("the callback counted %d consume sites; the PollFetches call is the site", c)
	}
	outer := buildKafkaReview(t, "example.com/kafkareview.Each")
	if c := outer.Topics.Consume; c.Sites != 1 || c.Resolved != 1 {
		t.Errorf("Each: consume census %+v, want one resolved PollFetches site", c)
	}
}

// Item 10: a symbolic topic cannot collide with a literal one.
func TestTopicSymbolsUnambiguous(t *testing.T) {
	env := topicVerts(buildKafkaReview(t, "example.com/kafkareview.Env"), pb.VertexKind_OUT_FIELD)
	lit := topicVerts(buildKafkaReview(t, "example.com/kafkareview.Lit"), pb.VertexKind_OUT_FIELD)
	e, l := env["${env:PFX}-orders"], lit["env:PFX-orders"]
	if e == nil || l == nil {
		t.Fatalf("cells: env %v, literal %v", keys(env), keys(lit))
	}
	if string(e.Sym) == string(l.Sym) {
		t.Error("the env symbol and the literal topic share a cell")
	}
	if sym, _ := TopicCell("${env:PFX}-orders"); string(sym) != string(e.Sym) {
		t.Error("the env cell must be ContractIID(msg:kafka:${env:PFX}-orders)")
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Item 12: a `read:` source is for a request this process RECEIVES. One it
// builds to send — NewRequest*, a literal, its Clone, a response's Request —
// gets none; a parameter, a field of one, an accessor's result and a
// handler's r.WithContext(ctx) keep theirs.
func TestReadSitesSkipOutgoingRequests(t *testing.T) {
	ld, err := loader.Load("testdata/requests", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]int{
		"example.com/requests.Param":        1,
		"example.com/requests.FieldOfParam": 1,
		"example.com/requests.Accessor":     1,
		"example.com/requests.Derived":      1,
		"example.com/requests.Built":        0,
		"example.com/requests.Literal":      0,
		"example.com/requests.Cloned":       0,
		"example.com/requests.FromResponse": 0,
	}
	for fn := range ssautil.AllFunctions(ld.Prog) {
		n, ok := want[fn.String()]
		if !ok || fn.Blocks == nil {
			continue
		}
		res := Build(fn, nil, Opts{FieldPaths: true, ClosureFlow: true, SurfaceReads: true})
		got := 0
		for _, cs := range res.Flow.Callsites {
			if strings.HasPrefix(cs.CalleeFqn, "read:") {
				got++
			}
		}
		if got != n {
			t.Errorf("%s: %d read: sites, want %d", fn, got, n)
		}
		delete(want, fn.String())
	}
	if len(want) != 0 {
		t.Fatalf("functions not found: %v", keys(want))
	}
}

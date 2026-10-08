package flow

import (
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/frameworks"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Coverage wave 1 §2.4: a Kafka topic is a program-wide abstract cell, the
// same kind --heap-slots builds for a struct field, so the core's phase-2
// heap fixpoint joins a producer in one repo to a consumer in another with no
// engine change. A produce writes the payload into an OUT_FIELD vertex
// carrying `sym = ContractIID("msg:kafka:<topic>")`; a consume is an IN_GLOBAL
// vertex with the same sym. These are emitted through cellWrite / cellRead,
// the machinery heapWrite / heapRead use, but they do not need --heap-slots.
//
// Topic resolution is local and deliberately small (constants, struct-literal
// fields, os.Getenv as the symbol env:NAME). There is no wildcard fallback:
// one producer joined to every consumer is a false-positive generator. What
// does not resolve is counted, split by where it came from, to size the
// rungs wave 1 does not build.

// TopicCensus counts Kafka produce/consume sites (pc-fe's `topics:` stderr
// line; not part of the CGF).
type TopicCensus struct {
	Produce, Consume TopicSideCensus
	// Cells is the set of topic symbols emitted; emit counts it program-wide.
	Cells map[string]bool
}

// TopicSideCensus is one side of TopicCensus.
type TopicSideCensus struct {
	Sites      int // recognised produce (consume) sites
	Resolved   int // ... every topic of which resolved to a cell
	FromParam  int // unresolved: a parameter of the enclosing function (an in-house wrapper)
	FromConfig int // unresolved: a field of a struct that is not a Kafka library type
	Other      int // unresolved: anything else, incl. a handle not built in the function
	EvalBudget int // ... of which the string evaluator ran out of budget
}

// Add accumulates o into c.
func (c *TopicSideCensus) Add(o TopicSideCensus) {
	c.Sites += o.Sites
	c.Resolved += o.Resolved
	c.FromParam += o.FromParam
	c.FromConfig += o.FromConfig
	c.Other += o.Other
	c.EvalBudget += o.EvalBudget
}

// count records one site: resolved when every topic resolved, else
// classified by the first unresolved value (nil: not even a value to look
// at — a handle whose construction is not in the function).
func (c *TopicSideCensus) count(resolved bool, unresolved *frameworks.Str) {
	c.Sites++
	switch {
	case resolved:
		c.Resolved++
	case unresolved == nil:
		c.Other++
	default:
		if unresolved.Exhausted() {
			c.EvalBudget++
		}
		switch cause, pkg := unresolved.FirstCause(); {
		case cause == frameworks.CauseParam:
			c.FromParam++
		case cause == frameworks.CauseField && !frameworks.IsKafkaLibPkg(pkg):
			c.FromConfig++
		default:
			c.Other++
		}
	}
}

// Add accumulates o into c.
func (c *TopicCensus) Add(o TopicCensus) {
	c.Produce.Add(o.Produce)
	c.Consume.Add(o.Consume)
	for k := range o.Cells {
		if c.Cells == nil {
			c.Cells = map[string]bool{}
		}
		c.Cells[k] = true
	}
}

// TopicIndex holds the push-style consumers of the program: a callback
// method bound to topics at a registering call elsewhere — sarama
// `group.Consume(ctx, []string{"orders"}, handler{})` binds
// `handler.ConsumeClaim`. Built once, before any function, because the
// binding and the callback are in different functions.
type TopicIndex struct {
	callbacks map[*ssa.Function]*callback
	// Bind is the census of the registering calls (counted as consume sites).
	Bind TopicSideCensus
}

type callback struct {
	topics []string // sorted, deduplicated
	// recv: the messages are what is received from calls of this {receiver
	// type, method} inside the callback (sarama claim.Messages()). param:
	// the message is the callback's first parameter (franz-go EachRecord).
	recv  [2]string
	pkgs  []string
	param bool
}

// Callbacks returns the push consumers and their topics, sorted by function
// name — emit gives each an Endpoint{MESSAGE} per topic.
func (ti *TopicIndex) Callbacks() []CallbackTopics {
	if ti == nil {
		return nil
	}
	out := make([]CallbackTopics, 0, len(ti.callbacks))
	for fn, cb := range ti.callbacks {
		out = append(out, CallbackTopics{Fn: fn, Topics: cb.topics})
	}
	sort.Slice(out, func(i, j int) bool { return hash.FQN(out[i].Fn) < hash.FQN(out[j].Fn) })
	return out
}

// CallbackTopics is one push consumer.
type CallbackTopics struct {
	Fn     *ssa.Function
	Topics []string
}

// TopicCell is the sym of a topic's cell and its human name.
func TopicCell(topic string) ([]byte, string) {
	return hash.ContractIID(frameworks.TopicContractName(topic)), "kafka topic " + topic
}

// BuildTopicIndex scans fns for the Bind and Each rows of
// frameworks.TopicCalls.
func BuildTopicIndex(prog *ssa.Program, fns []*ssa.Function) *TopicIndex {
	ti := &TopicIndex{callbacks: map[*ssa.Function]*callback{}}
	bind := func(cb *ssa.Function, topics []string, c callback) {
		if cb == nil || len(topics) == 0 {
			return
		}
		have := ti.callbacks[cb]
		if have == nil {
			have = &c
			ti.callbacks[cb] = have
		}
		have.topics = mergeTopics(have.topics, topics)
	}
	for _, fn := range fns {
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				call, ok := instr.(ssa.CallInstruction)
				if !ok {
					continue
				}
				tc, recv, args, ok := frameworks.MatchTopicCall(frameworks.TopicCalls, call.Common())
				if !ok {
					continue
				}
				switch tc.Role {
				case frameworks.Bind:
					if tc.TopicsArg >= len(args) || tc.HandlerArg >= len(args) {
						continue
					}
					topics, unresolved := topicList(args[tc.TopicsArg])
					cb := callbackMethod(prog, args[tc.HandlerArg], tc.CallbackMethod)
					ti.Bind.count(unresolved == nil && len(topics) > 0 && cb != nil, unresolved)
					bind(cb, topics, callback{recv: tc.CallbackRecv, pkgs: tc.Pkgs})
				case frameworks.Each:
					// counted at the consume call that fetched the batch
					if tc.CallbackArg >= len(args) {
						continue
					}
					topics, _ := eachTopics(tc, recv)
					bind(callbackFunc(args[tc.CallbackArg]), topics, callback{param: true})
				}
			}
		}
	}
	return ti
}

// eachTopics resolves the topics of a batch handed to an Each callback: the
// batch is the result of a FromCalls consume on a handle built with Ctors.
func eachTopics(tc *frameworks.TopicCall, batch ssa.Value) ([]string, *frameworks.Str) {
	call := frameworks.DefCall(batch)
	if call == nil {
		return nil, nil
	}
	for _, name := range tc.FromCalls {
		if frameworks.MatchCallee(&call.Call, tc.Pkgs, tc.FromRecv, name) {
			var handle ssa.Value
			if call.Call.IsInvoke() {
				handle = call.Call.Value
			} else if len(call.Call.Args) > 0 {
				handle = call.Call.Args[0]
			}
			return handleTopics(tc, handle)
		}
	}
	return nil, nil
}

// callbackFunc: the function a func-typed argument denotes.
func callbackFunc(v ssa.Value) *ssa.Function {
	for i := 0; i < 4; i++ {
		switch x := v.(type) {
		case *ssa.Function:
			return x
		case *ssa.MakeClosure:
			f, _ := x.Fn.(*ssa.Function)
			return f
		case *ssa.ChangeType:
			v = x.X
		default:
			return nil
		}
	}
	return nil
}

// topicList resolves a []string of topics; unresolved is the first element
// that did not resolve, or the slice itself when it is not a literal (nil
// when all resolved).
func topicList(v ssa.Value) ([]string, *frameworks.Str) {
	elems, ok := frameworks.SliceElems(v)
	if !ok {
		s := frameworks.Eval{}.Of(v) // a hole; its cause feeds the census split
		return nil, &s
	}
	var out []string
	for _, e := range elems {
		s := frameworks.Eval{EnvSymbols: true}.Of(e)
		t, ok := s.Symbol()
		if !ok || t == "" {
			return out, &s
		}
		out = append(out, t)
	}
	return out, nil
}

// callbackMethod: the method `name` of the concrete handler a registering
// call is given (the operand of the MakeInterface boxing it).
func callbackMethod(prog *ssa.Program, h ssa.Value, name string) *ssa.Function {
	for i := 0; i < 4; i++ {
		switch x := h.(type) {
		case *ssa.MakeInterface:
			obj, _, _ := types.LookupFieldOrMethod(x.X.Type(), true, nil, name)
			m, ok := obj.(*types.Func)
			if !ok {
				return nil
			}
			return prog.FuncValue(m) // a plain lookup for a declared method
		case *ssa.ChangeInterface:
			h = x.X
		default:
			if lv := frameworks.LocalValue(h); lv != nil {
				h = lv
				continue
			}
			return nil
		}
	}
	return nil
}

func mergeTopics(a, b []string) []string {
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// per-function emission
// ---------------------------------------------------------------------------

// topicWrite / topicRead are pending cell vertices, materialized in
// emitTopicCells after every call site exists (vertex ids stay in
// instruction order).
type topicWrite struct {
	topics []string
	msgs   []ssa.Value // the messages sent (or, unresolvable, the batch)
	pos    token.Pos
}

type topicRead struct {
	topics []string
	msgs   []ssa.Value // the received messages
}

// topicParamHook: an Each callback's first parameter is a received message.
func (b *builder) topicParamHook() {
	cb := b.opts.Topics.callbackOf(b.fn)
	if cb == nil || !cb.param {
		return
	}
	first := 0
	if b.fn.Signature.Recv() != nil {
		first = 1
	}
	if len(b.fn.Params) > first {
		b.topicReads = append(b.topicReads, topicRead{topics: cb.topics, msgs: []ssa.Value{b.fn.Params[first]}})
	}
}

// topicHook classifies one call (from handleCall, after its real site).
func (b *builder) topicHook(instr ssa.CallInstruction) {
	cc := instr.Common()
	if cb := b.opts.Topics.callbackOf(b.fn); cb != nil && !cb.param && frameworks.MatchCallee(cc, cb.pkgs, cb.recv[0], cb.recv[1]) {
		// inside a push consumer: what is received from claim.Messages().
		// Counted once, at the binding call (TopicIndex.Bind).
		b.topicReads = append(b.topicReads, topicRead{topics: cb.topics, msgs: received(instr.Value())})
		return
	}
	tc, recv, args, ok := frameworks.MatchTopicCall(frameworks.TopicCalls, cc)
	if !ok {
		return
	}
	switch tc.Role {
	case frameworks.Produce:
		topics, unresolved := produceTopics(tc, recv, args)
		b.topicC.Produce.count(unresolved == nil && len(topics) > 0, unresolved)
		if len(topics) == 0 || tc.PayloadArg >= len(args) {
			return
		}
		msgs := []ssa.Value{args[tc.PayloadArg]}
		if tc.PayloadElems {
			if elems, ok := frameworks.SliceElems(args[tc.PayloadArg]); ok {
				msgs = elems
			}
		}
		b.topicWrites = append(b.topicWrites, topicWrite{topics: topics, msgs: msgs, pos: instr.Pos()})
	case frameworks.Consume, frameworks.ConsumeRecv:
		topics, unresolved := handleTopics(tc, recv)
		b.topicC.Consume.count(unresolved == nil && len(topics) > 0, unresolved)
		if len(topics) == 0 || instr.Value() == nil {
			return
		}
		var msgs []ssa.Value
		if tc.Role == frameworks.ConsumeRecv {
			msgs = received(instr.Value())
		} else if _, tuple := instr.Value().Type().(*types.Tuple); tuple {
			msgs = extractsAt(instr.Value(), 0)
		} else {
			msgs = []ssa.Value{instr.Value()}
		}
		b.topicReads = append(b.topicReads, topicRead{topics: topics, msgs: msgs})
	}
}

// topicSendHook: a send on the channel an async producer's Input() returned
// (sarama `producer.Input() <- msg`) is a produce of the sent message.
func (b *builder) topicSendHook(s *ssa.Send) {
	c, ok := s.Chan.(*ssa.Call)
	if !ok {
		return
	}
	tc, _, _, ok := frameworks.MatchTopicCall(frameworks.AsyncInputs, &c.Call)
	if !ok {
		return
	}
	topics, unresolved := messageTopics(tc.MessageField, s.X, false)
	b.topicC.Produce.count(unresolved == nil && len(topics) > 0, unresolved)
	if len(topics) > 0 {
		b.topicWrites = append(b.topicWrites, topicWrite{topics: topics, msgs: []ssa.Value{s.X}, pos: s.Pos()})
	}
}

func (ti *TopicIndex) callbackOf(fn *ssa.Function) *callback {
	if ti == nil {
		return nil
	}
	return ti.callbacks[fn]
}

// produceTopics resolves a produce call's topic. kafka-go's writer topic is
// decisive when it resolves (Writer.Topic and Message.Topic are mutually
// exclusive); otherwise each message's own field comes first — franz-go's
// Record.Topic overrides the client's DefaultProduceTopic — then the
// handle's literal field or constructor config.
func produceTopics(tc *frameworks.TopicCall, recv ssa.Value, args []ssa.Value) ([]string, *frameworks.Str) {
	if tc.HandleDecisive {
		if topics, unresolved, found := handleTopicsFound(tc, recv); found && (len(topics) > 0 || unresolved != nil) {
			return topics, unresolved
		}
	}
	if tc.MessageField != "" && tc.PayloadArg < len(args) {
		if topics, unresolved := messageTopics(tc.MessageField, args[tc.PayloadArg], tc.PayloadElems); len(topics) > 0 || unresolved != nil {
			return topics, unresolved
		}
	}
	if tc.HandleDecisive {
		return nil, nil
	}
	topics, unresolved, _ := handleTopicsFound(tc, recv)
	return topics, unresolved
}

// handleTopics resolves the topic a consumer handle was built with.
func handleTopics(tc *frameworks.TopicCall, recv ssa.Value) ([]string, *frameworks.Str) {
	topics, unresolved, _ := handleTopicsFound(tc, recv)
	return topics, unresolved
}

// handleTopicsFound: found reports that the handle's construction was seen
// (a literal or a known constructor), even if it names no topic — kafka-go's
// Writer and Message topics are mutually exclusive, so an empty Writer.Topic
// sends the lookup on to the messages.
func handleTopicsFound(tc *frameworks.TopicCall, recv ssa.Value) ([]string, *frameworks.Str, bool) {
	if recv == nil {
		return nil, nil, false
	}
	if tc.HandleField != "" {
		if vals, ok := frameworks.FieldStores(recv, tc.HandleField); ok {
			t, u := evalTopics(vals)
			return t, u, true
		}
	}
	call := frameworks.DefCall(recv)
	if call == nil {
		return nil, nil, false
	}
	for _, ct := range tc.Ctors {
		if !frameworks.MatchCallee(&call.Call, ct.Pkgs, ct.Recv, ct.Name) {
			continue
		}
		args := call.Call.Args
		if ct.Recv != "" && !call.Call.IsInvoke() {
			args = args[1:]
		}
		if ct.Arg >= len(args) {
			return nil, nil, false
		}
		arg := args[ct.Arg]
		switch {
		case len(ct.Options) > 0:
			t, u := optionTopics(arg, ct)
			return t, u, true
		case ct.Field != "":
			var topics []string
			if vals, ok := frameworks.FieldStores(arg, ct.Field); ok {
				t, u := evalTopics(vals)
				if u != nil {
					return t, u, true
				}
				topics = t
			} else {
				return nil, nil, false
			}
			if ct.ListField != "" {
				if vals, ok := frameworks.FieldStores(arg, ct.ListField); ok {
					for _, v := range vals {
						t, u := topicList(v)
						if u != nil {
							return topics, u, true
						}
						topics = mergeTopics(topics, t)
					}
				}
			}
			return topics, nil, true
		default:
			s := frameworks.Eval{EnvSymbols: true}.Of(arg)
			if t, ok := s.Symbol(); ok && t != "" {
				return []string{t}, nil, true
			}
			return nil, &s, true
		}
	}
	return nil, nil, false
}

// optionTopics: the string arguments of the option calls (kgo.ConsumeTopics
// (..), kgo.DefaultProduceTopic(t)) in a variadic option list.
func optionTopics(opts ssa.Value, ct frameworks.Ctor) ([]string, *frameworks.Str) {
	elems, ok := frameworks.SliceElems(opts)
	if !ok {
		return nil, nil
	}
	var topics []string
	for _, e := range elems {
		c := frameworks.DefCall(e)
		if c == nil {
			continue
		}
		for _, name := range ct.Options {
			if !frameworks.MatchCallee(&c.Call, ct.Pkgs, "", name) {
				continue
			}
			for _, a := range c.Call.Args {
				vals := []ssa.Value{a}
				if _, isSlice := a.Type().Underlying().(*types.Slice); isSlice {
					if vals, ok = frameworks.SliceElems(a); !ok {
						s := frameworks.Unknown(frameworks.CauseOther)
						return topics, &s
					}
				}
				t, u := evalTopics(vals)
				if u != nil {
					return topics, u
				}
				topics = mergeTopics(topics, t)
			}
		}
	}
	return topics, nil
}

// messageTopics: the topic field of the message literal(s) a produce call
// sends — one message, or each element of a slice of them.
func messageTopics(field string, payload ssa.Value, elems bool) ([]string, *frameworks.Str) {
	msgs := []ssa.Value{payload}
	if elems {
		var ok bool
		if msgs, ok = frameworks.SliceElems(payload); !ok {
			return nil, nil
		}
	}
	var topics []string
	for _, m := range msgs {
		vals, ok := frameworks.FieldStores(m, field)
		if !ok {
			// a message handed in by the caller sets its topic there: the
			// census counts it as a parameter
			if _, isParam := m.(*ssa.Parameter); isParam {
				s := frameworks.Unknown(frameworks.CauseParam)
				return topics, &s
			}
			return topics, nil
		}
		t, u := evalTopics(vals)
		if u != nil {
			return topics, u
		}
		topics = mergeTopics(topics, t)
	}
	return topics, nil
}

// evalTopics evaluates the stored topic values; an empty literal is no topic.
func evalTopics(vals []ssa.Value) ([]string, *frameworks.Str) {
	var out []string
	for _, v := range vals {
		s := frameworks.Eval{EnvSymbols: true}.Of(v)
		t, ok := s.Symbol()
		if !ok {
			return out, &s
		}
		if t != "" {
			out = mergeTopics(out, []string{t})
		}
	}
	return out, nil
}

// received: the values received from channel ch — `<-ch` (incl. the
// comma-ok form and `for m := range ch`) and select receive cases.
func received(ch ssa.Value) []ssa.Value {
	if ch == nil || ch.Referrers() == nil {
		return nil
	}
	var out []ssa.Value
	for _, r := range *ch.Referrers() {
		switch r := r.(type) {
		case *ssa.UnOp:
			if r.Op != token.ARROW || r.X != ch {
				continue
			}
			if r.CommaOk {
				out = append(out, extractsAt(r, 0)...)
			} else {
				out = append(out, r)
			}
		case *ssa.Select:
			k := 0
			for _, st := range r.States {
				if st.Dir != types.RecvOnly {
					continue
				}
				if st.Chan == ch {
					// the select tuple is (index, recvOk, r_0, r_1, …)
					out = append(out, extractsAt(r, 2+k)...)
				}
				k++
			}
		}
	}
	return out
}

func extractsAt(tuple ssa.Value, i int) []ssa.Value {
	var out []ssa.Value
	if refs := tuple.Referrers(); refs != nil {
		for _, r := range *refs {
			if ex, ok := r.(*ssa.Extract); ok && ex.Index == i {
				out = append(out, ex)
			}
		}
	}
	return out
}

// payloadNode is a synthetic flow node: the payload field of one message
// value (frameworks.MessagePayloads). A topic cell holds payloads, not
// messages: a write deposits a message's payload, a read hands it back, and
// the message's other fields (Topic, Partition, Offset, Key, Headers) carry
// no cell data (review item 4). SSA has no value for "the payload of m" when
// the code never reads it, so this stands in for one: it only ever serves as
// a key of the builder's flow maps, never as an operand.
type payloadNode struct {
	msg   ssa.Value
	field string
	typ   types.Type
}

func (p *payloadNode) Name() string                  { return p.msg.Name() + "." + p.field }
func (p *payloadNode) String() string                { return p.Name() }
func (p *payloadNode) Type() types.Type              { return p.typ }
func (p *payloadNode) Parent() *ssa.Function         { return p.msg.Parent() }
func (p *payloadNode) Referrers() *[]ssa.Instruction { return nil }
func (p *payloadNode) Pos() token.Pos                { return token.NoPos }

// payloadOf makes msg's payload node and wires it: out of the message (a
// projection — produce) or into it (an injection — consume). With field
// paths off both are whole-value edges. ok=false: msg is not a message type
// of the table.
func (b *builder) payloadOf(msg ssa.Value, into bool) (*payloadNode, bool) {
	idx, name, ok := frameworks.PayloadField(msg.Type())
	if !ok {
		return nil, false
	}
	st, _ := structOf(msg.Type())
	n := &payloadNode{msg: msg, field: name, typ: st.Field(idx).Type()}
	fid, fname, paths := b.projField(msg.Type(), idx)
	switch {
	case into && paths:
		// not addInject: a cell read is a direct read, not heap-derived
		b.succ[n] = append(b.succ[n], edge{to: msg, op: opInject, field: fid, name: fname})
	case into:
		b.addFlow(n, msg, false)
	case paths:
		b.addProj(msg, n, fid, fname)
	default:
		b.addFlow(msg, n, false)
	}
	return n, true
}

// payloadSinks: what a produce of msg deposits in the cell — the payload
// values a local literal stores (`kafka.Message{Value: b}`), else msg's
// payload field through a projection, else (not a known message type, e.g.
// a spread slice) msg itself.
func (b *builder) payloadSinks(msg ssa.Value) []ssa.Value {
	if _, name, ok := frameworks.PayloadField(msg.Type()); ok {
		if vals, lit := frameworks.FieldStores(msg, name); lit {
			return vals
		}
		n, _ := b.payloadOf(msg, false)
		return []ssa.Value{n}
	}
	return []ssa.Value{msg}
}

// emitTopicCells materializes the pending cell vertices. Additive only: no
// call site, so ids and NCallInstrs are untouched.
func (b *builder) emitTopicCells() {
	for _, w := range b.topicWrites {
		for _, t := range w.topics {
			sym, name := TopicCell(t)
			b.noteCell(t)
			for _, m := range w.msgs {
				for _, p := range b.payloadSinks(m) {
					b.cellWrite(sym, name, p, b.posSpan(w.pos))
				}
			}
		}
	}
	for _, r := range b.topicReads {
		for _, t := range r.topics {
			sym, name := TopicCell(t)
			b.noteCell(t)
			for _, m := range r.msgs {
				// the message's payload field, injected into the message:
				// `m.Value` reads it back whole, `m.Topic` does not, and a
				// helper given m sees it at its payload path. A message type
				// with no payload field in the table (franz-go's Fetches
				// batch) is the cell's value as a whole.
				if n, ok := b.payloadOf(m, true); ok {
					b.cellRead(sym, name, n)
				} else {
					b.cellRead(sym, name, m)
				}
			}
		}
	}
}

func (b *builder) noteCell(topic string) {
	if b.topicC.Cells == nil {
		b.topicC.Cells = map[string]bool{}
	}
	b.topicC.Cells[topic] = true
}

// cellRead / cellWrite are the vertex halves of heapRead / heapWrite, shared
// by struct-field cells and topic cells: an IN_GLOBAL in-slot that is a
// source of v, an OUT_FIELD out-slot that val sinks into, both carrying sym.
// The core reads an OUT_FIELD with a sym as Global(sym).
func (b *builder) cellRead(sym []byte, name string, v ssa.Value) uint32 {
	id := b.addVertex(pb.VertexKind_IN_GLOBAL, 0, 0, b.typeStr(v.Type()), nil)
	vx := b.flow.Vertices[id]
	vx.Sym, vx.SymName = sym, name
	b.srcVerts[v] = append(b.srcVerts[v], id)
	return id
}

func (b *builder) cellWrite(sym []byte, name string, val ssa.Value, span *pb.Span) uint32 {
	id := b.addVertex(pb.VertexKind_OUT_FIELD, 0, 0, b.typeStr(val.Type()), span)
	vx := b.flow.Vertices[id]
	vx.Sym, vx.SymName = sym, name
	b.sinkVerts[val] = append(b.sinkVerts[val], id)
	return id
}

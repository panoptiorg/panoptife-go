// Package pool is a miniature of ledger-svc's internal/pkg/lib/epool — the
// blocker debt A1 names. Every structural feature that keeps the real chain
// dark is reproduced, and nothing else:
//
//	Submit          sends into a struct-field channel   (heap cell #1)
//	runBatcher      launched by `go` from New, recv's it, and appends into a
//	                field of a heap object                (heap cell #2)
//	flushOneBatch   `copy`s that field out and sends it on (heap cell #3)
//	runWorker       recv's it and calls a STORED handler   ($bound, W1d)
//
// Producer and consumer are reached by disjoint call paths from the same *Pool:
// runBatcher/runWorker have exactly one creation site each (`go` inside New,
// called at init with an untainted pool), so no call site ever holds both sides
// and no amount of summary composition joins them. That is the property the
// fixture exists to pin.
package pool

type Handler func(items []string) error

type request struct {
	payload string
}

type batch struct {
	requests []request
}

type pending struct {
	requests []request
}

type Pool struct {
	incoming chan request
	batches  chan batch
	pend     *pending
	handler  Handler
	stop     chan struct{}
}

func New(h Handler) *Pool {
	p := &Pool{
		incoming: make(chan request, 8),
		batches:  make(chan batch, 8),
		pend:     &pending{},
		handler:  h,
		stop:     make(chan struct{}),
	}
	go p.runBatcher()
	go p.runWorker()
	return p
}

// Submit is the producer. The send is a bare *ssa.Send — which is not an
// ssa.Value at all, so before W1F scanInstrs' generic arm dropped it outright.
func (p *Pool) Submit(payload string) {
	p.incoming <- request{payload: payload}
}

// SubmitSelect is the same producer inside a `select`, which is how 40 of
// ledger-svc's 52 field-sends are written. Here *ssa.Select IS a value, so the
// generic arm wired every operand into the select tuple — the sent value went
// to the tuple and never to the channel (N7).
func (p *Pool) SubmitSelect(payload string) {
	select {
	case <-p.stop:
	case p.incoming <- request{payload: payload}:
	}
}

// runBatcher: recv from cell #1, accumulate into cell #2. Its only creation
// site is the `go` in New.
func (p *Pool) runBatcher() {
	for {
		select {
		case <-p.stop:
			return
		case req, ok := <-p.incoming:
			if !ok {
				return
			}
			p.pend.requests = append(p.pend.requests, req)
			if len(p.pend.requests) >= 4 {
				p.flushOneBatch()
			}
		}
	}
}

// flushOneBatch: `copy` out of cell #2 and send on cell #3. copy is a builtin,
// so it has no static callee, becomes an opaque call site, and the default leaf
// taints RESULTS only — copy's only result is the element count, so without the
// W1F arm the taint dies right here.
func (p *Pool) flushOneBatch() {
	toSend := make([]request, len(p.pend.requests))
	copy(toSend, p.pend.requests)
	p.pend.requests = p.pend.requests[:0]
	select {
	case <-p.stop:
	case p.batches <- batch{requests: toSend}:
	}
}

// runWorker: range over cell #3 (a plain recv UnOp, which already worked) and
// call the STORED handler — a $bound wrapper, which only W1d made transparent.
func (p *Pool) runWorker() {
	for bat := range p.batches {
		items := make([]string, 0, len(bat.requests))
		for _, req := range bat.requests {
			items = append(items, req.payload)
		}
		_ = p.handler(items)
	}
}

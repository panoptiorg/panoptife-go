// Package app exercises MAP WRITES and CHANNEL SENDS (--container-writes).
//
// `m[k] = v` (*ssa.MapUpdate) and `ch <- v` (*ssa.Send) are not ssa.Values, so
// before --container-writes the frontend's generic arm dropped both: the value
// went into the map or channel and the LocalFlow had no edge saying so. Every
// positive handler below produced ZERO chains before; each produces one after.
// The two *Other handlers are the precision guard: a write into one container
// must not taint a different one.
package app

import (
	"context"
	"strings"

	"example.com/containers/pb"
	"example.com/containers/store"
)

type Implementation struct {
	pb.UnimplementedContainerServer
	db *store.DB
}

// MapLocal: the value is written under a key and read back two lines later.
func (i *Implementation) MapLocal(ctx context.Context, r *pb.ContainerRequest) error {
	m := map[string]string{}
	m["q"] = r.Query
	return i.db.Selectx("SELECT * FROM t WHERE a='" + m["q"] + "'")
}

// MapLiteral: a composite literal is the same MapUpdate in SSA.
func (i *Implementation) MapLiteral(ctx context.Context, r *pb.ContainerRequest) error {
	m := map[string]string{"q": r.Query}
	return i.db.Selectx("SELECT * FROM t WHERE a='" + m["q"] + "'")
}

// MapKeysRange: the dedup-set idiom. Request data lives only in the KEYS, and
// reaches SQL by ranging over them.
func (i *Implementation) MapKeysRange(ctx context.Context, r *pb.ContainerRequest) error {
	seen := map[string]struct{}{}
	for _, id := range r.IDs {
		seen[id] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return i.db.Selectx("SELECT * FROM t WHERE id IN ('" + strings.Join(ids, "','") + "')")
}

type cache struct{ byKey map[string]string }

// MapInField: the map lives in a struct field, so the write and the read are
// two different loads of `&c.byKey` — the write must reach the address.
func (i *Implementation) MapInField(ctx context.Context, r *pb.ContainerRequest) error {
	c := &cache{byKey: map[string]string{}}
	c.byKey["q"] = r.Query
	return i.db.Selectx(c.byKey["q"])
}

// ChanLocal: sent and received in the same function.
func (i *Implementation) ChanLocal(ctx context.Context, r *pb.ContainerRequest) error {
	ch := make(chan string, 1)
	ch <- r.Query
	return i.db.Selectx(<-ch)
}

func consume(db *store.DB, ch chan string) {
	for q := range ch {
		db.Selectx(q)
	}
}

// ChanWorker: the handler sends; a goroutine it started receives and sinks.
func (i *Implementation) ChanWorker(ctx context.Context, r *pb.ContainerRequest) error {
	ch := make(chan string)
	go consume(i.db, ch)
	ch <- r.Query
	close(ch)
	return nil
}

// SelectSend: the send is a select case, not a plain statement.
func (i *Implementation) SelectSend(ctx context.Context, r *pb.ContainerRequest) error {
	ch := make(chan string, 1)
	select {
	case ch <- r.Query:
	case <-ctx.Done():
		return ctx.Err()
	}
	return i.db.Selectx(<-ch)
}

func produce(ch chan string, q string) { ch <- q }

// ChanProducer: a goroutine sends, the handler receives. The send happens in
// the CALLEE, so it needs the callee's channel param to be an out-slot:
// found only with --byref-out.
func (i *Implementation) ChanProducer(ctx context.Context, r *pb.ContainerRequest) error {
	ch := make(chan string, 1)
	go produce(ch, r.Query)
	return i.db.Selectx(<-ch)
}

// MapOther (precision): the request goes into one map, SQL reads another.
func (i *Implementation) MapOther(ctx context.Context, r *pb.ContainerRequest) error {
	tainted := map[string]string{}
	tainted["q"] = r.Query
	clean := map[string]string{"q": "SELECT 1"}
	_ = tainted
	return i.db.Selectx(clean["q"])
}

// ChanOther (precision): the request goes into one channel, SQL reads another.
func (i *Implementation) ChanOther(ctx context.Context, r *pb.ContainerRequest) error {
	tainted := make(chan string, 1)
	clean := make(chan string, 1)
	tainted <- r.Query
	clean <- "SELECT 1"
	return i.db.Selectx(<-clean)
}

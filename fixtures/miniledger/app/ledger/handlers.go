package ledger

import (
	"context"

	"example.com/miniledger/pb"
)

// HandleRawQuery: GetSql (SOURCE) → all-static path → Queryx, plus the
// self-recursive walkDepth → Getx. Control chain: survives --dispatch=off.
func (i *Implementation) HandleRawQuery(ctx context.Context, req *pb.RawQueryRequest) error {
	q := req.GetSql()
	if err := i.walkDepth(3, q); err != nil {
		return err
	}
	_, err := i.raw.SearchAccounts(q)
	return err
}

// HandleGetAccount: GetNumber (SOURCE) → IAccounts.FetchAccount (2 impls,
// conf 0.5) → Getx inside *store.Storage.
func (i *Implementation) HandleGetAccount(ctx context.Context, req *pb.GetAccountRequest) (string, error) {
	return i.accounts.FetchAccount(req.GetNumber())
}

// HandleReserves: GetPortfolio (SOURCE) → capped notifyAll (opaque,
// taint-transparent) → IStore.SaveReserve (union iface, 1 impl) → Execx.
func (i *Implementation) HandleReserves(ctx context.Context, req *pb.CalcReservesRequest) error {
	p := req.GetPortfolio()
	p = i.notifyAll(p)
	return i.store.SaveReserve(p)
}

// HandleEndOfDay: GetDate (SOURCE) → IStep.Run (4 impls, conf 0.25) → each
// step's local-interface storage sink.
func (i *Implementation) HandleEndOfDay(ctx context.Context, req *pb.RunEodRequest) error {
	date := req.GetDate()
	for _, s := range i.steps {
		if err := s.Run(date); err != nil {
			return err
		}
	}
	return nil
}

// HandleRecalc: GetPortfolioId (SOURCE) → ICalc.Recalc (3-member SCC) → Execx.
func (i *Implementation) HandleRecalc(ctx context.Context, req *pb.RecalcRequest) error {
	return i.calc.Recalc(req.GetPortfolioId())
}

// HandleEvent: GetPayload (SOURCE) → enqueue func value (closure, 1 target) →
// handleEventsBatch → Execx via IStore.SaveOsv. Since the generics fix the
// pool.Add route (retryEvent) also carries taint, merging both paths into one
// size>=5 SCC; the chain key is the same either way.
func (i *Implementation) HandleEvent(ctx context.Context, req *pb.PushEventRequest) error {
	return i.enqueue(req.GetPayload())
}

// notifyAll fans the value through the capped INotifier site; the opaque
// default leaf is taint-transparent, so the return stays tainted.
func (i *Implementation) notifyAll(v string) string {
	for _, n := range i.notify {
		v = n.Send(v)
	}
	return v
}

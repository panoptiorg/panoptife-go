// Package ledger is the mini god-object service: acme's Implementation holds
// 26 interface-typed deps and a 35-member IStore union; this one holds the same
// shapes at 1/5 scale, plus the epool-style closure wiring.
package ledger

import (
	"example.com/miniledger/app/endofday"
	"example.com/miniledger/pb"
	"example.com/miniledger/pool"
	"example.com/miniledger/store"
)

// IAccounts is a service-local interface (acme style: app packages re-declare
// narrow views of the store). 2 impls — *store.Storage and the in-memory stub
// in package app — so its dispatch site resolves at confidence 0.5.
type IAccounts interface {
	FetchAccount(number string) (string, error)
}

type Implementation struct {
	pb.UnimplementedLedgerServer
	store    store.IStore
	accounts IAccounts
	calc     ICalc
	steps    []endofday.IStep
	notify   []INotifier
	raw      *store.Storage
	pool     *pool.Pool[string]
	enqueue  func(e string) error
}

func New(st store.IStore, accounts IAccounts, steps []endofday.IStep, notify []INotifier, raw *store.Storage) *Implementation {
	i := &Implementation{store: st, accounts: accounts, steps: steps, notify: notify, raw: raw}
	// acme DI style: the interface field points back at the god-object itself,
	// so calc-method cross-calls become interface dispatch (the 3-member SCC).
	i.calc = i
	i.pool = initEventPool(i)
	// Non-generic func-value path (VTA resolves closures through struct
	// fields): closes the handleEventsBatch→retryEvent→enqueue closure cycle.
	i.enqueue = func(e string) error {
		return i.handleEventsBatch([]string{e})
	}
	return i
}

// initEventPool mirrors acme's epool wiring (ledger-svc service.go:213): a
// closure over the god-object becomes the pool's batch handler, creating the
// dynamic func-value edge that closes the event-retry cycle.
func initEventPool(i *Implementation) *pool.Pool[string] {
	return pool.New(1, func(batch []string) error {
		return i.handleEventsBatch(batch)
	})
}

// Package app wires the mini service. New allocates BOTH IAccounts impls, all
// 4 EOD steps, and all 12 notifiers: VTA is flow-based, so only allocated
// types enter target sets (cf. fixtures/dispatch/app/app.go).
package app

import (
	"example.com/miniledger/app/endofday"
	"example.com/miniledger/app/endofday/accrue"
	"example.com/miniledger/app/endofday/backup"
	"example.com/miniledger/app/endofday/osvreview"
	"example.com/miniledger/app/endofday/overdue"
	"example.com/miniledger/app/ledger"
	"example.com/miniledger/db"
	"example.com/miniledger/store"
)

// memAccounts is the second IAccounts impl (acme: mock/in-memory store).
type memAccounts struct{ data map[string]string }

func (m *memAccounts) FetchAccount(number string) (string, error) {
	return m.data[number], nil
}

func New(useReal bool) *ledger.Implementation {
	st := store.New(db.New("postgres://mini"))
	var accounts ledger.IAccounts
	if useReal {
		accounts = st
	} else {
		accounts = &memAccounts{data: map[string]string{}}
	}
	steps := []endofday.IStep{
		accrue.New(st), osvreview.New(st), overdue.New(st), backup.New(st),
	}
	notify := []ledger.INotifier{
		ledger.Notifier01{}, ledger.Notifier02{}, ledger.Notifier03{},
		ledger.Notifier04{}, ledger.Notifier05{}, ledger.Notifier06{},
		ledger.Notifier07{}, ledger.Notifier08{}, ledger.Notifier09{},
		ledger.Notifier10{}, ledger.Notifier11{}, ledger.Notifier12{},
	}
	return ledger.New(st, accounts, steps, notify, st)
}

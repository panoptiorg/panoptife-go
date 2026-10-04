package store

// Per-table interfaces, all satisfied only by *Storage — the mini version of
// ledger-svc's 35 I*Store interfaces (internal/app/ledger/service.go).

type IAccountsStore interface {
	FetchAccount(number string) (string, error)
	ListAccounts(owner string) ([]string, error)
}

type IAccountsWrite interface {
	SaveAccount(row string) error
	DeleteAccount(number string) error
}

type ISearchStore interface {
	SearchAccounts(filter string) ([]string, error)
	CountAccounts(owner string) (int, error)
}

type IReservesStore interface {
	SaveReserve(row string) error
	GetReserve(id string) (string, error)
}

type IReserveCalc interface {
	RecalcReserve(id string) error
	AccrueReserve(day string) error
}

type IReserveHistory interface {
	ListReserves(day string) ([]string, error)
	ReserveHistory(id string) ([]string, error)
}

type IOsvStore interface {
	SaveOsv(row string) error
	GetOsv(id string) (string, error)
	ListOsvParts(id string) ([]string, error)
}

type IOsvOps interface {
	ReviewOsv(id string) error
	BackupOsv(id string) ([]string, error)
}

// IStore is the union interface the god-object holds (acme: 35 members).
type IStore interface {
	IAccountsStore
	IAccountsWrite
	ISearchStore
	IReservesStore
	IReserveCalc
	IReserveHistory
	IOsvStore
	IOsvOps
}

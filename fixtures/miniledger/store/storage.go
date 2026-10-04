// Package store mirrors acme's one-concrete-type store layer: a single
// Storage struct whose methods (17 here, 822 in ledger-svc) satisfy every
// per-table interface in ifaces.go plus each app package's locally declared
// Storage interface — the VTA SCC enlarger at real scale.
package store

import "example.com/miniledger/db"

type Storage struct{ db *db.DB }

func New(d *db.DB) *Storage { return &Storage{db: d} }

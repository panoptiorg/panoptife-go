// Package app wires the real store.
package app

import (
	"example.com/mockheaders/mockingbird"
	"example.com/mockheaders/pb"
	"example.com/mockheaders/store"
)

// Lookup reads an account through the store interface.
func Lookup(s store.Store, id string) string {
	return mockingbird.Sing(s.Get(id))
}

// Name reads a generated getter.
func Name(a *pb.Account) string { return a.GetName() }

// Main uses the real store.
func Main() string { return Lookup(store.New("acct-"), "42") }

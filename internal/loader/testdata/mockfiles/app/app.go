package app

import "example.com/mockfiles/store"

// Get reads through the store.
func Get(s store.Store) string { return s.Get("1") }

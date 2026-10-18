// Package store is a real package that also holds generated mocks under
// file names that do not follow the `_mock.go` convention.
package store

// Store reads accounts.
type Store interface {
	Get(id string) string
}

// SQL is the real implementation.
type SQL struct{ prefix string }

// Get returns the account name.
func (s *SQL) Get(id string) string { return s.prefix + id }

// New builds the real store.
func New(prefix string) Store { return &SQL{prefix: prefix} }

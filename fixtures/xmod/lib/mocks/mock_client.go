// Package mocks holds test doubles in the layout mockgen writes: a mock type
// per interface. The app imports it, so it is part of the loaded closure, but
// no dispatch target may name it.
package mocks

// MockClient is a double for lib.Client.
type MockClient struct{ calls int }

func NewMockClient() *MockClient { return &MockClient{} }

func (m *MockClient) Do(q string) string { m.calls++; return q }

func (m *MockClient) Close() error { return nil }

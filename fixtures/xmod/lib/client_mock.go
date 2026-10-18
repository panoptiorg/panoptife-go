package lib

// clientMock is a test double that sits in a file named like mockgen output.
// It implements Client, but a dispatch target never names it.
type clientMock struct{ calls int }

func (m *clientMock) Do(q string) string { m.calls++; return q }

func (m *clientMock) Close() error { return nil }

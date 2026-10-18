package lib

// Echoer returns a constant: the summary of its only implementation does not
// carry the argument into the result. Other code could implement it, so a
// caller must not rely on that summary alone.
type Echoer interface {
	Echo(s string) string
}

type plainEcho struct{}

func (plainEcho) Echo(s string) string { return "echo" }

// NewEchoer returns an Echoer.
func NewEchoer() Echoer { return plainEcho{} }

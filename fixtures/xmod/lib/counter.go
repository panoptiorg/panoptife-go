package lib

// Counter implements io.Writer. A call through io.Writer is not a call to it:
// that interface belongs to the standard library, which every library
// implements somewhere, so dispatch does not name Counter for it.
type Counter struct{ n int }

func (c *Counter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}

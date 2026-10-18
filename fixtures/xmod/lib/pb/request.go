// Package pb has a message type with a generated-style getter, in a package
// whose path ends in a pb segment.
package pb

// Request is a message.
type Request struct {
	Name string
}

// GetName is a trivial getter: its body is the field and the nil check.
func (r *Request) GetName() string {
	if r != nil {
		return r.Name
	}
	return ""
}

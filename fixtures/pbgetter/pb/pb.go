// Package pb is generated-style protobuf output, as protoc-gen-go emits it:
// message structs that embed the protoimpl state field, plus trivial getters.
// The point of the fixture: a --scope rule that drops every /pb/ package
// (the common extraction shape) leaves these getters with no SSA body, and
// the getter rule must then recognize them from go/types alone.
package pb

import "google.golang.org/protobuf/runtime/protoimpl"

// Req models a request with two disjoint fields: A carries tainted request
// data, B is clean (a server writes it from the session before dispatch).
type Req struct {
	state protoimpl.MessageState
	A     string
	B     string

	// Choice is a oneof: the message field is the wrapper interface and the
	// getter returns that same interface, so the field's and the result's
	// types are identical — the same projection rule applies.
	Choice isReq_Choice
}

// Trivial generated getters — `return x.F` modulo the generated nil-check.
func (x *Req) GetA() string {
	if x != nil {
		return x.A
	}
	return ""
}

func (x *Req) GetB() string {
	if x != nil {
		return x.B
	}
	return ""
}

type isReq_Choice interface{ isReq_Choice() }

type Req_C struct{ C string }

func (*Req_C) isReq_Choice() {}

func (x *Req) GetChoice() isReq_Choice {
	if x != nil {
		return x.Choice
	}
	return nil
}

// HandRec is a hand-written type in the same pb-path package: NOT generated
// protobuf output, so its getter keeps its call site when the body is absent
// (a hand-written GetX may compute). Its getter is trivial in shape, which is
// exactly why the generated-message check matters.
type HandRec struct{ A string }

func (r *HandRec) GetA() string { return r.A }

// GetH is the identity guard: a getter whose result type differs from the
// same-named field's type keeps its call site even without a body.
type FieldRec struct {
	state protoimpl.MessageState
	H     string
}

func (x *FieldRec) GetH() HandRec { return HandRec{A: x.H} }

package lib

import "os/exec"

// store has an exported constructor but an unexported type, so a caller can
// hold the value without naming the type. lib never converts it to an
// interface, so (*store).Save is reachable only from outside.
type store struct{ tool string }

// Save passes name to a command: name reaches an exec sink.
func (s *store) Save(name string) {
	_ = exec.Command(s.tool, "put", name).Run()
}

// NewStore returns the concrete *store, not an interface.
func NewStore() *store { return &store{tool: "tar"} }

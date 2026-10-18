package lib

import "os/exec"

// Runner is an interface that lib declares and implements itself.
type Runner interface {
	Run(cmd string) string
}

type shell struct{}

// Run executes cmd: cmd reaches an exec sink.
func (s *shell) Run(cmd string) string {
	out, _ := exec.Command("sh", "-c", cmd).Output()
	return string(out)
}

// NewRunner returns a Runner.
func NewRunner() Runner { return &shell{} }

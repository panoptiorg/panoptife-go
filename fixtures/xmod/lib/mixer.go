package lib

import "log"

// Mixer is a concrete exported type with a pointer-receiver method.
type Mixer struct{}

// Mix logs s: s reaches a log sink.
func (m *Mixer) Mix(s string) string {
	log.Println(s)
	return s
}

// NewMixer returns the concrete *Mixer.
func NewMixer() *Mixer { return &Mixer{} }

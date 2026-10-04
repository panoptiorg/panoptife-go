package libwrites

import (
	"encoding/json"
	"strings"
)

func sink(string) {}

type T struct{ Name string }

// library call writing into its receiver
func Builder(q string) {
	var sb strings.Builder
	sb.WriteString(q)
	sink(sb.String())
}

// library call writing through a pointer boxed into `any`
func Unmarshal(b []byte) {
	var t T
	json.Unmarshal(b, &t)
	sink(t.Name)
}

func fill(p *T) { p.Name = "x" }

// in-scope callee: its own summary covers it, no library write-back
func InScope() {
	var t T
	fill(&t)
	sink(t.Name)
}

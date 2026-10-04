package store

import (
	"encoding/json"
	"fmt"
)

// MaskDB / NoteDB are two SAME-CLASS (sqli) sinks distinguishable by callee fqn
// — the tie-break-independence device (HANDOFF trap 4).
type MaskDB struct{}

func (d *MaskDB) Selectx(query string) error { return nil }

type NoteDB struct{}

func (d *NoteDB) Selectx(query string) error { return nil }

// Load is an IN-SCOPE producer whose error comes from an out-of-scope leaf
// (json.Unmarshal, one result, an error). doc 31 §4 claims a leaf-only rule
// reaches this transitively — no summary-side rule needed.
//
// The value result is a constant ON PURPOSE. With the catalog's json.Unmarshal
// propagator the request really does land in v, so returning v["k"] makes
// result 0 request data — and without --error-results-strict the result-tuple
// alias (doc 31 §6a) then carries result 0 to the consumers of err too. That is
// a different mechanism from the one this case isolates (the error leaf).
func Load(q string) (string, error) {
	var v map[string]string
	if err := json.Unmarshal([]byte(q), &v); err != nil {
		return "", err
	}
	_ = v
	return "ok", nil
}

// LoadWrapping is the same shape except its error is BUILT from the request
// string by a wrapper, so it genuinely carries request data. A summary-side rule
// ("drop flows into error returns") would delete this one — which is why doc 31
// §4 puts the rule at the leaf instead.
func LoadWrapping(q string) (string, error) {
	return "", fmt.Errorf("cannot load %s", q)
}

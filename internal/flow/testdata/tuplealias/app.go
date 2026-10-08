// Package app holds three shapes of the port-0 tuple alias (doc 31 §6a): only
// the error consumed, value and error consumed apart, and a FIELD of result 0 —
// the path variant doc 31 says --error-results-strict loses.
package app

import (
	"encoding/json"
	"net/url"
	"strconv"
)

func sink(string)   {}
func sinkErr(error) {}

// ErrOnly: only the error of a 2-result library call is consumed.
func ErrOnly(q string) {
	_, err := json.Marshal(q)
	sinkErr(err)
}

// Both: the value and the error reach different sinks.
func Both(q string) {
	n, err := strconv.Atoi(q)
	sink(strconv.Itoa(n))
	if err != nil {
		sink(err.Error())
	}
}

// Field: a field of result 0 reaches a sink, the error another.
func Field(q string) {
	u, err := url.Parse(q)
	if err != nil {
		sinkErr(err)
		return
	}
	sink(u.Host)
}

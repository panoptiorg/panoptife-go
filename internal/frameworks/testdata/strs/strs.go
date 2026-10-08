// Package strs feeds the string evaluator one shape per function; each
// function passes the value under test to Sink.
package strs

import (
	"fmt"
	"net/url"
	"os"
	"path"
)

func Sink(s string) {}

func Const() { Sink("/api/users") }

func Concat(base, id string) { Sink(base + "/api/users/" + id) }

func Sprintf(base, id string) { Sink(fmt.Sprintf("%s/api/users/%s?n=%d&p=100%%", base, id, 3)) }

func JoinPath(base, id string) {
	u, _ := url.JoinPath(base, "api", "users", id)
	Sink(u)
}

func PathJoin(id string) { Sink(path.Join("/api", "users", id)) }

func PhiSame(b bool) {
	s := "/same"
	if b {
		s = "/same"
	}
	Sink(s)
}

func PhiDiff(b bool) {
	s := "/a"
	if b {
		s = "/b"
	}
	Sink(s)
}

func Env() { Sink(os.Getenv("ORDERS_TOPIC")) }

func EnvConcat() { Sink(os.Getenv("ENV") + ".orders") }

type Config struct{ Topic string }

func FromConfig(c *Config) { Sink(c.Topic) }

func FromParam(topic string) { Sink(topic) }

func Captured() {
	s := "/captured"
	f := func() { Sink(s) }
	f()
}

type Writer struct {
	Topic string
	N     int
}

func UseWriter(w *Writer)      {}
func UseConfig(c Writer)       {}
func UseList(xs []string)      {}
func UseVariadic(xs ...string) {}

func Literal()      { UseWriter(&Writer{Topic: "orders"}) }
func ValueLiteral() { UseConfig(Writer{Topic: "events", N: 1}) }
func ZeroField()    { UseWriter(&Writer{N: 2}) }
func Elems()        { UseList([]string{"a", "b"}) }
func Variadic()     { UseVariadic("x", "y", "z") }
func NoVariadic()   { UseVariadic() }

// Blowup is the review's evaluator stress case (item 6): eight switches each
// appending one of ten values. Unmemoised, the Phi chain is 11^8 paths.
func Blowup(a0, a1, a2, a3, a4, a5, a6, a7 int, p0, p1, p2, p3, p4, p5, p6, p7, p8, p9 string) {
	u := "/search?"
	switch a0 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a1 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a2 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a3 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a4 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a5 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a6 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	switch a7 {
	case 0:
		u += p0
	case 1:
		u += p1
	case 2:
		u += p2
	case 3:
		u += p3
	case 4:
		u += p4
	case 5:
		u += p5
	case 6:
		u += p6
	case 7:
		u += p7
	case 8:
		u += p8
	case 9:
		u += p9
	}
	Sink(u)
}

// LiteralEnv is a topic literally named like a symbol (review item 10).
func LiteralEnv() { Sink("env:ORDERS_TOPIC") }

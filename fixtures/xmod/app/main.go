// Package main is a small HTTP service that calls into the lib module: a
// plain function, a generic function, a standard library function, and
// interface methods that lib implements.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"example.com/xmod/lib"
	"example.com/xmod/lib/pb"
	_ "example.com/xmod/lib/mocks"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", user)
	mux.HandleFunc("GET /do", doHandler)
	mux.HandleFunc("GET /save", saveHandler)
	mux.HandleFunc("GET /run", runHandler)
	mux.HandleFunc("GET /label", labelHandler)
	mux.HandleFunc("GET /write", writeHandler)
	mux.HandleFunc("GET /tag", tagHandler)
	mux.HandleFunc("GET /mix", mixHandler)
	mux.HandleFunc("GET /cache", cacheHandler)
	mux.HandleFunc("GET /echo", echoHandler)
	mux.HandleFunc("GET /funcs", funcHandler)
	mux.HandleFunc("GET /pb", pbHandler)
	_ = http.ListenAndServe(":8080", mux)
}

// user reads a request field and passes it to lib.Fetch, which runs it as SQL.
func user(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	name := lib.Fetch(id)
	upper := strings.ToUpper(name)
	tags := lib.Map([]string{upper}, strings.TrimSpace)
	fmt.Fprintln(w, strings.Join(tags, ","))
}

// doer is narrower than lib.Client.
type doer interface {
	Do(q string) string
}

// doHandler calls through an interface that lib's constructor hides the
// implementation of: lib.New returns lib.Client.
func doHandler(w http.ResponseWriter, r *http.Request) {
	var d doer = lib.New()
	fmt.Fprintln(w, d.Do(r.FormValue("id")))
}

// putter is satisfied by the type lib.NewStore returns.
type putter interface {
	Save(name string)
}

// saveHandler converts a concrete lib type to an app interface.
func saveHandler(w http.ResponseWriter, r *http.Request) {
	var p putter = lib.NewStore()
	p.Save(r.FormValue("name"))
}

// runHandler calls a method of an interface that lib declares.
func runHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, lib.NewRunner().Run(r.FormValue("cmd")))
}

// labeled has eleven implementations in lib, more than a site lists.
type labeled interface {
	Label() string
}

func labelHandler(w http.ResponseWriter, r *http.Request) {
	var l labeled = lib.Pick(len(r.FormValue("n")))
	fmt.Fprintln(w, l.Label())
}

// writeHandler calls Write on a writer that only the standard library
// implements.
func writeHandler(w http.ResponseWriter, r *http.Request) {
	var buf io.Writer = &bytes.Buffer{}
	_, _ = buf.Write([]byte(r.FormValue("x")))
}

// tagger has one declared method in lib, Base.Tag: lib.Base has it with a
// value receiver, lib.Wrapper by embedding.
type tagger interface {
	Tag() string
}

type tagger2 interface {
	Tag() string
}

func tagHandler(w http.ResponseWriter, r *http.Request) {
	var t tagger = lib.NewWrapper(r.FormValue("name"))
	var u tagger2 = lib.NewTagger(r.FormValue("name"))
	fmt.Fprintln(w, t.Tag(), u.Tag())
}

// mixer is implemented by a type of this package and by a type of lib.
type mixer interface {
	Mix(s string) string
}

type localMix struct{}

func (localMix) Mix(s string) string { return strings.ToLower(s) }

func pick(useLib bool) mixer {
	if useLib {
		return lib.NewMixer()
	}
	return localMix{}
}

func mixHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, pick(r.FormValue("lib") != "").Mix(r.FormValue("s")))
}

// getter is implemented by both caches of lib; lib.NewCache returns one.
type getter interface {
	Get(key string) string
}

func cacheHandler(w http.ResponseWriter, r *http.Request) {
	var g getter = lib.NewCache()
	fmt.Fprintln(w, g.Get(r.FormValue("key")))
}

// echoer is implemented in lib by a type whose Echo ignores its argument.
type echoer interface {
	Echo(s string) string
}

// echoHandler passes the result of Echo to a command. The library
// implementation returns a constant, but the interface may hold other types,
// so the argument is still assumed to reach the result.
func echoHandler(w http.ResponseWriter, r *http.Request) {
	var e echoer = lib.NewEchoer()
	_ = exec.Command("echo", e.Echo(r.FormValue("s"))).Run()
}

// logTo writes through an io.Writer that no caller in this module fixes.
func logTo(w io.Writer, s string) {
	_, _ = w.Write([]byte(s))
}

func applyF(f func(string) string, s string) string { return f(s) }

func applyB(f func() string) string { return f() }

func applyT(f func(lib.Base) string, b lib.Base) string { return f(b) }

// funcHandler calls lib through function values: a function, a bound method
// and a method expression. The method expression takes the receiver as its
// first argument, so the call is not a call of the declared method.
func funcHandler(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	base := lib.NewWrapper(name).Base
	fmt.Fprintln(w, applyF(lib.Fetch, name), applyB(base.Tag), applyT(lib.Base.Tag, base))
}

// pbHandler reads a field through a getter of a dependency. The getter is a
// plain field read when its body is built, and a call otherwise.
func pbHandler(w http.ResponseWriter, r *http.Request) {
	req := &pb.Request{Name: r.FormValue("n")}
	fmt.Fprintln(w, req.GetName())
}

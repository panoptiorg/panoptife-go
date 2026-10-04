// Package app exercises LIBRARY CALLS THAT WRITE INTO AN ARGUMENT (catalog
// [[propagators]] + the frontends' library write-back edge). The core's default
// leaf sends a library call's input to its RESULTS only, so before propagators
// every positive handler here produced ZERO chains: the request went into sb,
// buf, &t, … and the engine lost it. The two Clean* handlers are the precision
// guard: a write into one object must not taint a different one.
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strings"
	"text/template"

	"example.com/libwrites/pb"
	"example.com/libwrites/store"
	"example.com/libwrites/thirdparty"
)

type Impl struct {
	pb.UnimplementedLibServer
	db *store.DB
}

type T struct{ Name string }

// strings.Builder: receiver write
func (i *Impl) Builder(ctx context.Context, r *pb.Req) error {
	var sb strings.Builder
	sb.WriteString("SELECT * FROM t WHERE a='")
	sb.WriteString(r.Q)
	return i.db.Selectx(sb.String())
}

// bytes.Buffer: receiver write
func (i *Impl) Buffer(ctx context.Context, r *pb.Req) error {
	var buf bytes.Buffer
	buf.WriteString(r.Q)
	return i.db.Selectx(buf.String())
}

// json.Unmarshal: write through a pointer boxed into `any`
func (i *Impl) Unmarshal(ctx context.Context, r *pb.Req) error {
	var t T
	if err := json.Unmarshal(r.Raw, &t); err != nil {
		return err
	}
	return i.db.Selectx(t.Name)
}

// json.Decoder: the decoder carries the reader's data into Decode's argument
func (i *Impl) Decode(ctx context.Context, r *pb.Req) error {
	var t T
	if err := json.NewDecoder(bytes.NewReader(r.Raw)).Decode(&t); err != nil {
		return err
	}
	return i.db.Selectx(t.Name)
}

// fmt.Fprintf: formatted arguments land in the writer
func (i *Impl) Fprintf(ctx context.Context, r *pb.Req) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "SELECT %s", r.Q)
	return i.db.Selectx(buf.String())
}

// io.Copy: src into dst
func (i *Impl) Copy(ctx context.Context, r *pb.Req) error {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, strings.NewReader(r.Q)); err != nil {
		return err
	}
	return i.db.Selectx(buf.String())
}

// url.Values.Set: a map-typed receiver
func (i *Impl) Values(ctx context.Context, r *pb.Req) error {
	v := url.Values{}
	v.Set("q", r.Q)
	return i.db.Selectx(v.Encode())
}

// maps.Copy: generic, src into dst
func (i *Impl) MapsCopy(ctx context.Context, r *pb.Req) error {
	src := map[string]string{"q": r.Q}
	dst := map[string]string{}
	maps.Copy(dst, src)
	return i.db.Selectx(dst["q"])
}

var tmpl = template.Must(template.New("q").Parse("SELECT {{.}}"))

// template.Execute: data rendered into the writer
func (i *Impl) Template(ctx context.Context, r *pb.Req) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, r.Q); err != nil {
		return err
	}
	return i.db.Selectx(buf.String())
}

// base64 Decode(dst, src): src into dst. The count result is ignored on
// purpose: `dst[:n]` would carry taint through the length n (a slice index is
// an operand), and this case must be explained by the write into dst alone.
func (i *Impl) Base64(ctx context.Context, r *pb.Req) error {
	dst := make([]byte, 64)
	if _, err := base64.StdEncoding.Decode(dst, r.Raw); err != nil {
		return err
	}
	return i.db.Selectx(string(dst))
}

// a dependency the shipped catalog cannot know: found only once a rule for it
// is added — until then it is what `taint --unmodeled` reports
func (i *Impl) ThirdParty(ctx context.Context, r *pb.Req) error {
	var o thirdparty.Out
	thirdparty.Fill(&o, r.Q)
	return i.db.Selectx(o.V)
}

// precision: the request goes into one builder, SQL reads another
func (i *Impl) CleanBuilder(ctx context.Context, r *pb.Req) error {
	var tainted, clean strings.Builder
	tainted.WriteString(r.Q)
	clean.WriteString("SELECT 1")
	return i.db.Selectx(clean.String())
}

// precision: the request is decoded into one struct, SQL reads another
func (i *Impl) CleanUnmarshal(ctx context.Context, r *pb.Req) error {
	var a, b T
	if err := json.Unmarshal(r.Raw, &a); err != nil {
		return err
	}
	b.Name = "SELECT 1"
	return i.db.Selectx(b.Name)
}

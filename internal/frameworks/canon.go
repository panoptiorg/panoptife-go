// Package frameworks holds what pc-fe knows about HTTP routers, HTTP clients
// and Kafka libraries (coverage wave 1 §2): the tables of library shapes, the
// canonical HTTP path every frontend and the core agree on, and the bounded
// SSA string evaluator that recovers route, URL and topic templates.
//
// Vendor names live in the tables of this package, never in conditionals
// scattered through flow or emit: adding a router or a client is a table row.
package frameworks

import "strings"

// Hole is how an unresolved piece of a path is written into a template before
// it is canonicalised (§1.1: "a hole the frontend could not resolve is written
// into the input as the two characters {}").
const Hole = "{}"

// CatchAll is the canonical catch-all segment; it is only legal last.
const CatchAll = "{*}"

// CanonPath maps a route or URL template to the canonical path shared by both
// frontends and the core (coverage wave 1 §1.1, pinned by
// panopticode/testdata/http-canon-vectors.json):
//
//  1. a literal scheme (http://, https://, case-insensitive) or a leading //
//     drops the authority up to the next /;
//  2. ?query and #fragment are cut;
//  3. empty segments are dropped;
//  4. per segment: `{$}` is dropped; `*`, `*name`, `{name...}`, `[...x]`,
//     `[[...x]]` and an already canonical `{*}` are the catch-all `{*}`;
//     anything containing `{` or `[`, or starting with `:`, is the parameter
//     `{}` as a whole (`v{}.json` -> `{}`); else the literal, case kept;
//  5. nothing survives a catch-all.
func CanonPath(s string) string {
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "http://"):
		s = dropAuthority(s[len("http://"):])
	case strings.HasPrefix(low, "https://"):
		s = dropAuthority(s[len("https://"):])
	case strings.HasPrefix(s, "//"):
		s = dropAuthority(s[2:])
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	var segs []string
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			continue
		}
		c := canonSegment(seg)
		if c == "" {
			continue
		}
		segs = append(segs, c)
		if c == CatchAll {
			break
		}
	}
	return "/" + strings.Join(segs, "/")
}

func dropAuthority(s string) string {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[i:]
	}
	return "/"
}

func canonSegment(seg string) string {
	switch {
	case seg == "{$}":
		return ""
	case seg == CatchAll, strings.HasPrefix(seg, "*"):
		return CatchAll
	case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}"):
		return CatchAll
	case (strings.HasPrefix(seg, "[...") || strings.HasPrefix(seg, "[[...")) && strings.HasSuffix(seg, "]"):
		return CatchAll
	case strings.ContainsAny(seg, "{[") || strings.HasPrefix(seg, ":"):
		return Hole
	}
	return seg
}

// ContractName is the HTTP contract key both sides compute (§1):
// `http:<METHOD> <canonical path>`, e.g. `http:GET /api/users/{}`.
func ContractName(method, canonPath string) string {
	return "http:" + method + " " + canonPath
}

// TopicContractName keys a Kafka topic cell (§2.4): `msg:kafka:<topic>`.
func TopicContractName(topic string) string {
	return "msg:kafka:" + topic
}

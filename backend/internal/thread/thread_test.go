// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package thread

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestResolve(t *testing.T) {
	p := Policy{MaxThreadSize: 10}
	cases := []struct {
		name    string
		own     string
		ownSize int
		cands   []Candidate
		want    Plan
		ok      bool
	}{
		{"no candidates", "t_a", 1, nil, Plan{}, false},
		{"only itself", "t_a", 1, []Candidate{{ThreadID: "t_a", Size: 1, Link: LinkTwin}}, Plan{}, false},
		{"single parent", "t_a", 1, []Candidate{{ThreadID: "t_b", Size: 3, Link: LinkInReplyTo}},
			Plan{Canonical: "t_b", Absorb: []string{"t_a"}}, true},
		{"largest wins", "t_a", 4, []Candidate{{ThreadID: "t_b", Size: 2, Link: LinkInReplyTo}, {ThreadID: "t_c", Size: 3, Link: LinkChild}},
			Plan{Canonical: "t_a", Absorb: []string{"t_b", "t_c"}}, true},
		{"tie: the existing thread keeps its id", "t_a", 1, []Candidate{{ThreadID: "t_z", Size: 1, Link: LinkInReplyTo}},
			Plan{Canonical: "t_z", Absorb: []string{"t_a"}}, true},
		{"tie between candidates by id", "t_b", 2, []Candidate{{ThreadID: "t_c", Size: 2, Link: LinkInReplyTo}, {ThreadID: "t_a", Size: 2, Link: LinkReference, Pos: 3}},
			Plan{Canonical: "t_a", Absorb: []string{"t_b", "t_c"}}, true},
		{"absorb order follows the link rank", "t_a", 9, []Candidate{
			{ThreadID: "t_d", Size: 1, Link: LinkChild},
			{ThreadID: "t_c", Size: 1, Link: LinkReference, Pos: 1},
			{ThreadID: "t_b", Size: 1, Link: LinkReference, Pos: 0},
		}, Plan{Canonical: "t_a", Absorb: []string{"t_b"}}, true}, // 9+1 fits, the rest would not
		{"server id wins and local threads join", "t_a", 1, []Candidate{
			{ThreadID: "t_b", Size: 5, Link: LinkInReplyTo},
			{ThreadID: "AAMkConv", Size: 2, Link: LinkReference, Pos: 2},
			{ThreadID: "AAMkOther", Size: 2, Link: LinkReference, Pos: 4},
		}, Plan{Canonical: "AAMkConv", Absorb: []string{"t_a", "t_b"}}, true},
		{"own is a server id", "AAMkConv", 3, []Candidate{{ThreadID: "t_b", Size: 1, Link: LinkChild}}, Plan{}, false},
		{"own does not fit", "t_a", 3, []Candidate{{ThreadID: "t_b", Size: 8, Link: LinkInReplyTo}}, Plan{}, false},
		{"greedy skip of an over-cap candidate", "t_a", 1, []Candidate{
			{ThreadID: "t_b", Size: 6, Link: LinkInReplyTo},
			{ThreadID: "t_c", Size: 4, Link: LinkReference, Pos: 0},
			{ThreadID: "t_d", Size: 2, Link: LinkReference, Pos: 1},
		}, Plan{Canonical: "t_b", Absorb: []string{"t_a", "t_d"}}, true},
		{"dedupe keeps the best link", "t_a", 1, []Candidate{
			{ThreadID: "t_b", Size: 1, Link: LinkChild},
			{ThreadID: "t_b", Size: 1, Link: LinkInReplyTo},
			{ThreadID: "t_c", Size: 1, Link: LinkReference, Pos: 0},
		}, Plan{Canonical: "t_b", Absorb: []string{"t_a", "t_c"}}, true},
		{"no cap", "t_a", 400, []Candidate{{ThreadID: "t_b", Size: 900, Link: LinkTwin}},
			Plan{Canonical: "t_b", Absorb: []string{"t_a"}}, true},
	}
	for _, c := range cases {
		pol := p
		if c.name == "no cap" {
			pol = Policy{}
		}
		got, ok := Resolve(c.own, c.ownSize, c.cands, pol)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Resolve = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeSubject(t *testing.T) {
	cases := map[string]string{
		"Lunch":                          "Lunch",
		"Re: Lunch":                      "Lunch",
		"RE: RE: Lunch":                  "Lunch",
		"Fwd: Re: Lunch":                 "Lunch",
		"Re[2]: Lunch":                   "Lunch",
		"Re(3):Lunch":                    "Lunch",
		"AW: Mittagessen":                "Mittagessen",
		"SV: Lunch":                      "Lunch",
		"[list] Re: Lunch":               "[list] Re: Lunch",
		"Reply: Lunch":                   "Reply: Lunch",
		"Refs: Lunch":                    "Refs: Lunch",
		"Re-check: Lunch":                "Re-check: Lunch",
		"Re:":                            "",
		"Re: ":                           "",
		"Re: Lunch":                      "Lunch",
		"  Re: Lunch  ":                  "Lunch",
		"Re [x]: Lunch":                  "Re [x]: Lunch",
		"Odp: Oběd":                      "Oběd",
		"Odpověď: Oběd":                  "Odpověď: Oběd",
		"Přeposláno: Oběd":               "Přeposláno: Oběd",
		"":                               "",
		strings.Repeat("Re: ", 20) + "x": strings.Repeat("Re: ", 4) + "x",
	}
	for in, want := range cases {
		if got := NormalizeSubject(in); got != want {
			t.Errorf("NormalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
	long := "Re: " + strings.Repeat("x", 2048)
	if got := NormalizeSubject(long); got != long[4:] {
		t.Errorf("long subject: %d bytes", len(got))
	}
}

func FuzzNormalizeSubject(f *testing.F) {
	for _, s := range []string{"Re: x", "Fwd: Re[2]: x", "re:re:re:", "[tag] Re: x", " Re: x", "Re (", "Re[", "Re[1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := NormalizeSubject(s)
		if len(out) > len(s) {
			t.Fatalf("grew: %q -> %q", s, out)
		}
		if out != strings.TrimFunc(out, unicode.IsSpace) {
			t.Fatalf("untrimmed: %q -> %q", s, out)
		}
	})
}

func FuzzResolve(f *testing.F) {
	f.Add("t_a", 1, []byte{1, 0, 2, 3})
	f.Add("AAMk", 2, []byte{0, 0, 0})
	f.Fuzz(func(t *testing.T, own string, ownSize int, data []byte) {
		var cands []Candidate
		for i := 0; i+1 < len(data); i += 2 {
			id := fmt.Sprintf("t_%d", data[i]%5)
			if data[i]%7 == 0 {
				id = fmt.Sprintf("srv%d", data[i]%3)
			}
			cands = append(cands, Candidate{ThreadID: id, Size: int(data[i+1] % 9), Link: Link(data[i] % 4), Pos: int(data[i+1] % 3)})
		}
		plan, ok := Resolve(own, ownSize, cands, Policy{MaxThreadSize: 12, MaxLinkRows: 10})
		if !ok {
			return
		}
		known := map[string]bool{own: true}
		for _, c := range cands {
			known[c.ThreadID] = true
		}
		if !known[plan.Canonical] {
			t.Fatalf("canonical %q unknown", plan.Canonical)
		}
		for _, a := range plan.Absorb {
			if !known[a] || a == plan.Canonical || !IsLocalID(a) {
				t.Fatalf("bad absorb %q in %+v", a, plan)
			}
		}
		if !IsLocalID(own) {
			t.Fatal("a server-threaded message moved")
		}
	})
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEncodeFolding(t *testing.T) {
	ev := &Component{Name: "VEVENT"}
	ev.Add(NewText("SUMMARY", strings.Repeat("Žluťoučký kůň úpěl ďábelské ódy; ", 10)))
	ev.Add(NewProperty("X-ASCII", strings.Repeat("x", 300), Param{Name: "X-A", Values: []string{"a:b", "c,d", "plain", ""}}))
	ev.Add(NewText("DESCRIPTION", "line one\nline two, with a comma"))
	ev.Add(NewProperty("X-EMOJI", strings.Repeat("😀", 60)))
	cal := &Component{Name: "vcalendar", Components: []*Component{ev}}
	cal.Add(NewProperty("VERSION", "2.0"))
	out, err := EncodeComponent(cal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out, []byte("\r\n")) || bytes.Contains(bytes.ReplaceAll(out, []byte("\r\n"), nil), []byte("\n")) {
		t.Errorf("line endings")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line of %d octets: %q", len(line), line)
		}
		if !utf8.ValidString(line) {
			t.Errorf("fold inside a UTF-8 sequence: %q", line)
		}
	}
	if !strings.HasPrefix(string(out), "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n") {
		t.Errorf("order: %q", out[:60])
	}
	if !strings.Contains(string(out), `X-ASCII;X-A="a:b","c,d",plain,:`) {
		t.Errorf("parameter quoting: %q", out)
	}
	o, err := Parse(out)
	if err != nil || o.HasProblems() {
		t.Fatalf("parse back: %v %v", err, o.Problems)
	}
	got := o.Calendar()
	cal.Name = "VCALENDAR"
	if !reflect.DeepEqual(got, cal) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, cal)
	}
	if got.Components[0].Property("DESCRIPTION").Text() != "line one\nline two, with a comma" {
		t.Errorf("text round trip")
	}
}

func TestEncodeRefuses(t *testing.T) {
	bad := []Property{
		{Name: "", Value: "x"},
		{Name: "X_Y", Value: "x"},
		{Name: "BEGIN", Value: "VEVENT"},
		{Name: "end", Value: "VEVENT"},
		{Name: "X", Value: "a\r\nBEGIN:VEVENT"},
		{Name: "X", Value: "a\nb"},
		{Name: "X", Value: "a\x00b"},
		{Name: "X", Value: "a\xffb"},
		{Name: "X", Value: strings.Repeat("x", MaxLineBytes)},
		{Name: "X", Params: []Param{{Name: "Q", Values: []string{`a"b`}}}},
		{Name: "X", Params: []Param{{Name: "Q", Values: []string{"a\x01"}}}},
		{Name: "X", Params: []Param{{Name: "Q", Values: []string{"a\xff"}}}},
		{Name: "X", Params: []Param{{Name: "Q:", Values: []string{"a"}}}},
		{Name: "X", Params: make([]Param, MaxParams+1)},
	}
	for _, p := range bad {
		c := &Component{Name: "VCALENDAR", Props: []Property{p}}
		if _, err := EncodeComponent(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Encode(%+v) = %v", p, err)
		}
	}
	deep := &Component{Name: "VCALENDAR"}
	c := deep
	for range MaxDepth {
		n := &Component{Name: "X"}
		c.Components = []*Component{n}
		c = n
	}
	for _, x := range []*Component{nil, {Name: "V EVENT"}, deep} {
		if _, err := EncodeComponent(x); !errors.Is(err, ErrInvalid) {
			t.Errorf("Encode(%+v) = %v", x, err)
		}
	}
	if out, err := Encode(nil); err != nil || len(out) != 0 {
		t.Errorf("Encode(nil) = %q, %v", out, err)
	}
}

// Parse(Encode(x)) equals x for every clean object of the corpus, and
// encoding is deterministic.
func TestRoundTripCorpus(t *testing.T) {
	for name, data := range corpus(t) {
		o, err := Parse(data)
		if err != nil || o.HasProblems() {
			continue
		}
		a, err := Encode(o)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, err := Parse(a)
		if err != nil || back.HasProblems() || !reflect.DeepEqual(back, o) {
			t.Errorf("%s: round trip differs (%v %v)", name, err, back.Problems)
		}
		b, _ := Encode(back)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: encoding not deterministic", name)
		}
	}
}

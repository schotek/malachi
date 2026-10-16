// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return o
}

func wrap(lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n"
}

func TestGrammar(t *testing.T) {
	o := mustParse(t, wrap(
		`attendee;cn="Doe, Jane";Role=REQ-PARTICIPANT;delegated-to="mailto:a@example.org","mailto:b@example.org";X-EMPTY=;X-Q="":MAILTO:jane@example.org`,
		`DESCRIPTION:a\, b\; c\\ d\nline\Ne:f;g`,
	))
	if o.HasProblems() {
		t.Fatalf("problems %v", o.Problems)
	}
	p := o.Calendar().Props[1]
	want := Property{Name: "ATTENDEE", Params: []Param{
		{Name: "CN", Values: []string{"Doe, Jane"}},
		{Name: "ROLE", Values: []string{"REQ-PARTICIPANT"}},
		{Name: "DELEGATED-TO", Values: []string{"mailto:a@example.org", "mailto:b@example.org"}},
		{Name: "X-EMPTY", Values: []string{""}},
		{Name: "X-Q", Values: []string{""}},
	}, Value: "MAILTO:jane@example.org"}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got %+v\nwant %+v", p, want)
	}
	d := o.Calendar().Props[2]
	if d.Value != `a\, b\; c\\ d\nline\Ne:f;g` {
		t.Errorf("raw value %q", d.Value)
	}
	if got := d.Text(); got != "a, b; c\\ d\nline\ne:f;g" {
		t.Errorf("text %q", got)
	}
}

func TestBadLines(t *testing.T) {
	cases := map[string]string{
		"no colon":             "SUMMARY",
		"space in name":        "SUM MARY:x",
		"empty name":           ":x",
		"name too long":        strings.Repeat("X", 65) + ":x",
		"param without equals": "SUMMARY;FOO:x",
		"param bad name":       "SUMMARY;F_O=1:x",
		"param no value end":   "SUMMARY;A=1",
		"unterminated quote":   `SUMMARY;A="x:y`,
		"text after quote":     `SUMMARY;A="x"y:z`,
		"quote in unquoted":    `SUMMARY;A=x"y:z`,
		"control in param":     "SUMMARY;A=x\x01y:z",
		"control in quoted":    "SUMMARY;A=\"x\x1by\":z",
	}
	for name, line := range cases {
		o := mustParse(t, wrap(line))
		if len(o.Calendar().Props) != 1 || len(o.Problems) != 1 {
			t.Errorf("%s: props %v problems %v", name, o.Calendar().Props, o.Problems)
			continue
		}
		if c := o.Problems[0].Code; c != ProblemBadLine && c != ProblemBadParam {
			t.Errorf("%s: code %s", name, c)
		}
		if o.Problems[0].Line != 3 {
			t.Errorf("%s: line %d", name, o.Problems[0].Line)
		}
	}
	// A 64-character name is fine; tab and non-ASCII are fine in values.
	o := mustParse(t, wrap(strings.Repeat("X", 64)+";A=\"ž\t\":x\ty ž"))
	if o.HasProblems() {
		t.Errorf("problems %v", o.Problems)
	}
}

func TestLineEndingsAndUnfolding(t *testing.T) {
	want := "BEGIN:VCALENDAR\nSUMMARY:Žluťoučký kůň\nEND:VCALENDAR\n"
	split := "BEGIN:VCALENDAR\r\nSUMMARY:\xc5\r\n \xbdlu\r\n\tťoučký kůň\r\nEND:VCALENDAR\r\n"
	for name, in := range map[string]string{
		"crlf":       strings.ReplaceAll(want, "\n", "\r\n"),
		"lf":         want,
		"cr":         strings.ReplaceAll(want, "\n", "\r"),
		"mixed":      "BEGIN:VCALENDAR\rSUMMARY:Žluťoučký kůň\nEND:VCALENDAR\r\n",
		"bom":        "\xef\xbb\xbf" + want,
		"no final":   strings.TrimSuffix(want, "\n"),
		"split utf8": split,
		"blank":      "\r\n\r\nBEGIN:VCALENDAR\r\n\r\nSUMMARY:Žluťoučký\r\r\n  kůň\r\nEND:VCALENDAR\r\n\r\n",
	} {
		o := mustParse(t, in)
		if o.HasProblems() {
			t.Errorf("%s: problems %v", name, o.Problems)
			continue
		}
		if got := o.Calendar().Property("SUMMARY").Text(); got != "Žluťoučký kůň" {
			t.Errorf("%s: summary %q", name, got)
		}
	}
}

func TestCleaning(t *testing.T) {
	o := mustParse(t, wrap("SUM\x00MARY:a\x00b", "DESCRIPTION:\xff\xfex", "COMMENT:bell\x07"))
	if got := problemCodes(o); got != "controlChar invalidUTF8 nulByte" {
		t.Errorf("problems %q", got)
	}
	c := o.Calendar()
	if c.Property("SUMMARY").Value != "ab" || c.Property("DESCRIPTION").Value != "�x" || c.Property("COMMENT").Value != "bell\x07" {
		t.Errorf("props %+v", c.Props)
	}
}

func TestLongLines(t *testing.T) {
	// A line of exactly MaxLineBytes is kept; one more byte, even through
	// folding, drops the property.
	value := strings.Repeat("a", MaxLineBytes-len("SUMMARY:"))
	o := mustParse(t, wrap("SUMMARY:"+value))
	if o.HasProblems() || o.Calendar().Property("SUMMARY").Value != value {
		t.Errorf("64 KiB line: %v", o.Problems)
	}
	o = mustParse(t, wrap("SUMMARY:"+value[:1000], " "+value[1000:]+"b", "LOCATION:x"))
	if problemCodes(o) != ProblemLineTooLong || o.Calendar().Property("SUMMARY") != nil || o.Calendar().Property("LOCATION") == nil {
		t.Errorf("long folded line: %v", o.Problems)
	}
	// long-line-1mib.ics, generated here rather than committed: one line
	// filling the 1 MiB input.
	head, tail := "BEGIN:VCALENDAR\r\nX-MALACHI-TEST:one 1 MiB line\r\nDESCRIPTION:", "\r\nSUMMARY:after\r\nEND:VCALENDAR\r\n"
	big := head + strings.Repeat("x", MaxBytes-len(head)-len(tail)) + tail
	o = mustParse(t, big)
	if problemCodes(o) != ProblemLineTooLong || o.Calendar().Property("SUMMARY").Value != "after" {
		t.Errorf("1 MiB line: %v", o.Problems)
	}
	// too-big.ics, generated: one byte over MaxBytes.
	if _, err := Parse([]byte(big + "x")); !errors.Is(err, ErrTooBig) {
		t.Errorf("too big: %v", err)
	}
	// A 64 KiB line of invalid UTF-8 grows when replaced and is dropped.
	o = mustParse(t, wrap("SUMMARY:"+strings.Repeat("a\xff", (MaxLineBytes-8)/2)))
	if o.Calendar().Property("SUMMARY") != nil {
		t.Errorf("grown line kept")
	}
}

func TestStructure(t *testing.T) {
	cases := []struct {
		name, in, codes, shape string
	}{
		{"nested", wrap("BEGIN:VEVENT", "BEGIN:VALARM", "END:VALARM", "END:VEVENT", "BEGIN:VTODO", "END:VTODO"), "", "VCALENDAR(VEVENT(VALARM) VTODO)"},
		{"lowercase names", "begin:vcalendar\nbegin:vevent \nend:VEVENT\nEND:vcalendar\n", "", "VCALENDAR(VEVENT)"},
		{"missing end", "BEGIN:VCALENDAR\nBEGIN:VEVENT\nBEGIN:VALARM\n", "missingEnd*3", "VCALENDAR(VEVENT(VALARM))"},
		{"mismatched end", wrap("BEGIN:VEVENT", "BEGIN:VALARM", "END:VEVENT"), "mismatchedEnd", "VCALENDAR(VEVENT(VALARM))"},
		{"stray end", wrap("END:VEVENT", "BEGIN:VEVENT", "END:VEVENT"), "strayEnd", "VCALENDAR(VEVENT)"},
		{"stray end outside", "END:VCALENDAR\n" + wrap(), "strayEnd", "VCALENDAR"},
		{"stray property", "METHOD:REQUEST\n" + wrap(), "strayProperty", "VCALENDAR"},
		{"bad begin", wrap("BEGIN:", "BEGIN:V EVENT", "END:"), "badBegin*3", "VCALENDAR"},
		{"no vcalendar", "BEGIN:VEVENT\nUID:1\nEND:VEVENT\nBEGIN:VTODO\nEND:VTODO\n", "noVCalendar", "VCALENDAR(VEVENT VTODO)"},
		{"no begin vcalendar", "BEGIN:VEVENT\nEND:VEVENT\nEND:VCALENDAR\n", "noVCalendar", "VCALENDAR(VEVENT)"},
		{"stray then real", "BEGIN:VEVENT\nEND:VEVENT\n" + wrap("BEGIN:VTODO", "END:VTODO"), "multipleCalendars noVCalendar", "VCALENDAR(VEVENT) VCALENDAR(VTODO)"},
		{"two calendars", wrap() + wrap(), "multipleCalendars", "VCALENDAR VCALENDAR"},
		{"vcalendar inside", "BEGIN:VCALENDAR\nBEGIN:VEVENT\nBEGIN:VCALENDAR\nEND:VCALENDAR\n", "missingEnd*2 multipleCalendars", "VCALENDAR(VEVENT) VCALENDAR"},
		{"empty", "", "empty", ""},
		{"only blank", "\r\n\n\r", "empty", ""},
		{"only garbage", "hello world\n", "badLine empty", ""},
		{"too deep", wrap("BEGIN:A", "BEGIN:B", "BEGIN:C", "BEGIN:D", "X:1", "BEGIN:E", "END:E", "END:D", "END:C", "Y:1", "END:B", "END:A"), "tooDeep", "VCALENDAR(A(B(C)))"},
		{"too deep mismatched", wrap("BEGIN:A", "BEGIN:B", "BEGIN:C", "BEGIN:D", "END:B", "END:A"), "mismatchedEnd tooDeep", "VCALENDAR(A(B(C)))"},
	}
	for _, c := range cases {
		o := mustParse(t, c.in)
		if got := problemCodes(o); got != c.codes {
			t.Errorf("%s: problems %q, want %q", c.name, got, c.codes)
		}
		var parts []string
		for _, cal := range o.Calendars {
			parts = append(parts, shape(cal))
		}
		if got := strings.Join(parts, " "); got != c.shape {
			t.Errorf("%s: shape %q, want %q", c.name, got, c.shape)
		}
	}
	o := mustParse(t, wrap("BEGIN:A", "BEGIN:B", "BEGIN:C", "BEGIN:D", "END:D", "Z:1", "END:C", "END:B", "END:A"))
	c := o.Calendar().Components[0].Components[0].Components[0]
	if !o.Truncated || c.Property("Z") == nil {
		t.Errorf("properties after a dropped deep component: %+v", c)
	}
}

func shape(c *Component) string {
	if len(c.Components) == 0 {
		return c.Name
	}
	var kids []string
	for _, k := range c.Components {
		kids = append(kids, shape(k))
	}
	return c.Name + "(" + strings.Join(kids, " ") + ")"
}

func TestParamLimit(t *testing.T) {
	params := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, ";X-%d=%d", i, i)
		}
		return b.String()
	}
	o := mustParse(t, wrap("SUMMARY"+params(MaxParams)+":x"))
	if o.HasProblems() || len(o.Calendar().Property("SUMMARY").Params) != MaxParams {
		t.Errorf("%d params: %v", MaxParams, o.Problems)
	}
	o = mustParse(t, wrap("SUMMARY"+params(MaxParams+1)+":x"))
	if problemCodes(o) != ProblemTooManyParams || o.Calendar().Property("SUMMARY") != nil || o.Truncated {
		t.Errorf("%d params: %v", MaxParams+1, o.Problems)
	}
}

func TestComponentLimits(t *testing.T) {
	events := func(n int) []string {
		var l []string
		for i := range n {
			l = append(l, "BEGIN:VEVENT", fmt.Sprintf("UID:%d", i), "END:VEVENT")
		}
		return l
	}
	o := mustParse(t, wrap(events(MaxComponents-1)...))
	if o.HasProblems() || len(o.Calendar().Components) != MaxComponents-1 {
		t.Errorf("%d components: %v", MaxComponents, o.Problems)
	}
	o = mustParse(t, wrap(append(events(MaxComponents-1), "BEGIN:VEVENT", "UID:partial", "BEGIN:VALARM")...))
	if !o.Truncated || problemCodes(o) != ProblemTooManyComponents || len(o.Calendar().Components) != MaxComponents-1 {
		t.Errorf("over the cap: %v %d", o.Problems, len(o.Calendar().Components))
	}
	// The component open at the cap is partial and dropped.
	o = mustParse(t, wrap(append(events(MaxComponents-3), "BEGIN:VEVENT", "UID:partial", "BEGIN:VALARM", "END:VALARM", "BEGIN:VALARM")...))
	if !o.Truncated || len(o.Calendar().Components) != MaxComponents-3 {
		t.Errorf("partial component kept: %d", len(o.Calendar().Components))
	}
}

func TestPropertyLimits(t *testing.T) {
	props := func(n int) []string {
		l := make([]string, n)
		for i := range l {
			l[i] = fmt.Sprintf("X-P:%d", i)
		}
		return l
	}
	ev := func(n int, uid string) []string {
		return append(append([]string{"BEGIN:VEVENT", "UID:" + uid}, props(n-1)...), "END:VEVENT")
	}
	o := mustParse(t, wrap(ev(MaxComponentProperties, "full")...))
	if o.HasProblems() || len(o.Calendar().Components[0].Props) != MaxComponentProperties {
		t.Errorf("%d properties: %v", MaxComponentProperties, o.Problems)
	}
	in := append(ev(MaxComponentProperties+1, "over"), "BEGIN:VEVENT", "UID:next", "BEGIN:VALARM", "END:VALARM", "END:VEVENT")
	o = mustParse(t, wrap(in...))
	if !o.Truncated || problemCodes(o) != ProblemTooManyProperties || shape(o.Calendar()) != "VCALENDAR(VEVENT(VALARM))" ||
		o.Calendar().Components[0].Property("UID").Value != "next" {
		t.Errorf("over the component cap: %v %s", o.Problems, shape(o.Calendar()))
	}
	// Object-wide cap: 5000 properties (VERSION is one).
	var l []string
	for i := range 5 {
		l = append(l, ev(999, fmt.Sprint(i))...)
	}
	o = mustParse(t, wrap(l...))
	if o.HasProblems() || len(o.Calendar().Components) != 5 {
		t.Errorf("4996 properties: %v", o.Problems)
	}
	l = append(l, ev(10, "last")...)
	o = mustParse(t, wrap(l...))
	if !o.Truncated || problemCodes(o) != ProblemTooManyProperties || len(o.Calendar().Components) != 5 {
		t.Errorf("over the object cap: %v %d", o.Problems, len(o.Calendar().Components))
	}
}

func TestProblemLimit(t *testing.T) {
	l := make([]string, 300)
	for i := range l {
		l[i] = "garbage"
	}
	o := mustParse(t, wrap(l...))
	if len(o.Problems) != MaxProblems+1 || o.Problems[MaxProblems].Code != ProblemTooManyProblems {
		t.Errorf("%d problems", len(o.Problems))
	}
}

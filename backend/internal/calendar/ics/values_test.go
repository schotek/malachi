// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	cases := []struct {
		v, tzid string
		want    Time
		err     error
	}{
		{"20261020", "", Time{Kind: KindDate, Year: 2026, Month: 10, Day: 20}, nil},
		{"20261020", "Europe/Prague", Time{Kind: KindDate, Year: 2026, Month: 10, Day: 20}, nil},
		{"20261020T090000Z", "", Time{Kind: KindUTC, Year: 2026, Month: 10, Day: 20, Hour: 9}, nil},
		{"20261020t090000z", "", Time{Kind: KindUTC, Year: 2026, Month: 10, Day: 20, Hour: 9}, nil},
		{"20261020T090000Z", "Europe/Prague", Time{Kind: KindUTC, Year: 2026, Month: 10, Day: 20, Hour: 9}, nil},
		{"20261020T090000", "", Time{Kind: KindFloating, Year: 2026, Month: 10, Day: 20, Hour: 9}, nil},
		{"20261020T090000", "/mozilla.org/x/Europe/Prague", Time{Kind: KindZoned, Year: 2026, Month: 10, Day: 20, Hour: 9, TZID: "/mozilla.org/x/Europe/Prague"}, nil},
		{" 00010101T000000Z ", "", Time{Kind: KindUTC, Year: 1, Month: 1, Day: 1}, nil},
		{"99991231T235960Z", "", Time{Kind: KindUTC, Year: 9999, Month: 12, Day: 31, Hour: 23, Minute: 59, Second: 59}, nil},
		{"20240229", "", Time{Kind: KindDate, Year: 2024, Month: 2, Day: 29}, nil},
		{"20260229", "", Time{}, ErrRange},
		{"20260230T000000Z", "", Time{}, ErrRange},
		{"00000101", "", Time{}, ErrRange},
		{"20261320", "", Time{}, ErrRange},
		{"20261020T240000Z", "", Time{}, ErrRange},
		{"20261020T096000", "", Time{}, ErrRange},
		{"2026-10-20", "", Time{}, ErrSyntax},
		{"20261020T0900", "", Time{}, ErrSyntax},
		{"20261020X090000", "", Time{}, ErrSyntax},
		{"20261020T090000X", "", Time{}, ErrSyntax},
		{"+0261020", "", Time{}, ErrSyntax},
		{"", "", Time{}, ErrSyntax},
	}
	for _, c := range cases {
		got, err := ParseTime(c.v, c.tzid)
		if !errors.Is(err, c.err) || err == nil && got != c.want {
			t.Errorf("ParseTime(%q, %q) = %+v, %v; want %+v, %v", c.v, c.tzid, got, err, c.want, c.err)
		}
	}
	tm, _ := ParseTime("20261020T090000Z", "")
	if u, ok := tm.UTC(); !ok || !u.Equal(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("UTC %v", u)
	}
	loc := time.FixedZone("x", 2*3600)
	if got := tm.In(loc); got.Hour() != 11 {
		t.Errorf("In %v", got)
	}
	fl, _ := ParseTime("20261020T090000", "")
	if _, ok := fl.UTC(); ok {
		t.Errorf("floating has an instant")
	}
	if got := fl.In(loc); got.Hour() != 9 || got.Location() != loc {
		t.Errorf("floating In %v", got)
	}
	for _, v := range []string{"20261020", "20261020T090102Z", "20261020T090102"} {
		tm, _ := ParseTime(v, "")
		if tm.Value() != v {
			t.Errorf("Value %q, want %q", tm.Value(), v)
		}
	}
	p := NewTime("DTSTART", Time{Kind: KindZoned, Year: 2026, Month: 1, Day: 2, Hour: 3, TZID: "Europe/Prague"})
	if p.Value != "20260102T030000" || p.Param("TZID") != "Europe/Prague" {
		t.Errorf("NewTime %+v", p)
	}
	p = NewTime("DTSTART", DateOf(time.Date(2026, 1, 2, 23, 0, 0, 0, time.UTC)))
	if p.Value != "20260102" || p.Param("VALUE") != "DATE" {
		t.Errorf("NewTime date %+v", p)
	}
	if v := UTCOf(time.Date(2026, 1, 2, 3, 4, 5, 0, loc)).Value(); v != "20260102T010405Z" {
		t.Errorf("UTCOf %q", v)
	}
}

func TestTimes(t *testing.T) {
	p := NewProperty("EXDATE", "20261020T090000,bad,20261021T090000", Param{Name: "TZID", Values: []string{"Europe/Prague"}})
	ts, err := p.Times()
	if len(ts) != 2 || !errors.Is(err, ErrSyntax) || ts[1].TZID != "Europe/Prague" {
		t.Errorf("Times %+v %v", ts, err)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		v    string
		want time.Duration
		err  error
	}{
		{"PT15M", 15 * time.Minute, nil},
		{"-PT15M", -15 * time.Minute, nil},
		{"+P1D", 24 * time.Hour, nil},
		{"P1W", 7 * 24 * time.Hour, nil},
		{"P1DT2H3M4S", 26*time.Hour + 3*time.Minute + 4*time.Second, nil},
		{"P0DT0H10M0S", 10 * time.Minute, nil},
		{"PT0S", 0, nil},
		{"P1W2D", 9 * 24 * time.Hour, nil},
		{"p1dt1h", 25 * time.Hour, nil},
		{"P3653D", 3653 * 24 * time.Hour, nil},
		{"P3654D", 0, ErrRange},
		{"P522W", 0, ErrRange},
		{"P99999999W", 0, ErrRange},
		{"PT9999999999S", 0, ErrRange},
		{"P", 0, ErrSyntax},
		{"PT", 0, ErrSyntax},
		{"P1DT", 0, ErrSyntax},
		{"P1H", 0, ErrSyntax},
		{"PT1D", 0, ErrSyntax},
		{"P1D1W", 0, ErrSyntax},
		{"PT1M1H", 0, ErrSyntax},
		{"PT1H1H", 0, ErrSyntax},
		{"P1DTT1H", 0, ErrSyntax},
		{"1D", 0, ErrSyntax},
		{"--P1D", 0, ErrSyntax},
		{"P1.5D", 0, ErrSyntax},
		{"PD", 0, ErrSyntax},
		{"", 0, ErrSyntax},
	}
	for _, c := range cases {
		d, err := ParseDuration(c.v)
		if !errors.Is(err, c.err) || err == nil && d.Approx() != c.want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v, %v", c.v, d.Approx(), err, c.want, c.err)
		}
		if err == nil {
			back, err := ParseDuration(d.String())
			if err != nil || back.Approx() != d.Approx() {
				t.Errorf("String %q of %q does not read back", d.String(), c.v)
			}
		}
	}
	// Days are nominal: across the change to summer time a day is 23 hours.
	prague := time.FixedZone("CET", 3600)
	if loc, err := time.LoadLocation("Europe/Prague"); err == nil {
		prague = loc
	}
	d, _ := ParseDuration("P1DT1H")
	start := time.Date(2026, 3, 28, 12, 0, 0, 0, prague)
	if got := d.AddTo(start); got.Day() != 29 || got.Hour() != 13 {
		t.Errorf("AddTo %v", got)
	}
	n, _ := ParseDuration("-P1D")
	if got := n.AddTo(start); got.Day() != 27 || got.Hour() != 12 {
		t.Errorf("AddTo negative %v", got)
	}
}

func TestParsePeriod(t *testing.T) {
	p, err := ParsePeriod("20261020T090000Z/PT1H", "")
	if err != nil || !p.HasDuration || p.Duration.Approx() != time.Hour || p.Start.Kind != KindUTC {
		t.Errorf("period with duration %+v %v", p, err)
	}
	p, err = ParsePeriod("20261020T090000/20261020T100000", "Europe/Prague")
	if err != nil || p.HasDuration || p.End.Hour != 10 || p.End.TZID != "Europe/Prague" {
		t.Errorf("explicit period %+v %v", p, err)
	}
	for _, v := range []string{"20261020T090000Z", "20261020/PT1H", "20261020T090000Z/20261021", "20261020T090000Z/", "/PT1H", "20261020T090000Z/P99999W"} {
		if _, err := ParsePeriod(v, ""); err == nil {
			t.Errorf("ParsePeriod(%q) accepted", v)
		}
	}
	pr := NewProperty("RDATE", "20270101T090000Z/PT1H,x,20270102T090000Z/20270102T100000Z")
	ps, err := pr.Periods()
	if len(ps) != 2 || err == nil {
		t.Errorf("Periods %v %v", ps, err)
	}
}

func TestUTCOffset(t *testing.T) {
	cases := map[string]int{"+0100": 3600, "-0530": -19800, "+023015": 9015, "-0000": 0, "+2359": 86340}
	for v, want := range cases {
		got, err := ParseUTCOffset(v)
		if err != nil || got != want {
			t.Errorf("ParseUTCOffset(%q) = %d, %v", v, got, err)
		}
		back, _ := ParseUTCOffset(FormatUTCOffset(got))
		if back != got {
			t.Errorf("FormatUTCOffset(%d) = %q", got, FormatUTCOffset(got))
		}
	}
	for v, want := range map[string]error{"+9999": ErrRange, "+2400": ErrRange, "+0160": ErrRange, "0100": ErrSyntax, "+01": ErrSyntax, "+01:00": ErrSyntax, "junk": ErrSyntax, "": ErrSyntax} {
		if _, err := ParseUTCOffset(v); !errors.Is(err, want) {
			t.Errorf("ParseUTCOffset(%q) = %v, want %v", v, err, want)
		}
	}
}

func TestInteger(t *testing.T) {
	for v, want := range map[string]int32{"0": 0, "+5": 5, "-5": -5, " 42 ": 42, "2147483647": math.MaxInt32, "-2147483648": math.MinInt32} {
		if got, err := ParseInteger(v); err != nil || got != want {
			t.Errorf("ParseInteger(%q) = %d, %v", v, got, err)
		}
	}
	for v, want := range map[string]error{"2147483648": ErrRange, "-2147483649": ErrRange, "99999999999999999999": ErrRange, "": ErrSyntax, "+-1": ErrSyntax, "1e3": ErrSyntax, "0x10": ErrSyntax, "--1": ErrSyntax} {
		if _, err := ParseInteger(v); !errors.Is(err, want) {
			t.Errorf("ParseInteger(%q) = %v, want %v", v, err, want)
		}
	}
}

func TestCalAddress(t *testing.T) {
	cases := map[string]string{
		"mailto:ada@example.org":         "ada@example.org",
		"MAILTO:Ada.X@EXAMPLE.Org":       "Ada.X@example.org",
		" mailto:ada@example.org ":       "ada@example.org",
		"mailto:ada@example.org?subject": "ada@example.org",
		"mailto:":                        "",
		"mailto:@example.org":            "",
		"mailto:ada@":                    "",
		"mailto:a@b@example.org":         "",
		"mailto:ada smith@example.org":   "",
		"mailto:<ada@example.org>":       "",
		"mailto:ada@example..org":        "",
		"mailto:ada@.example.org":        "",
		"tel:+10000000000":               "",
		"https://example.org/ada":        "",
		"urn:uuid:0000":                  "",
		"ada@example.org":                "",
	}
	for v, want := range cases {
		a := ParseCalAddress(v)
		if a.Email != want || a.URI != v || a.IsEmail() != (want != "") {
			t.Errorf("ParseCalAddress(%q) = %+v, want %q", v, a, want)
		}
	}
	a := ParseCalAddress("MAILTO:Ada@Example.org")
	if !a.SameEmail("ada@EXAMPLE.org") || a.SameEmail("bo@example.org") || ParseCalAddress("tel:1").SameEmail("") {
		t.Errorf("SameEmail")
	}
}

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:      "plain",
		`a\nb\Nc`:    "a\nb\nc",
		`a\,b\;c\\d`: `a,b;c\d`,
		`a\:b`:       "a:b",
		`trailing\`:  "trailing",
		`\\n`:        `\n`,
		"ž\\ž":       "žž",
	} {
		if got := UnescapeText(in); got != want {
			t.Errorf("UnescapeText(%q) = %q, want %q", in, got, want)
		}
	}
	for _, s := range []string{"", "a,b;c\\d\ne", "line\r\nbreak\rcr", "ž;,\\"} {
		got := UnescapeText(EscapeText(s))
		want := s
		if s == "line\r\nbreak\rcr" {
			want = "line\nbreak\ncr"
		}
		if got != want {
			t.Errorf("escape round trip of %q = %q", s, got)
		}
	}
	p := NewProperty("CATEGORIES", `a\,b,c,,d\\`)
	got := p.TextList()
	if len(got) != 4 || got[0] != "a,b" || got[2] != "" || got[3] != `d\` {
		t.Errorf("TextList %q", got)
	}
	q := NewProperty("X", "a,b")
	if got := q.Values(); len(got) != 2 {
		t.Errorf("Values %q", got)
	}
}

func TestParamAccessors(t *testing.T) {
	p := NewProperty("attendee", "mailto:a@example.org")
	if p.Name != "ATTENDEE" || p.HasParam("CN") || p.Param("CN") != "" {
		t.Errorf("empty params")
	}
	p.SetParam("cn", "Ada")
	p.SetParam("CN", "Bo")
	p.SetParam("X-E")
	if len(p.Params) != 2 || p.Param("Cn") != "Bo" || !p.HasParam("x-e") || p.Param("X-E") != "" {
		t.Errorf("params %+v", p.Params)
	}
	var c *Component
	if c.Property("X") != nil || c.Properties("X") != nil || c.Children("X") != nil {
		t.Errorf("nil component")
	}
	var np *Property
	if np.Text() != "" || np.TextList() != nil || np.Values() != nil || np.URI() != "" || np.CalAddress().IsEmail() || np.Param("X") != "" {
		t.Errorf("nil property")
	}
	if _, err := np.Time(); err == nil {
		t.Errorf("nil property has a time")
	}
	if ts, err := np.Times(); ts != nil || err != nil {
		t.Errorf("nil property times")
	}
	_, _ = np.Duration()
	_, _ = np.Periods()
	_, _ = np.UTCOffset()
	_, _ = np.Integer()
	var o *Object
	if o.Calendar() != nil || o.HasProblems() {
		t.Errorf("nil object")
	}
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"math"
	"testing"
	"unicode/utf8"
)

func TestBuiltinFormat(t *testing.T) {
	for id, want := range map[int]numFormat{
		0: {}, 1: {}, 2: {}, 3: {}, 4: {}, 11: {}, 12: {}, 13: {}, 37: {}, 48: {}, 49: {}, 164: {},
		9: {kind: kindPercent}, 10: {kind: kindPercent},
		14: {kind: kindDate}, 15: {kind: kindDate}, 16: {kind: kindDate}, 17: {kind: kindDate},
		18: {kind: kindTime}, 20: {kind: kindTime},
		19: {kind: kindTime, seconds: true}, 21: {kind: kindTime, seconds: true},
		22: {kind: kindDateTime},
		27: {kind: kindDateAuto}, 36: {kind: kindDateAuto}, 50: {kind: kindDateAuto}, 58: {kind: kindDateAuto},
		45: {kind: kindTime, seconds: true}, 46: {kind: kindElapsed, seconds: true}, 47: {kind: kindTime, seconds: true},
		-1: {}, 1 << 30: {},
	} {
		if got := builtinFormat(id); got != want {
			t.Errorf("builtinFormat(%d) = %+v, want %+v", id, got, want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for code, want := range map[string]numFormat{
		"General":                           {},
		"general":                           {},
		"0.00":                              {},
		"#,##0.00":                          {},
		"0.00E+00":                          {},
		"##0.0e-0":                          {},
		"@":                                 {},
		`#,##0\ "Kč"`:                       {},
		`#,##0 [$€-407]`:                    {},
		`"day"0`:                            {},
		`0 "yd"`:                            {},
		`[Red]0.00;[Blue]-0.00`:             {},
		`_(* #,##0_);_(* (#,##0);_(* "-"_)`: {},
		"0%":                                {kind: kindPercent},
		"0.0%":                              {kind: kindPercent},
		`0"%"`:                              {},
		`0\%`:                               {},
		"yyyy-mm-dd":                        {kind: kindDate},
		`d/\ m/\ yyyy`:                      {kind: kindDate},
		"mmm-yy":                            {kind: kindDate},
		"m":                                 {kind: kindDate},
		"[$-F800]dddd, mmmm dd, yyyy":       {kind: kindDate},
		"hh:mm:ss":                          {kind: kindTime, seconds: true},
		"h:mm AM/PM":                        {kind: kindTime},
		"h:mm a/p":                          {kind: kindTime},
		"mm:ss":                             {kind: kindTime, seconds: true},
		"yyyy-mm-dd hh:mm":                  {kind: kindDateTime},
		`d/m/yyyy\ h:mm`:                    {kind: kindDateTime},
		"[h]:mm":                            {kind: kindElapsed},
		"[h]:mm:ss":                         {kind: kindElapsed, seconds: true},
		"[mm]:ss":                           {kind: kindElapsed, seconds: true},
		"[ss]":                              {kind: kindElapsed, seconds: true},
		"[hh]":                              {kind: kindElapsed},
		"[hm]":                              {},
		"[Red][h]:mm":                       {kind: kindElapsed},
		"0.00;yyyy":                         {},
		`"PRAVDA";"PRAVDA";"NEPRAVDA"`:      {},
		`"unclosed`:                         {},
		"[unclosed":                         {},
		`\`:                                 {},
		"e":                                 {kind: kindDate},
		"":                                  {},
	} {
		if got := parseFormat(code); got != want {
			t.Errorf("parseFormat(%q) = %+v, want %+v", code, got, want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	for _, c := range []struct {
		x    float64
		want string
	}{
		{0, "0"}, {math.Copysign(0, -1), "0"}, {1, "1"}, {-1, "-1"},
		{1234.5, "1234.5"}, {0.1, "0.1"}, {0.1 + 0.2, "0.3"}, {1.0 / 3, "0.333333333333333"},
		{2.0 / 3, "0.666666666666667"}, {123456789012345, "123456789012345"},
		{999999999999999, "999999999999999"}, {999999999999999.9, "1E+15"},
		{1e15, "1E+15"}, {1.5e20, "1.5E+20"}, {-2.5e100, "-2.5E+100"},
		{1e-9, "0.000000001"}, {1.234e-9, "0.000000001234"}, {9.99e-10, "9.99E-10"},
		{1e-10, "1E-10"}, {5e-324, "4.94065645841247E-324"}, {math.MaxFloat64, "1.79769313486232E+308"},
		{21305.94, "21305.94"}, {12345678.901234567, "12345678.9012346"}, {100, "100"}, {1e14, "100000000000000"},
	} {
		if got := formatNumber(c.x); got != c.want {
			t.Errorf("formatNumber(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestParseDecimal(t *testing.T) {
	for _, s := range []string{"0", "1", "-1", "+1", "1.5", ".5", "5.", "1e5", "1E-5", "-1.5e+300", "007"} {
		if _, ok := parseDecimal(s); !ok {
			t.Errorf("parseDecimal(%q) refused", s)
		}
	}
	for _, s := range []string{"", "-", ".", "e5", "1e", "1e+", "0x10", "NaN", "Inf", "infinity", "1_000", "1 ", " 1", "1e400", "1.2.3", "1,5"} {
		if _, ok := parseDecimal(s); ok {
			t.Errorf("parseDecimal(%q) accepted", s)
		}
	}
}

func TestFormatSerial(t *testing.T) {
	date := numFormat{kind: kindDate}
	clock := numFormat{kind: kindTime}
	both := numFormat{kind: kindDateTime}
	auto := numFormat{kind: kindDateAuto}
	for _, c := range []struct {
		x    float64
		f    numFormat
		sys  dateSystem
		want string
	}{
		{0, date, date1900, "1899-12-31"},
		{1, date, date1900, "1900-01-01"},
		{59, date, date1900, "1900-02-28"},
		{60, date, date1900, "1900-02-29"},
		{61, date, date1900, "1900-03-01"},
		{45658, date, date1900, "2025-01-01"},
		{46295, date, date1900, "2026-09-30"},
		{2958465, date, date1900, "9999-12-31"},
		{59.99999999, date, date1900, "1900-02-29"},
		{0, date, date1900NoBug, "1899-12-30"},
		{60, date, date1900NoBug, "1900-02-28"},
		{61, date, date1900NoBug, "1900-03-01"},
		{0, date, date1904, "1904-01-01"},
		{44833, date, date1904, "2026-09-30"},
		{0.5, clock, date1900, "12:00"},
		{0.573263888888889, clock, date1900, "13:45:30"},
		{0.573263888888889, numFormat{kind: kindTime, seconds: true}, date1900, "13:45:30"},
		{0.25, numFormat{kind: kindTime, seconds: true}, date1900, "06:00:00"},
		{1.25, clock, date1900, "06:00"},
		{0.99999999, clock, date1900, "00:00"},
		{46295.34375, both, date1900, "2026-09-30 08:15"},
		{46295.999999999, both, date1900, "2026-10-01 00:00"},
		{1.52083333333333, numFormat{kind: kindElapsed}, date1900, "36:30"},
		{0.0006944444, numFormat{kind: kindElapsed, seconds: true}, date1904, "0:01:00"},
		{46295, auto, date1900, "2026-09-30"},
		{0.5, auto, date1900, "12:00"},
		{46295.5, auto, date1900, "2026-09-30 12:00"},
	} {
		got, ok := formatSerial(c.x, c.f, c.sys)
		if !ok || got != c.want {
			t.Errorf("formatSerial(%v, %+v, %d) = %q, %v, want %q", c.x, c.f, c.sys, got, ok, c.want)
		}
	}
	for _, c := range []struct {
		x   float64
		sys dateSystem
	}{
		{-1, date1900}, {-0.5, date1900}, {2958466, date1900}, {2957004, date1904},
		{math.NaN(), date1900}, {math.Inf(1), date1900},
	} {
		if got, ok := formatSerial(c.x, date, c.sys); ok {
			t.Errorf("formatSerial(%v, %d) = %q, want a number", c.x, c.sys, got)
		}
	}
}

func TestFormatNumeric(t *testing.T) {
	for _, c := range []struct {
		v    string
		f    numFormat
		sys  dateSystem
		want string
	}{
		{"1234.5", numFormat{}, date1900, "1234.5"},
		{"0.155", numFormat{kind: kindPercent}, date1900, "15.5%"},
		{"0.07", numFormat{kind: kindPercent}, date1900, "7%"},
		{"1e308", numFormat{kind: kindPercent}, date1900, "1E+308"},
		{"46295", numFormat{kind: kindDate}, date1900, "2026-09-30"},
		{"-5", numFormat{kind: kindDate}, date1900, "-5"},
		{"3000000", numFormat{kind: kindDate}, date1900, "3000000"},
		{"#N/A", numFormat{}, date1900, "#N/A"},
		{"0x1p-2", numFormat{}, date1900, "0x1p-2"},
		{"NaN", numFormat{kind: kindDate}, date1900, "NaN"},
		{"0.10000000000000001", numFormat{}, date1900, "0.1"},
	} {
		if got := formatNumeric(c.v, c.f, c.sys); got != c.want {
			t.Errorf("formatNumeric(%q, %+v) = %q, want %q", c.v, c.f, got, c.want)
		}
	}
}

func FuzzNumFmt(f *testing.F) {
	for _, c := range []struct {
		code, v string
		id      int
	}{
		{"General", "1234.5", 0}, {"0.0%", "0.155", 9}, {"yyyy-mm-dd", "46295", 14},
		{"[h]:mm:ss", "1.5", 46}, {`"x";"y"`, "-0", 22}, {"[$-F800]dddd", "2958465.99999", 27},
		{`\`, "1e-10", 47}, {"mm:ss", "60", 45},
	} {
		f.Add(c.code, c.v, c.id, uint8(0))
	}
	f.Fuzz(func(t *testing.T, code, v string, id int, sys uint8) {
		ds := dateSystem(sys % 3)
		for _, nf := range []numFormat{parseFormat(code), builtinFormat(id)} {
			if nf.kind > kindDateAuto {
				t.Fatalf("kind %d", nf.kind)
			}
			got := formatNumeric(v, nf, ds)
			if again := formatNumeric(v, nf, ds); got != again {
				t.Fatalf("not deterministic: %q / %q", got, again)
			}
			if utf8.ValidString(v) && !utf8.ValidString(got) {
				t.Fatalf("invalid UTF-8 from %q: %q", v, got)
			}
			if x, ok := parseDecimal(v); ok && got == "" {
				t.Fatalf("number %v shown as nothing", x)
			}
		}
		if x, ok := parseDecimal(v); ok {
			if s := formatNumber(x); s == "" || len(s) > 32 {
				t.Fatalf("formatNumber(%v) = %q", x, s)
			}
		}
	})
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package tz

import (
	"archive/zip"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // as in cmd/malachid (D4)
)

func TestLoadLocationTzdata(t *testing.T) {
	for _, name := range []string{"Europe/Prague", "America/New_York", "Australia/Lord_Howe"} {
		if _, err := time.LoadLocation(name); err != nil {
			t.Fatalf("LoadLocation(%q): %v", name, err)
		}
	}
}

func TestResolve(t *testing.T) {
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cet := Offsets{Standard: time.Hour, Daylight: 2 * time.Hour, HasDaylight: true, At: at}
	tests := []struct {
		tzid  string
		hints []Offsets
		name  string
		kind  Kind
		err   bool
	}{
		{"Europe/Prague", nil, "Europe/Prague", KindIANA, false},
		{"  \"America/New_York\" ", nil, "America/New_York", KindIANA, false},
		{"UTC", nil, "UTC", KindIANA, false},
		{"Etc/UTC", nil, "UTC", KindIANA, false},
		{"Central Europe Standard Time", nil, "Europe/Budapest", KindWindows, false},
		{"central europe standard time", nil, "Europe/Budapest", KindWindows, false},
		{"Eastern Standard Time", nil, "America/New_York", KindWindows, false},
		{"Pacific Standard Time", nil, "America/Los_Angeles", KindWindows, false},
		{"UTC", nil, "UTC", KindIANA, false},
		{"Tokyo Standard Time", nil, "Asia/Tokyo", KindWindows, false},
		{"/mozilla.org/20050126_1/Europe/Prague", nil, "Europe/Prague", KindPrefix, false},
		{"/mozilla.org/20070129_1/America/New_York", nil, "America/New_York", KindPrefix, false},
		{"/softwarestudio.org/Olson_20011030_5/America/New_York", nil, "America/New_York", KindPrefix, false},
		{"/softwarestudio.org/Tzfile/Europe/Prague", nil, "Europe/Prague", KindPrefix, false},
		{"/freeassociation.sourceforge.net/Tzfile/Europe/Prague", nil, "Europe/Prague", KindPrefix, false},
		{"/freeassociation.sourceforge.net/Europe/Prague", nil, "Europe/Prague", KindPrefix, false},
		{"/citadel.org/20190914_1/Europe/Prague", nil, "Europe/Prague", KindPrefix, false},
		{"/mozilla.org/20050126_1/Nowhere/City", nil, "", 0, true},
		{"/etc/../../passwd", nil, "", 0, true},
		{"../../../etc/passwd", nil, "", 0, true},
		{"Local", nil, "", 0, true},
		{"", nil, "", 0, true},
		{"Customized Time Zone", nil, "", 0, true},
		{"Customized Time Zone", []Offsets{cet}, "", KindMatched, false},
		{"Customized Time Zone", []Offsets{{Standard: time.Hour + 30*time.Minute}}, "UTC+01:30", KindFixed, false},
		{"Customized Time Zone", []Offsets{{Standard: -(3*time.Hour + 30*time.Minute), At: at}}, "UTC-03:30", KindFixed, false},
		{"Weird", []Offsets{{Standard: 99 * time.Hour}}, "", 0, true},
		{"Weird", []Offsets{{Standard: time.Hour, Daylight: -40 * time.Hour, HasDaylight: true}}, "", 0, true},
		{"Europe/Prague\x00", nil, "", 0, true},
		{strings.Repeat("A/", 300), nil, "", 0, true},
	}
	for _, tc := range tests {
		loc, kind, err := Resolve(tc.tzid, tc.hints...)
		if tc.err {
			if err == nil || !errors.Is(err, ErrUnknownZone) {
				t.Errorf("Resolve(%q) = %v, %v, %v; want ErrUnknownZone", tc.tzid, loc, kind, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.tzid, err)
			continue
		}
		if kind != tc.kind {
			t.Errorf("Resolve(%q) kind = %v, want %v", tc.tzid, kind, tc.kind)
		}
		if tc.name != "" && loc.String() != tc.name {
			t.Errorf("Resolve(%q) = %q, want %q", tc.tzid, loc.String(), tc.name)
		}
		if kind == KindMatched {
			std, dst := yearOffsets(loc, 2026)
			if std != time.Hour || dst != 2*time.Hour {
				t.Errorf("matched %s has offsets %v/%v", loc, std, dst)
			}
		}
	}
}

func TestWindowsName(t *testing.T) {
	tests := map[string]string{
		"Europe/Prague":       "Central Europe Standard Time",
		"Europe/Budapest":     "Central Europe Standard Time",
		"Europe/Berlin":       "W. Europe Standard Time",
		"America/New_York":    "Eastern Standard Time",
		"America/Los_Angeles": "Pacific Standard Time",
		"Asia/Tokyo":          "Tokyo Standard Time",
		"UTC":                 "UTC",
		"Etc/UTC":             "UTC",
	}
	for iana, want := range tests {
		got, ok := WindowsName(iana)
		if !ok || got != want {
			t.Errorf("WindowsName(%q) = %q, %v; want %q", iana, got, ok, want)
		}
	}
	if _, ok := WindowsName("Nowhere/City"); ok {
		t.Error("WindowsName of an unknown zone succeeded")
	}
	// Every Windows name round-trips through its territory 001 zone, and
	// every zone of the table loads from the embedded tzdata.
	for win, iana := range windowsToIANA {
		if got, ok := WindowsName(iana); !ok || (got != win && iana != "Etc/UTC") {
			t.Errorf("WindowsName(%q) = %q, want %q", iana, got, win)
		}
	}
	for iana := range ianaToWindows {
		if _, ok := loadIANA(iana); !ok {
			t.Errorf("table zone %q does not load", iana)
		}
	}
}

// parsedObservance is the minimal subset of a VTIMEZONE observance that
// the writer's test reads back.
type parsedObservance struct {
	kind, dtstart, from, to, name string
}

func parseVTIMEZONE(t *testing.T, lines []string) (string, []parsedObservance) {
	t.Helper()
	if len(lines) < 2 || lines[0] != "BEGIN:VTIMEZONE" || lines[len(lines)-1] != "END:VTIMEZONE" {
		t.Fatalf("not a VTIMEZONE: %q", lines)
	}
	var tzid string
	var obs []parsedObservance
	var cur *parsedObservance
	for _, l := range lines[1 : len(lines)-1] {
		name, value, ok := strings.Cut(l, ":")
		if !ok {
			t.Fatalf("bad line %q", l)
		}
		for _, c := range l {
			if c < 0x20 || c > 0x7e {
				t.Fatalf("non-ASCII or control in %q", l)
			}
		}
		switch {
		case name == "TZID":
			tzid = value
		case name == "BEGIN":
			obs = append(obs, parsedObservance{kind: value})
			cur = &obs[len(obs)-1]
		case name == "END":
			if cur == nil || cur.kind != value {
				t.Fatalf("mismatched END %q", l)
			}
			cur = nil
		case cur == nil:
			t.Fatalf("property outside an observance: %q", l)
		case name == "DTSTART":
			cur.dtstart = value
		case name == "TZOFFSETFROM":
			cur.from = value
		case name == "TZOFFSETTO":
			cur.to = value
		case name == "TZNAME":
			cur.name = value
		default:
			t.Fatalf("unexpected property %q", l)
		}
	}
	return tzid, obs
}

func TestWriteVTIMEZONEPrague(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Prague")
	lines, err := WriteVTIMEZONE(loc,
		time.Date(2026, 5, 1, 9, 0, 0, 0, loc), time.Date(2027, 6, 1, 9, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	tzid, obs := parseVTIMEZONE(t, lines)
	if tzid != "Europe/Prague" {
		t.Fatalf("TZID %q", tzid)
	}
	want := []parsedObservance{
		{"STANDARD", "20251026T030000", "+0200", "+0100", "CET"},
		{"DAYLIGHT", "20260329T020000", "+0100", "+0200", "CEST"},
		{"STANDARD", "20261025T030000", "+0200", "+0100", "CET"},
		{"DAYLIGHT", "20270328T020000", "+0100", "+0200", "CEST"},
		{"STANDARD", "20271031T030000", "+0200", "+0100", "CET"},
	}
	if len(obs) != len(want) {
		t.Fatalf("observances %+v, want %+v", obs, want)
	}
	for i := range want {
		if obs[i] != want[i] {
			t.Errorf("observance %d = %+v, want %+v", i, obs[i], want[i])
		}
	}
}

func TestWriteVTIMEZONEEdges(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Prague")
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	lines, err := WriteVTIMEZONE(loc, from, from.AddDate(50, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, obs := parseVTIMEZONE(t, lines); len(obs) != 1+2*MaxVTIMEZONEYears {
		t.Errorf("50 years capped to %d observances, want %d", len(obs), 1+2*MaxVTIMEZONEYears)
	}
	lines, err = WriteVTIMEZONE(time.UTC, from, from)
	if err != nil {
		t.Fatal(err)
	}
	tzid, obs := parseVTIMEZONE(t, lines)
	if tzid != "UTC" || len(obs) != 1 || obs[0].from != "+0000" || obs[0].dtstart != "19700101T000000" {
		t.Errorf("UTC: %q %+v", tzid, obs)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	if lines, err = WriteVTIMEZONE(tokyo, from, from.AddDate(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, obs := parseVTIMEZONE(t, lines); len(obs) != 1 || obs[0].to != "+0900" {
		t.Errorf("Tokyo: %+v", obs)
	}
	if _, err := WriteVTIMEZONE(time.FixedZone("UTC+01:00", 3600), from, from); !errors.Is(err, ErrNotIANA) {
		t.Errorf("fixed zone: %v", err)
	}
	if _, err := WriteVTIMEZONE(time.Local, from, from); !errors.Is(err, ErrNotIANA) {
		t.Errorf("Local: %v", err)
	}
	if _, err := WriteVTIMEZONE(loc, from, from.AddDate(-1, 0, 0)); err == nil {
		t.Error("reversed range accepted")
	}
	if got := offsetValue(-(5*3600 + 30*60 + 7)); got != "-053007" {
		t.Errorf("offsetValue = %q", got)
	}
}

func TestSetPreferred(t *testing.T) {
	t.Cleanup(func() { SetPreferred(nil) })
	prague, _ := time.LoadLocation("Europe/Prague")
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	tests := []struct {
		pref       *time.Location
		tzid, want string
	}{
		{prague, "Central Europe Standard Time", "Europe/Prague"},
		{prague, "central europe standard time", "Europe/Prague"},
		{prague, "Eastern Standard Time", "America/New_York"},
		{prague, "Europe/Budapest", "Europe/Budapest"},
		{tokyo, "Central Europe Standard Time", "Europe/Budapest"},
		{tokyo, "Tokyo Standard Time", "Asia/Tokyo"},
		{nil, "Central Europe Standard Time", "Europe/Budapest"},
		{time.Local, "Central Europe Standard Time", "Europe/Budapest"},
		{time.FixedZone("Europe/Prague", 3600), "Central Europe Standard Time", "Europe/Prague"},
		{time.FixedZone("UTC+01:00", 3600), "Central Europe Standard Time", "Europe/Budapest"},
	}
	for _, tc := range tests {
		SetPreferred(tc.pref)
		loc, kind, err := Resolve(tc.tzid)
		if err != nil || loc.String() != tc.want {
			t.Errorf("preferred %v: Resolve(%q) = %v, %v, %v; want %s", tc.pref, tc.tzid, loc, kind, err, tc.want)
		}
		if w, _ := WindowsName("Europe/Budapest"); w != "Central Europe Standard Time" {
			t.Errorf("WindowsName changed: %q", w)
		}
	}
}

// TestWriteVTIMEZONESlimData: the zone files of Windows are Go's own (the
// embedded time/tzdata, the zoneinfo.zip of GOROOT), which list no
// transition past the last change of a zone's rules, so Time.ZoneBounds
// answers there with the turn of each year, which is no observance (in a
// leap year it is December 31 and stays there). The VTIMEZONE of such a
// file is that of the zone file of a Linux or a Mac, which lists them.
func TestWriteVTIMEZONESlimData(t *testing.T) {
	zr, err := zip.OpenReader(filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip"))
	if err != nil {
		t.Skipf("no zoneinfo.zip in GOROOT: %v", err)
	}
	defer zr.Close()
	for _, name := range []string{"Europe/Prague", "America/New_York", "Australia/Sydney", "Australia/Lord_Howe", "Asia/Tokyo", "Asia/Kolkata", "Europe/London"} {
		f, err := zr.Open(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		slim, err := time.LoadLocationFromTZData(name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, first := range []int{2026, 2027, 2028} {
			from := time.Date(first, 5, 1, 9, 0, 0, 0, slim)
			lines, err := WriteVTIMEZONE(slim, from, from.AddDate(MaxVTIMEZONEYears, 0, 0))
			if err != nil {
				t.Fatalf("%s %d: %v", name, first, err)
			}
			_, obs := parseVTIMEZONE(t, lines)
			for i, o := range obs[1:] {
				if o.from == o.to && o.name == obs[i].name {
					t.Errorf("%s %d: observance %+v changes nothing", name, first, o)
				}
			}
			real, err := time.LoadLocation(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := WriteVTIMEZONE(real, time.Date(first, 5, 1, 9, 0, 0, 0, real), time.Date(first+MaxVTIMEZONEYears, 5, 1, 9, 0, 0, 0, real))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(lines, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s %d: the VTIMEZONE of the zoneinfo.zip differs from that of LoadLocation:\n%v\n%v", name, first, lines, want)
			}
		}
	}
}

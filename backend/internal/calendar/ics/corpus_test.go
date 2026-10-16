// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const corpusDir = "../../../testdata/calendar"

// corpus returns every fixture of testdata/calendar by base name,
// including the rrule-*.ics files of calendar/recur (they must parse too).
func corpus(tb testing.TB) map[string][]byte {
	tb.Helper()
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.ics"))
	if err != nil {
		tb.Fatal(err)
	}
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			tb.Fatal(err)
		}
		out[filepath.Base(f)] = b
	}
	return out
}

type expect struct {
	codes     string // sorted problem codes with their counts, "" for none
	truncated bool
	calendars int
}

// expected lists the fixtures of this package; a fixture missing here
// (an rrule-*.ics of another package, or a new one) must still parse.
var expected = map[string]expect{
	"all-day-datetime-mixed.ics":    {calendars: 1},
	"attach-binary-huge.ics":        {codes: "lineTooLong", calendars: 1},
	"attach-uri.ics":                {calendars: 1},
	"attendee-flood.ics":            {calendars: 1},
	"attendee-non-mailto.ics":       {calendars: 1},
	"bare-cr.ics":                   {calendars: 1},
	"bare-lf.ics":                   {calendars: 1},
	"bidi-summary.ics":              {calendars: 1},
	"component-flood.ics":           {codes: "tooManyComponents", truncated: true, calendars: 1},
	"conference-http.ics":           {calendars: 1},
	"control-chars.ics":             {codes: "badParam controlChar*2", calendars: 1},
	"deep-nesting.ics":              {codes: "tooDeep", truncated: true, calendars: 1},
	"dtstart-year-0001.ics":         {calendars: 1},
	"dtstart-year-9999.ics":         {calendars: 1},
	"duration-huge.ics":             {calendars: 1},
	"duration-negative.ics":         {calendars: 1},
	"empty.ics":                     {codes: "empty strayProperty"},
	"end-before-start.ics":          {calendars: 1},
	"exdate-flood.ics":              {calendars: 1},
	"floating-and-utc-mixed.ics":    {calendars: 1},
	"folded-utf8-split.ics":         {calendars: 1},
	"google-request.ics":            {calendars: 1},
	"icloud-request.ics":            {calendars: 1},
	"invalid-utf8.ics":              {codes: "invalidUTF8*2", calendars: 1},
	"location-with-url.ics":         {calendars: 1},
	"method-cancel-unknown-uid.ics": {calendars: 1},
	"method-reply-spoofed.ics":      {calendars: 1},
	"method-unknown.ics":            {calendars: 1},
	"mismatched-end.ics":            {codes: "mismatchedEnd*2 strayEnd", calendars: 1},
	"missing-end.ics":               {codes: "missingEnd*3", calendars: 1},
	"mixed-uids.ics":                {calendars: 1},
	"no-vcalendar.ics":              {codes: "noVCalendar", calendars: 1},
	"nul-bytes.ics":                 {codes: "nulByte", calendars: 1},
	"organizer-missing.ics":         {calendars: 1},
	"organizer-spoofed.ics":         {calendars: 1},
	"outlook-request.ics":           {calendars: 1},
	"override-flood.ics":            {calendars: 1},
	"override-without-master.ics":   {calendars: 1},
	"param-flood.ics":               {codes: "tooManyParams", calendars: 1},
	"param-quote-injection.ics":     {codes: "badParam*3", calendars: 1},
	"property-flood.ics":            {codes: "tooManyProperties", truncated: true, calendars: 1},
	"rdate-flood.ics":               {calendars: 1},
	"recurrence-id-tz-mismatch.ics": {calendars: 1},
	"sequence-overflow.ics":         {calendars: 1},
	"styled-description.ics":        {calendars: 1},
	"teams-request.ics":             {calendars: 1},
	"two-vcalendars.ics":            {codes: "multipleCalendars", calendars: 2},
	"tzid-mozilla-prefix.ics":       {calendars: 1},
	"tzid-unknown.ics":              {calendars: 1},
	"tzid-windows.ics":              {calendars: 1},
	"url-javascript.ics":            {calendars: 1},
	"valarm-absolute-past.ics":      {calendars: 1},
	"valarm-email-procedure.ics":    {calendars: 1},
	"vtimezone-hostile.ics":         {calendars: 1},
	"vtimezone-missing.ics":         {calendars: 1},
	"x-alt-desc-html.ics":           {calendars: 1},
}

func problemCodes(o *Object) string {
	n := map[string]int{}
	for _, p := range o.Problems {
		n[p.Code]++
	}
	var out []string
	for c, k := range n {
		if k > 1 {
			c += "*" + string(rune('0'+min(k, 9)))
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestCorpus(t *testing.T) {
	files := corpus(t)
	for name := range expected {
		if _, ok := files[name]; !ok {
			t.Errorf("fixture %s missing", name)
		}
	}
	for name, data := range files {
		t.Run(name, func(t *testing.T) {
			if !bytes.Contains(data, []byte("X-MALACHI-TEST:")) {
				t.Errorf("no X-MALACHI-TEST line")
			}
			o, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if want, ok := expected[name]; ok {
				if got := problemCodes(o); got != want.codes {
					t.Errorf("problems %q, want %q (%v)", got, want.codes, o.Problems)
				}
				if o.Truncated != want.truncated {
					t.Errorf("truncated %v, want %v", o.Truncated, want.truncated)
				}
				if len(o.Calendars) != want.calendars {
					t.Errorf("%d calendars, want %d", len(o.Calendars), want.calendars)
				}
			}
			for _, c := range o.Calendars {
				if c.Name != "VCALENDAR" {
					t.Errorf("top-level %s", c.Name)
				}
			}
			exerciseAccessors(o)
			// Whatever the parser kept encodes and reads back the same,
			// problems or not (the guarantee proper is for clean objects).
			enc, err := Encode(o)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			back, err := Parse(enc)
			if err != nil {
				t.Fatalf("Parse(Encode): %v", err)
			}
			if !reflect.DeepEqual(back.Calendars, o.Calendars) {
				t.Errorf("round trip changed the calendars")
			}
			if !o.HasProblems() && back.HasProblems() {
				t.Errorf("round trip of a clean object has problems: %v", back.Problems)
			}
		})
	}
}

// The four realistic shapes parse clean and their accessors read.
func TestRealisticShapes(t *testing.T) {
	files := corpus(t)
	for _, name := range []string{"outlook-request.ics", "google-request.ics", "icloud-request.ics", "teams-request.ics"} {
		o, err := Parse(files[name])
		if err != nil || o.HasProblems() {
			t.Fatalf("%s: %v %v", name, err, o.Problems)
		}
		cal := o.Calendar()
		if got := cal.Property("METHOD").Value; got != "REQUEST" {
			t.Errorf("%s: METHOD %q", name, got)
		}
		ev := cal.Children("VEVENT")
		if len(ev) != 1 {
			t.Fatalf("%s: %d VEVENTs", name, len(ev))
		}
		start, err := ev[0].Property("DTSTART").Time()
		if err != nil || start.Kind != KindZoned || start.TZID == "" {
			t.Errorf("%s: DTSTART %+v %v", name, start, err)
		}
		org := ev[0].Property("ORGANIZER").CalAddress()
		if !org.SameEmail("ADA@example.org") {
			t.Errorf("%s: organizer %+v", name, org)
		}
		for _, a := range ev[0].Properties("ATTENDEE") {
			if !a.CalAddress().IsEmail() {
				t.Errorf("%s: attendee %q", name, a.Value)
			}
		}
		if len(ev[0].Children("VALARM")) != 1 {
			t.Errorf("%s: no VALARM", name)
		}
		if len(cal.Children("VTIMEZONE")) != 1 {
			t.Errorf("%s: no VTIMEZONE", name)
		}
	}
}

// exerciseAccessors calls every typed accessor on every property: none
// may panic, whatever the value.
func exerciseAccessors(o *Object) {
	var walk func(c *Component)
	walk = func(c *Component) {
		for i := range c.Props {
			p := &c.Props[i]
			_ = p.Text()
			_ = p.TextList()
			_, _ = p.Integer()
			_, _ = p.Time()
			_, _ = p.Times()
			_, _ = p.Periods()
			if d, err := p.Duration(); err == nil {
				_ = d.String()
				_ = d.Approx()
			}
			_, _ = p.UTCOffset()
			_ = p.CalAddress().SameEmail("a@example.org")
			_ = p.Param("TZID")
		}
		for _, ch := range c.Components {
			walk(ch)
		}
	}
	for _, c := range o.Calendars {
		walk(c)
	}
}

// The semantic fixtures keep what later tasks need.
func TestCorpusContent(t *testing.T) {
	files := corpus(t)
	get := func(name string) *Component {
		o, err := Parse(files[name])
		if err != nil {
			t.Fatal(err)
		}
		return o.Calendar()
	}
	ev := get("folded-utf8-split.ics").Children("VEVENT")[0]
	if got := ev.Property("SUMMARY").Text(); !strings.HasPrefix(got, "Schůzka o rozpočtu") {
		t.Errorf("folded summary %q", got)
	}
	ev = get("bare-lf.ics").Children("VEVENT")[0]
	if got := ev.Property("DESCRIPTION").Text(); got != "first part of a folded line" {
		t.Errorf("bare LF description %q", got)
	}
	ev = get("bare-cr.ics").Children("VEVENT")[0]
	if got := ev.Property("DESCRIPTION").Text(); got != "first part of a folded line" {
		t.Errorf("bare CR description %q", got)
	}
	ev = get("nul-bytes.ics").Children("VEVENT")[0]
	if got := ev.Property("LOCATION").Text(); got != "Room 4" {
		t.Errorf("NUL location %q", got)
	}
	ev = get("invalid-utf8.ics").Children("VEVENT")[0]
	if got := ev.Property("DESCRIPTION").Text(); !strings.Contains(got, "�") {
		t.Errorf("invalid UTF-8 description %q", got)
	}
	ev = get("control-chars.ics").Children("VEVENT")[0]
	if ev.Property("LOCATION") != nil {
		t.Errorf("LOCATION with a control character in a parameter kept")
	}
	ev = get("param-quote-injection.ics").Children("VEVENT")[0]
	at := ev.Properties("ATTENDEE")
	if len(at) != 1 || at[0].Param("CN") != "Fine, Quoted: Name" || len(at[0].ParamValues("DELEGATED-FROM")) != 2 {
		t.Errorf("quoted parameters %+v", at)
	}
	cal := get("deep-nesting.ics")
	depth, c := 1, cal
	for len(c.Components) > 0 {
		c, depth = c.Components[0], depth+1
	}
	if depth != MaxDepth {
		t.Errorf("deep nesting kept depth %d", depth)
	}
	cal = get("property-flood.ics")
	if ev := cal.Children("VEVENT"); len(ev) != 1 || ev[0].Property("SUMMARY").Text() != "Kept" {
		t.Errorf("property flood kept %d events", len(ev))
	}
	cal = get("component-flood.ics")
	if n := len(cal.Children("VEVENT")); n != MaxComponents-1 {
		t.Errorf("component flood kept %d events", n)
	}
	cal = get("no-vcalendar.ics")
	if ev := cal.Children("VEVENT"); len(ev) != 1 || ev[0].Property("UID").Value != "novcal-1@example.org" {
		t.Errorf("synthetic VCALENDAR %+v", cal)
	}
	cal = get("missing-end.ics")
	if ev := cal.Children("VEVENT"); len(ev) != 1 || len(ev[0].Children("VALARM")) != 1 {
		t.Errorf("missing END lost components")
	}
	cal = get("mismatched-end.ics")
	if ev := cal.Children("VEVENT"); len(ev) != 1 || len(ev[0].Children("VALARM")) != 1 {
		t.Errorf("mismatched END lost components")
	}
	ev = get("sequence-overflow.ics").Children("VEVENT")[0]
	if _, err := ev.Property("SEQUENCE").Integer(); err == nil {
		t.Errorf("SEQUENCE overflow accepted")
	}
	ev = get("duration-huge.ics").Children("VEVENT")[1]
	if _, err := ev.Property("DURATION").Duration(); err == nil {
		t.Errorf("DURATION of more than ten years accepted")
	}
	ev = get("exdate-flood.ics").Children("VEVENT")[0]
	if ts, err := ev.Properties("EXDATE")[0].Times(); err != nil || len(ts) != 3500 {
		t.Errorf("EXDATE flood: %d %v", len(ts), err)
	}
	ev = get("attendee-non-mailto.ics").Children("VEVENT")[0]
	var emails []string
	for _, a := range ev.Properties("ATTENDEE") {
		if ad := a.CalAddress(); ad.IsEmail() {
			emails = append(emails, ad.Email)
		}
	}
	if !reflect.DeepEqual(emails, []string{"Upper.Case@example.org"}) {
		t.Errorf("non-mailto attendees: %v", emails)
	}
}

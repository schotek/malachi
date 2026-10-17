// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func hrefsOf(rs []Resource) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Href)
	}
	sort.Strings(out)
	return out
}

func TestStateAndSyncCollection(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	a, _ := s.PutObject(cal, "a.ics", event("a", t0))
	b, _ := s.PutObject(cal, "b.ics", event("b", t0))
	c := newClient(t, s)

	st, err := c.State(t.Context(), cal)
	ctag, tok := s.CalendarState(cal)
	if err != nil || st.CTag != ctag || st.SyncToken != tok || !st.SupportsSync {
		t.Fatalf("state %+v %v (server %q %q)", st, err, ctag, tok)
	}

	res, err := c.SyncCollection(t.Context(), cal, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hrefsOf(res.Changed), []string{a, b}) || len(res.Removed) != 0 || res.Truncated || res.Token != tok {
		t.Fatalf("initial %+v", res)
	}
	for _, r := range res.Changed {
		if _, etag, _ := s.Object(r.Href); r.ETag != etag {
			t.Fatalf("etag %q, server %q", r.ETag, etag)
		}
	}

	s.PutObject(cal, "a.ics", event("a", t0.Add(time.Hour)))
	s.RemoveObject(b)
	res2, err := c.SyncCollection(t.Context(), cal, res.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hrefsOf(res2.Changed), []string{a}) || !slices.Equal(res2.Removed, []string{b}) || res2.Token == res.Token {
		t.Fatalf("delta %+v", res2)
	}
	res3, err := c.SyncCollection(t.Context(), cal, res2.Token)
	if err != nil || len(res3.Changed)+len(res3.Removed) != 0 {
		t.Fatalf("no change: %+v %v", res3, err)
	}

	s.ExpireSyncTokens(cal)
	if _, err := c.SyncCollection(t.Context(), cal, res2.Token); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("expired token: %v", err)
	}
	// 409 is the other answer RFC 6578 allows.
	s.FailNext(caldavtest.Failure{Method: "REPORT", Status: http.StatusConflict})
	if _, err := c.SyncCollection(t.Context(), cal, "x"); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("409: %v", err)
	}
}

func TestSyncCollectionTruncated(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	var want []string
	for i := range 7 {
		h, _ := s.PutObject(cal, fmt.Sprintf("e%d.ics", i), event(fmt.Sprint(i), t0))
		want = append(want, h)
	}
	s.Set(func(s *caldavtest.Server) { s.SyncLimit = 3 })
	c := newClient(t, s)
	var got []string
	tok := ""
	for pass := 0; ; pass++ {
		if pass > 5 {
			t.Fatal("truncation never ends")
		}
		res, err := c.SyncCollection(t.Context(), cal, tok)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hrefsOf(res.Changed)...)
		tok = res.Token
		if !res.Truncated {
			break
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestRadicaleHasNoSyncCollection(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Radicale)
	cal := s.CalendarHref("calendar.ics")
	s.PutObject(cal, "a.ics", event("a", t0))
	c := newClient(t, s)
	st, err := c.State(t.Context(), cal)
	if err != nil || st.SupportsSync || st.SyncToken != "" || st.CTag == "" {
		t.Fatalf("state %+v %v", st, err)
	}
	if _, err := c.SyncCollection(t.Context(), cal, ""); !IsForbidden(err) {
		t.Fatalf("sync-collection: %v", err)
	}
	all, err := c.ListETags(t.Context(), cal)
	if err != nil || len(all) != 1 || all[0].ETag == "" {
		t.Fatalf("list %+v %v", all, err)
	}
}

func TestQueryETags(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	in, _ := s.PutObject(cal, "in.ics", event("in", t0))
	out, _ := s.PutObject(cal, "out.ics", event("out", t0.AddDate(-2, 0, 0)))
	c := newClient(t, s)
	from, to := t0.AddDate(0, -1, 0), t0.AddDate(0, 1, 0)
	res, filtered, err := c.QueryETags(t.Context(), cal, from, to)
	if err != nil || !filtered || !slices.Equal(hrefsOf(res), []string{in}) {
		t.Fatalf("query %v %v %v", hrefsOf(res), filtered, err)
	}
	s.Set(func(s *caldavtest.Server) { s.TimeRange = false })
	res, filtered, err = c.QueryETags(t.Context(), cal, from, to)
	if err != nil || filtered || !slices.Equal(hrefsOf(res), []string{in, out}) {
		t.Fatalf("fallback %v %v %v", hrefsOf(res), filtered, err)
	}
	reqs := s.Requests()
	if last := reqs[len(reqs)-1]; last.Method != "PROPFIND" || last.Depth != "1" {
		t.Fatalf("fallback request %s depth %s", last.Method, last.Depth)
	}
}

func TestMultigetBatching(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	var hrefs []string
	for i := range 120 {
		h, _ := s.PutObject(cal, fmt.Sprintf("e%03d.ics", i), event(fmt.Sprint(i), t0))
		hrefs = append(hrefs, h)
	}
	missing := cal + "missing.ics"
	hrefs = append(hrefs, missing)
	c := newClient(t, s)
	s.ResetRequests()
	objs, err := c.Multiget(t.Context(), cal, hrefs)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != len(hrefs) {
		t.Fatalf("%d objects", len(objs))
	}
	for i, o := range objs {
		if o.Href != hrefs[i] {
			t.Fatalf("order: %s at %d", o.Href, i)
		}
		if o.Href == missing {
			if o.Status != http.StatusNotFound {
				t.Fatalf("missing: %+v", o)
			}
			continue
		}
		data, etag, _ := s.Object(o.Href)
		if o.Status != 200 || o.ETag != etag || string(o.Data) != string(data) {
			t.Fatalf("object %s: %+v", o.Href, o)
		}
	}
	var batches []int
	for _, r := range s.Requests() {
		if r.Method == "REPORT" {
			batches = append(batches, r.MultigetHrefs)
		}
	}
	if !slices.Equal(batches, []int{50, 50, 21}) {
		t.Fatalf("batches %v", batches)
	}

	// A server that refuses multiget is asked object by object.
	s.Set(func(s *caldavtest.Server) { s.Multiget = false })
	s.ResetRequests()
	objs, err = c.Multiget(t.Context(), cal, hrefs[:3])
	if err != nil || len(objs) != 3 || objs[2].Status != 200 {
		t.Fatalf("fallback %+v %v", objs, err)
	}
	gets := 0
	for _, r := range s.Requests() {
		if r.Method == http.MethodGet {
			gets++
		}
	}
	if gets != 3 {
		t.Fatalf("%d GETs", gets)
	}
}

func TestMultigetFlood(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.FixtureAnswer(t, "multistatus-flood.xml", 0))
	objs, err := newClient(t, s).Multiget(t.Context(), cal, []string{cal + "a.ics"})
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].ETag != `"a0"` || string(objs[0].Data) != "BEGIN:VCALENDAR\r\nUID:a-0\r\nEND:VCALENDAR\r\n" {
		t.Fatalf("objects %+v", objs)
	}
}

func TestElementFlood(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	cal := s.CalendarHref("personal")
	// One response per href is six elements; the element cap stops an
	// answer of more than ~33 000 before MaxResources is reached.
	s.FailNext(caldavtest.HugeMultistatus(8 << 20))
	if _, _, err := newClient(t, s).QueryETags(t.Context(), cal, t0, t0.Add(time.Hour)); code(err) != api.CodeMalformedCalendar {
		t.Fatalf("element flood: %v", err)
	}
}

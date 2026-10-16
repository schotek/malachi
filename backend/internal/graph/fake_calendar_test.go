// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
)

// fakeCal is an in-memory Microsoft Graph calendar service: calendars and
// event items (singles, series masters, occurrences, exceptions) as JSON
// maps, so tests can give them any shape the service sends. Every change
// bumps seq; a delta token remembers the seq it was issued at.
type fakeCal struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	seq       int
	pageSize  int
	calendars []map[string]any
	items     map[string]*fakeItem
	removed   []fakeRemoval
	tokens    map[string]fakeDelta // deltaLink state → what it was issued for
	pagesLeft map[string][]map[string]any
	requests  []fakeCalRequest
	// noDelta: calendars whose delta answers 400 (the list fallback).
	noDelta map[string]bool
	// fail: method+" "+path → status and code, answered once.
	fail map[string]fakeFailure
	// schedule answers getSchedule (nil: an empty answer).
	schedule func(body map[string]any) (int, any)
	// attachments received, per event id.
	attachments map[string][]map[string]any
}

type fakeItem struct {
	cal  string
	data map[string]any
	ver  int
	seq  int
}

type fakeRemoval struct {
	cal, id string
	seq     int
}

type fakeDelta struct {
	cal      string
	seq      int
	from, to time.Time
}

type fakeFailure struct {
	status int
	code   string
}

type fakeCalRequest struct {
	Method, Path, Query string
	IfMatch, Prefer     string
	Body                map[string]any
}

func newFakeCal(t *testing.T) *fakeCal {
	f := &fakeCal{t: t, pageSize: 3, items: map[string]*fakeItem{}, tokens: map[string]fakeDelta{},
		pagesLeft: map[string][]map[string]any{}, noDelta: map[string]bool{}, fail: map[string]fakeFailure{},
		attachments: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCal) client() *Client {
	return NewClient(Options{
		BaseURL: f.srv.URL, HTTP: f.srv.Client(),
		Token: func(context.Context) (string, error) { return "cal-token", nil },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
}

func (f *fakeCal) provider(opts CalendarProviderOptions) *CalendarProvider {
	if opts.AccountID == "" {
		opts.AccountID = "acc_1"
	}
	if opts.Me == nil {
		opts.Me = []string{"me@contoso.invalid"}
	}
	return newCalendarProvider(f.client(), opts)
}

// add stores an item in a calendar; its etag and changeKey follow ver.
func (f *fakeCal) add(cal string, item map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := item["id"].(string)
	it := &fakeItem{cal: cal, data: item, ver: 1, seq: f.bumpLocked()}
	f.items[id] = it
	f.stamp(id, it)
	return id
}

func (f *fakeCal) bumpLocked() int { f.seq++; return f.seq }

func (f *fakeCal) stamp(id string, it *fakeItem) {
	ck := fmt.Sprintf("ck-%s-%d", id, it.ver)
	it.data["changeKey"] = ck
	it.data["@odata.etag"] = `W/"` + ck + `"`
}

// edit changes fields of an item (a new version).
func (f *fakeCal) edit(id string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it := f.items[id]
	for k, v := range fields {
		it.data[k] = v
	}
	it.ver++
	it.seq = f.bumpLocked()
	f.stamp(id, it)
}

func (f *fakeCal) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeLocked(id)
}

func (f *fakeCal) removeLocked(id string) {
	it := f.items[id]
	if it == nil {
		return
	}
	delete(f.items, id)
	f.removed = append(f.removed, fakeRemoval{cal: it.cal, id: id, seq: f.bumpLocked()})
}

func (f *fakeCal) etag(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[id].data["@odata.etag"].(string)
}

func (f *fakeCal) item(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it := f.items[id]; it != nil {
		return it.data
	}
	return nil
}

func (f *fakeCal) reqs(method, pathPart string) []fakeCalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCalRequest
	for _, r := range f.requests {
		if r.Method == method && strings.Contains(r.Path, pathPart) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCal) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (f *fakeCal) failWith(w http.ResponseWriter, status int, code string) {
	f.reply(w, status, map[string]any{"error": map[string]string{"code": code, "message": "fake " + code}})
}

// itemStart is an item's start instant (UTC wall clock of start.dateTime).
func itemStart(d map[string]any) time.Time {
	st, _ := d["start"].(map[string]any)
	s, _ := st["dateTime"].(string)
	t, _ := time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
	return t
}

func itemEnd(d map[string]any) time.Time {
	st, _ := d["end"].(map[string]any)
	s, _ := st["dateTime"].(string)
	t, _ := time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
	return t
}

func inWindow(d map[string]any, from, to time.Time) bool {
	if d["type"] == "seriesMaster" {
		return true
	}
	return itemStart(d).Before(to) && itemEnd(d).After(from)
}

func parseWindow(q url.Values) (time.Time, time.Time) {
	from, _ := time.Parse(time.RFC3339, q.Get("startDateTime"))
	to, _ := time.Parse(time.RFC3339, q.Get("endDateTime"))
	return from, to
}

// view lists the items of cal in [from, to) as calendarView does (no
// masters unless withMasters, as delta does).
func (f *fakeCal) view(cal string, from, to time.Time, withMasters bool, since int) []map[string]any {
	var ids []string
	for id, it := range f.items {
		if it.cal != cal || it.seq <= since {
			continue
		}
		if it.data["type"] == "seriesMaster" && !withMasters {
			continue
		}
		if inWindow(it.data, from, to) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, f.items[id].data)
	}
	return out
}

func (f *fakeCal) page(w http.ResponseWriter, all []map[string]any, delta string) {
	key := strconv.Itoa(f.bumpLocked())
	f.pagesLeft[key] = all
	f.nextPage(w, key, delta)
}

func (f *fakeCal) nextPage(w http.ResponseWriter, key, delta string) {
	all := f.pagesLeft[key]
	n := min(f.pageSize, len(all))
	resp := map[string]any{"value": all[:n]}
	f.pagesLeft[key] = all[n:]
	if len(all) > n {
		resp["@odata.nextLink"] = f.srv.URL + "/v1.0/next?key=" + key + "&delta=" + url.QueryEscape(delta)
	} else if delta != "" {
		resp["@odata.deltaLink"] = delta
	}
	f.reply(w, 200, resp)
}

func (f *fakeCal) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	p := strings.TrimPrefix(r.URL.Path, "/v1.0")
	f.requests = append(f.requests, fakeCalRequest{Method: r.Method, Path: p, Query: r.URL.RawQuery,
		IfMatch: r.Header.Get("If-Match"), Prefer: r.Header.Get("Prefer"), Body: body})
	if r.Header.Get("Authorization") != "Bearer cal-token" {
		f.failWith(w, 401, "InvalidAuthenticationToken")
		return
	}
	if fl, ok := f.fail[r.Method+" "+p]; ok {
		delete(f.fail, r.Method+" "+p)
		f.failWith(w, fl.status, fl.code)
		return
	}
	q := r.URL.Query()
	seg := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case r.Method == "GET" && p == "/next":
		f.nextPage(w, q.Get("key"), q.Get("delta"))
	case r.Method == "GET" && p == "/me/calendars":
		f.reply(w, 200, map[string]any{"value": f.calendars})
	case r.Method == "GET" && p == "/deltastate":
		st := f.tokens[q.Get("s")]
		f.delta(w, st.cal, st.from, st.to, st.seq)
	case r.Method == "GET" && p == "/me/calendarView/delta":
		from, to := parseWindow(q)
		f.delta(w, f.defaultCal(), from, to, 0)
	case r.Method == "GET" && len(seg) == 5 && seg[1] == "calendars" && seg[3] == "calendarView" && seg[4] == "delta":
		if f.noDelta[seg[2]] {
			f.failWith(w, 400, "ErrorInvalidRequest")
			return
		}
		from, to := parseWindow(q)
		f.delta(w, seg[2], from, to, 0)
	case r.Method == "GET" && len(seg) == 4 && seg[1] == "calendars" && seg[3] == "calendarView":
		from, to := parseWindow(q)
		f.page(w, f.view(seg[2], from, to, false, 0), "")
	case r.Method == "POST" && len(seg) == 4 && seg[1] == "calendars" && seg[3] == "events":
		f.create(w, seg[2], body)
	case r.Method == "POST" && p == "/me/calendar/getSchedule":
		if f.schedule == nil {
			f.reply(w, 200, map[string]any{"value": []any{}})
			return
		}
		st, v := f.schedule(body)
		f.reply(w, st, v)
	case len(seg) >= 3 && seg[1] == "events":
		f.event(w, r, seg[2:], body, q)
	default:
		f.failWith(w, 404, "ErrorNotFound")
	}
}

func (f *fakeCal) defaultCal() string {
	for _, c := range f.calendars {
		if c["isDefaultCalendar"] == true {
			return c["id"].(string)
		}
	}
	return ""
}

func (f *fakeCal) delta(w http.ResponseWriter, cal string, from, to time.Time, since int) {
	items := f.view(cal, from, to, true, since)
	for _, rm := range f.removed {
		if rm.cal == cal && rm.seq > since && since > 0 {
			items = append(items, map[string]any{"id": rm.id, "@removed": map[string]string{"reason": "deleted"}})
		}
	}
	state := strconv.Itoa(f.bumpLocked())
	f.tokens[state] = fakeDelta{cal: cal, seq: f.seq, from: from, to: to}
	f.page(w, items, f.srv.URL+"/v1.0/deltastate?s="+state)
}

func (f *fakeCal) create(w http.ResponseWriter, cal string, body map[string]any) {
	// transactionId makes a repeated create the same event.
	for id, it := range f.items {
		if tx, _ := body["transactionId"].(string); tx != "" && it.data["transactionId"] == tx {
			f.reply(w, 201, f.items[id].data)
			return
		}
	}
	id := fmt.Sprintf("new-%d", f.bumpLocked())
	data := map[string]any{"id": id, "type": "singleInstance", "isOrganizer": true, "iCalUId": "uid-" + id}
	for k, v := range body {
		data[k] = v
	}
	if body["recurrence"] != nil {
		data["type"] = "seriesMaster"
	}
	it := &fakeItem{cal: cal, data: data, ver: 1, seq: f.bumpLocked()}
	f.items[id] = it
	f.stamp(id, it)
	f.reply(w, 201, data)
}

func (f *fakeCal) event(w http.ResponseWriter, r *http.Request, seg []string, body map[string]any, q url.Values) {
	id := seg[0]
	it := f.items[id]
	if it == nil {
		f.failWith(w, 404, "ErrorItemNotFound")
		return
	}
	if m := r.Header.Get("If-Match"); m != "" && m != it.data["@odata.etag"] {
		f.failWith(w, 412, "ErrorIrresolvableConflict")
		return
	}
	switch {
	case r.Method == "GET" && len(seg) == 1:
		f.reply(w, 200, it.data)
	case r.Method == "GET" && len(seg) == 2 && seg[1] == "instances":
		from, to := parseWindow(q)
		var out []map[string]any
		ids := make([]string, 0)
		for iid, x := range f.items {
			if x.data["seriesMasterId"] == id && inWindow(x.data, from, to) {
				ids = append(ids, iid)
			}
		}
		sort.Strings(ids)
		for _, iid := range ids {
			out = append(out, f.items[iid].data)
		}
		f.page(w, out, "")
	case r.Method == "PATCH" && len(seg) == 1:
		for k, v := range body {
			it.data[k] = v
		}
		if it.data["type"] == "occurrence" {
			it.data["type"] = "exception"
		}
		it.ver++
		it.seq = f.bumpLocked()
		f.stamp(id, it)
		f.reply(w, 200, it.data)
	case r.Method == "DELETE" && len(seg) == 1:
		f.removeLocked(id)
		f.reply(w, 204, nil)
	case r.Method == "POST" && len(seg) == 2 && seg[1] == "cancel":
		f.removeLocked(id)
		f.reply(w, 202, nil)
	case r.Method == "POST" && len(seg) == 2 && seg[1] == "attachments":
		f.attachments[id] = append(f.attachments[id], body)
		f.reply(w, 201, map[string]any{"id": "att-1"})
	case r.Method == "POST" && len(seg) == 2 && (seg[1] == "accept" || seg[1] == "tentativelyAccept" || seg[1] == "decline"):
		resp := map[string]string{"accept": "accepted", "tentativelyAccept": "tentativelyAccepted", "decline": "declined"}[seg[1]]
		it.data["responseStatus"] = map[string]any{"response": resp}
		it.ver++
		it.seq = f.bumpLocked()
		f.stamp(id, it)
		f.reply(w, 202, nil)
	default:
		f.failWith(w, 405, "ErrorInvalidRequest")
	}
}

// recSink records what a Sync hands over.
type recSink struct {
	upserts []calendar.Object
	removes []string
}

func (s *recSink) Upsert(o calsync.Object) error { s.upserts = append(s.upserts, o); return nil }
func (s *recSink) Remove(id string) error        { s.removes = append(s.removes, id); return nil }

func (s *recSink) byID(id string) *calendar.Object {
	for i := range s.upserts {
		if s.upserts[i].RemoteID == id {
			return &s.upserts[i]
		}
	}
	return nil
}

// Item builders. Times are written as the service does with the Prefer
// header: seven fractional digits, zone "UTC".

func gtime(t time.Time) map[string]any {
	return map[string]any{"dateTime": t.UTC().Format("2006-01-02T15:04:05") + ".0000000", "timeZone": "UTC"}
}

func single(id string, start time.Time, d time.Duration, extra map[string]any) map[string]any {
	m := map[string]any{"id": id, "type": "singleInstance", "iCalUId": "uid-" + id, "subject": "Event " + id,
		"start": gtime(start), "end": gtime(start.Add(d)), "originalStartTimeZone": "Central Europe Standard Time",
		"originalEndTimeZone": "Central Europe Standard Time", "isOrganizer": true, "showAs": "busy"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func master(id string, start time.Time, d time.Duration, pattern map[string]any) map[string]any {
	return map[string]any{"id": id, "type": "seriesMaster", "iCalUId": "uid-" + id, "subject": "Series " + id,
		"start": gtime(start), "end": gtime(start.Add(d)), "originalStartTimeZone": "Central Europe Standard Time",
		"isOrganizer": true, "showAs": "busy",
		"recurrence": map[string]any{"pattern": pattern, "range": map[string]any{"type": "noEnd",
			"startDate": start.Format("2006-01-02"), "endDate": "0001-01-01", "recurrenceTimeZone": "Central Europe Standard Time"}}}
}

func occurrence(id, masterID string, start time.Time, d time.Duration, typ string) map[string]any {
	return map[string]any{"id": id, "type": typ, "seriesMasterId": masterID, "subject": "Series " + masterID,
		"start": gtime(start), "end": gtime(start.Add(d)), "originalStart": start.UTC().Format(time.RFC3339),
		"originalStartTimeZone": "", "isOrganizer": true, "showAs": "busy"}
}

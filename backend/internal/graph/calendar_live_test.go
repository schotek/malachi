// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Live, read-only Microsoft Graph calendar spike (task T0, docs/calendar.md
// §3.6, D14/D15). It is skipped unless MALACHI_GRAPH_LIVE=1 and
// MALACHI_GRAPH_TOKEN are set.
//
// How to run it (the owner, on his Microsoft 365 account):
//
//  1. Open https://developer.microsoft.com/graph/graph-explorer, sign in with
//     the work account and consent to Calendars.Read and Mail.Read
//     (Modify permissions tab). Copy the token from the "Access token" tab.
//  2. cd backend && MALACHI_GRAPH_LIVE=1 MALACHI_GRAPH_TOKEN=… \
//     go test ./internal/graph/ -run CalendarLive -v -count=1 2>&1 | grep -E 'FINDING|SUMMARY|FAIL'
//  3. Paste the FINDING/SUMMARY lines back.
//
// The token must never be committed or pasted into chat (the test never logs
// it). The test only reads: every request goes through a guard that refuses
// anything but GET and the read-only POST .../calendar/getSchedule. Event
// subjects and bodies are not logged; only shapes, types and calendar names.
package graph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// readOnlyTransport enforces the "only reads" promise and asks for UTC
// event times (the client's own Prefer header is fixed, so it is extended
// here).
type readOnlyTransport struct{ next http.RoundTripper }

func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch {
	case r.Method == http.MethodGet:
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/calendar/getSchedule"):
	default:
		return nil, fmt.Errorf("calendar live test: refusing non-read request %s %s", r.Method, r.URL.Path)
	}
	r = r.Clone(r.Context())
	r.Header.Set("Prefer", `IdType="ImmutableId", odata.maxpagesize=200, outlook.timezone="UTC"`)
	return t.next.RoundTrip(r)
}

type liveCal struct {
	t   *testing.T
	c   *Client
	ctx context.Context
	n   int // findings
	bad int // failed steps
}

func (l *liveCal) f(format string, a ...any) {
	l.n++
	l.t.Logf("FINDING: "+format, a...)
}

func (l *liveCal) fail(step string, err error) {
	l.bad++
	if se, ok := err.(*StatusError); ok {
		l.f("%s FAILED status=%d code=%q message=%q", step, se.Status, se.Code, se.Message)
		return
	}
	l.f("%s FAILED %v", step, err)
}

func keysOf(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

func list(m map[string]any, k string) []any {
	a, _ := m[k].([]any)
	return a
}

// delta follows a calendarView/delta query to its end (at most 10 pages).
func (l *liveCal) delta(path string) (events []map[string]any, pages int, deltaLink bool, err error) {
	next := path
	for pages < 10 && next != "" {
		var page map[string]any
		if err = l.c.Get(l.ctx, next, &page); err != nil {
			return
		}
		pages++
		for _, v := range list(page, "value") {
			if e, ok := v.(map[string]any); ok {
				events = append(events, e)
			}
		}
		if str(page, "@odata.deltaLink") != "" {
			deltaLink = true
		}
		next = str(page, "@odata.nextLink")
	}
	return
}

func TestCalendarLive(t *testing.T) {
	if os.Getenv("MALACHI_GRAPH_LIVE") != "1" {
		t.Skip("set MALACHI_GRAPH_LIVE=1 and MALACHI_GRAPH_TOKEN to run the live Graph calendar spike")
	}
	token := strings.TrimSpace(os.Getenv("MALACHI_GRAPH_TOKEN"))
	if token == "" {
		t.Skip("MALACHI_GRAPH_TOKEN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	l := &liveCal{t: t, ctx: ctx}
	l.c = NewClient(Options{
		HTTP:  &http.Client{Timeout: bodyTimeout, Transport: readOnlyTransport{http.DefaultTransport}},
		Token: func(context.Context) (string, error) { return token, nil },
	})

	// 7. Scope check (first, so a 403 later is easy to read).
	func() {
		parts := strings.Split(token, ".")
		if len(parts) < 2 {
			l.f("7 token is not a JWT (opaque or personal-account token); scopes unknown")
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
		if err != nil {
			l.f("7 JWT payload not decodable")
			return
		}
		var claims map[string]any
		if json.Unmarshal(raw, &claims) != nil {
			l.f("7 JWT payload not JSON")
			return
		}
		l.f("7 scp=%q roles=%v tid_present=%v", str(claims, "scp"), claims["roles"], str(claims, "tid") != "")
	}()

	// 1. Calendars.
	var cals []map[string]any
	func() {
		var out map[string]any
		if err := l.c.Get(ctx, "/me/calendars", &out); err != nil {
			l.fail("1 /me/calendars", err)
			return
		}
		for _, v := range list(out, "value") {
			c, _ := v.(map[string]any)
			if c == nil {
				continue
			}
			cals = append(cals, c)
			l.f("1 calendar name=%q isDefault=%v canEdit=%v color=%q hexColor=%q providers=%v changeKey_present=%v",
				str(c, "name"), c["isDefaultCalendar"], c["canEdit"], str(c, "color"), str(c, "hexColor"),
				c["allowedOnlineMeetingProviders"], str(c, "changeKey") != "")
		}
		if len(cals) > 0 {
			l.f("1 calendar keys=%s", keysOf(cals[0]))
		}
	}()

	now := time.Now().UTC().Truncate(time.Hour)
	q := url.Values{}
	q.Set("startDateTime", now.Format("2006-01-02T15:04:05Z"))
	q.Set("endDateTime", now.AddDate(0, 0, 30).Format("2006-01-02T15:04:05Z"))
	window := q.Encode()

	// 2. Default calendar delta.
	var masterID string
	func() {
		evs, pages, dl, err := l.delta("/me/calendarView/delta?" + window)
		if err != nil {
			l.fail("2 /me/calendarView/delta", err)
			return
		}
		l.f("2 default delta ok events=%d pages=%d deltaLink=%v", len(evs), pages, dl)
		types := map[string]int{}
		tzs := map[string]int{}
		var ical, master, allDayShown int
		for _, e := range evs {
			types[str(e, "type")+" "+str(e, "@odata.type")]++
			tzs[str(e, "originalStartTimeZone")+"|"+str(e, "originalEndTimeZone")]++
			if str(e, "iCalUId") != "" {
				ical++
			}
			if str(e, "seriesMasterId") != "" {
				master++
			}
			if masterID == "" && str(e, "type") == "seriesMaster" {
				masterID = str(e, "id")
			}
			if masterID == "" && str(e, "seriesMasterId") != "" {
				masterID = str(e, "seriesMasterId")
			}
			if e["isAllDay"] == true && allDayShown < 2 {
				allDayShown++
				s, en := obj(e, "start"), obj(e, "end")
				l.f("2 all-day start=%v/%q end=%v/%q originalTZ=%q/%q", s["dateTime"], str(s, "timeZone"),
					en["dateTime"], str(en, "timeZone"), str(e, "originalStartTimeZone"), str(e, "originalEndTimeZone"))
			}
		}
		l.f("2 type counts (type @odata.type): %v", types)
		l.f("2 originalStartTimeZone|originalEndTimeZone seen: %v", tzs)
		l.f("2 iCalUId present on %d, seriesMasterId on %d of %d", ical, master, len(evs))
		if len(evs) > 0 {
			l.f("2 event keys=%s", keysOf(evs[0]))
			s := obj(evs[0], "start")
			l.f("2 first event start shape dateTime=%v timeZone=%q", s["dateTime"], str(s, "timeZone"))
		}
	}()

	// 3. Delta per non-default calendar (D14).
	for _, c := range cals {
		if c["isDefaultCalendar"] == true || str(c, "id") == "" {
			continue
		}
		name := str(c, "name")
		evs, pages, dl, err := l.delta("/me/calendars/" + url.PathEscape(str(c, "id")) + "/calendarView/delta?" + window)
		if err != nil {
			l.fail(fmt.Sprintf("3 calendar %q delta", name), err)
			continue
		}
		l.f("3 calendar %q delta ok events=%d pages=%d deltaLink=%v", name, len(evs), pages, dl)
	}

	// 4. Series master.
	func() {
		if masterID == "" {
			l.f("4 no seriesMaster or occurrence found in the next 30 days; skipped")
			return
		}
		var e map[string]any
		if err := l.c.Get(ctx, "/me/events/"+url.PathEscape(masterID), &e); err != nil {
			l.fail("4 /me/events/{master}", err)
			return
		}
		r := obj(e, "recurrence")
		p, rg := obj(r, "pattern"), obj(r, "range")
		l.f("4 master type=%q originalStartTimeZone=%q originalEndTimeZone=%q", str(e, "type"), str(e, "originalStartTimeZone"), str(e, "originalEndTimeZone"))
		l.f("4 recurrence pattern=%v range=%v", p, rg)
		s := obj(e, "start")
		l.f("4 master start dateTime=%v timeZone=%q", s["dateTime"], str(s, "timeZone"))
	}()

	// 5. getSchedule.
	func() {
		var me map[string]any
		if err := l.c.Get(ctx, "/me?$select=mail,userPrincipalName", &me); err != nil {
			l.fail("5 /me", err)
			return
		}
		addr := str(me, "mail")
		if addr == "" {
			addr = str(me, "userPrincipalName")
		}
		body := map[string]any{
			"schedules":                []string{addr},
			"startTime":                map[string]string{"dateTime": now.Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
			"endTime":                  map[string]string{"dateTime": now.AddDate(0, 0, 1).Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
			"availabilityViewInterval": 30,
		}
		var out map[string]any
		if err := l.c.Post(ctx, "/me/calendar/getSchedule", body, &out); err != nil {
			l.fail("5 getSchedule", err)
			return
		}
		for _, v := range list(out, "value") {
			s, _ := v.(map[string]any)
			items := list(s, "scheduleItems")
			l.f("5 getSchedule ok keys=%s availabilityView_len=%d scheduleItems=%d", keysOf(s), len(str(s, "availabilityView")), len(items))
			if len(items) > 0 {
				if it, ok := items[0].(map[string]any); ok {
					l.f("5 scheduleItem keys=%s status=%q", keysOf(it), str(it, "status"))
				}
			}
			if wh := obj(s, "workingHours"); wh != nil {
				l.f("5 workingHours keys=%s", keysOf(wh))
			}
		}
	}()

	// 6. Meeting requests in mail.
	func() {
		var out map[string]any
		if err := l.c.Get(ctx, "/me/messages?$select=id,subject&$orderby=receivedDateTime%20desc&$top=50", &out); err != nil {
			l.fail("6 /me/messages", err)
			return
		}
		types := map[string]int{}
		var first string
		for _, v := range list(out, "value") {
			m, _ := v.(map[string]any)
			ot := str(m, "@odata.type")
			types[ot]++
			if first == "" && ot == "#microsoft.graph.eventMessageRequest" {
				first = str(m, "id")
			}
		}
		l.f("6 last 50 messages @odata.type counts: %v", types)
		if first == "" {
			l.f("6 no eventMessageRequest among the last 50 messages; skipped")
			return
		}
		id := url.PathEscape(first)
		rc, err := l.c.GetRaw(ctx, "/me/messages/"+id+"/$value")
		if err != nil {
			l.fail("6 message $value", err)
		} else {
			raw, _ := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			var cts []string
			for _, ln := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
				if len(ln) > 13 && strings.EqualFold(ln[:13], "content-type:") {
					cts = append(cts, strings.TrimSpace(ln[13:]))
				}
			}
			cal := false
			for _, ct := range cts {
				if strings.Contains(strings.ToLower(ct), "text/calendar") {
					cal = true
					l.f("6 MIME has text/calendar part: %q", ct)
				}
			}
			l.f("6 MIME content types (top-level and parts): %q; text/calendar part=%v", cts, cal)
		}
		var withEvent map[string]any
		if err := l.c.Get(ctx, "/me/messages/"+id+"?$expand=microsoft.graph.eventMessage/event", &withEvent); err != nil {
			l.fail("6 message $expand event", err)
			return
		}
		l.f("6 message keys=%s", keysOf(withEvent))
		if ev := obj(withEvent, "event"); ev != nil {
			l.f("6 expanded event keys=%s", keysOf(ev))
		} else {
			l.f("6 expanded event absent")
		}
	}()

	l.t.Logf("SUMMARY: %d findings, %d failed steps (see FAILED lines; 403 ErrorAccessDenied = missing Calendars.Read/Mail.Read scope, compare with step 7 scp)", l.n, l.bad)
}

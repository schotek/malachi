// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestPutDelete(t *testing.T) {
	for _, p := range []caldavtest.Profile{caldavtest.Nextcloud, caldavtest.ICloud, caldavtest.Fastmail, caldavtest.Radicale} {
		t.Run(string(p), func(t *testing.T) {
			s := caldavtest.New(t, p)
			c := newClient(t, s)
			info, err := c.ListCalendars(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			cal := info.Calendars[0].Href
			uid := NewUID()
			href := ObjectHref(cal, uid)
			s.ResetRequests()
			etag, err := c.Put(t.Context(), href, event(uid, t0), "")
			if err != nil {
				t.Fatal(err)
			}
			if _, server, _ := s.Object(href); etag == "" || etag != server {
				t.Fatalf("etag %q, server %q", etag, server)
			}
			reqs := s.Requests()
			if reqs[0].Header.Get("If-None-Match") != "*" || reqs[0].Header.Get("If-Match") != "" {
				t.Fatalf("create headers %v", reqs[0].Header)
			}
			if p == caldavtest.ICloud && (len(reqs) != 2 || reqs[1].Method != http.MethodGet) {
				t.Fatalf("no ETag on PUT must be followed by a GET: %d requests", len(reqs))
			}

			// Creating it again conflicts.
			if _, err := c.Put(t.Context(), href, event(uid, t0), ""); !IsConflict(err) || code(err) != api.CodeConflict {
				t.Fatalf("second create: %v", err)
			}
			etag2, err := c.Put(t.Context(), href, event(uid, t0.Add(time.Hour)), etag)
			if err != nil || etag2 == etag {
				t.Fatalf("update: %q %v", etag2, err)
			}
			// The old version no longer matches.
			if _, err := c.Put(t.Context(), href, event(uid, t0), etag); !IsConflict(err) {
				t.Fatalf("stale update: %v", err)
			}
			if err := c.Delete(t.Context(), href, etag); !IsConflict(err) {
				t.Fatalf("stale delete: %v", err)
			}
			o, err := c.Get(t.Context(), href)
			if err != nil || o.ETag != etag2 || !strings.Contains(string(o.Data), "UID:"+uid) {
				t.Fatalf("get %+v %v", o, err)
			}
			if err := c.Delete(t.Context(), href, etag2); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(t.Context(), href, ""); !IsGone(err) || code(err) != api.CodeEventGone {
				t.Fatalf("delete of a deleted object: %v", err)
			}
			if _, err := c.Get(t.Context(), href); !IsGone(err) {
				t.Fatalf("get of a deleted object: %v", err)
			}
		})
	}
}

func TestPutReadOnly(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := newClient(t, s)
	href := ObjectHref(s.CalendarHref("team-shared_by_petr"), NewUID())
	_, err := c.Put(t.Context(), href, event("x", t0), "")
	if !IsForbidden(err) || code(err) != api.CodeReadOnly {
		t.Fatalf("read-only calendar: %v", err)
	}
	var se *StatusError
	if !asStatus(err, &se) || se.Precondition != "need-privileges" {
		t.Fatalf("precondition: %+v", se)
	}
}

func asStatus(err error, se **StatusError) bool {
	e, ok := err.(*StatusError)
	*se = e
	return ok
}

func TestPutRefusesBadInput(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	c := newClient(t, s)
	cal := s.CalendarHref("personal")
	if _, err := c.Put(t.Context(), cal+"a.ics", nil, ""); code(err) != api.CodeInvalidArgument {
		t.Fatalf("empty: %v", err)
	}
	if _, err := c.Put(t.Context(), cal+"a.ics", make([]byte, api.MaxICSBytes+1), ""); code(err) != api.CodeInvalidArgument {
		t.Fatalf("too big: %v", err)
	}
	if _, err := c.Put(t.Context(), cal+"a.ics", event("a", t0), "\"x\"\n"); code(err) != api.CodeInvalidArgument {
		t.Fatalf("bad etag: %v", err)
	}
	if _, err := c.Put(t.Context(), "https://evil.test/a.ics", event("a", t0), ""); err != ErrOtherOrigin {
		t.Fatalf("other origin: %v", err)
	}
	if s.EvilRequests() != 0 {
		t.Fatal("request to another origin")
	}
}

func TestNewUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		u := NewUID()
		if !re.MatchString(u) || seen[u] {
			t.Fatalf("uid %q", u)
		}
		seen[u] = true
	}
	if got := ObjectHref("/cal", "a b/c"); got != "/cal/a%20b%2Fc.ics" {
		t.Fatalf("href %q", got)
	}
}

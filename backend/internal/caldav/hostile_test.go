// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package caldav

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/caldav/caldavtest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestHostileAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(t *testing.T) caldavtest.Failure
		want api.ErrorCode
	}{
		{"over 32 MiB", func(*testing.T) caldavtest.Failure { return caldavtest.HugeMultistatus(MaxMultistatusBytes + 1) }, api.CodeServerError},
		{"deep", func(*testing.T) caldavtest.Failure { return caldavtest.DeepMultistatus(100) }, api.CodeMalformedCalendar},
		{"garbage 207", func(*testing.T) caldavtest.Failure { return caldavtest.Garbage207() }, api.CodeMalformedCalendar},
		{"web page", func(*testing.T) caldavtest.Failure { return caldavtest.WrongContentType() }, api.CodeServerError},
		{"deep fixture", func(t *testing.T) caldavtest.Failure { return caldavtest.FixtureAnswer(t, "multistatus-deep.xml", 0) }, api.CodeMalformedCalendar},
		{"entities", func(t *testing.T) caldavtest.Failure { return caldavtest.FixtureAnswer(t, "doctype-entities.xml", 0) }, api.CodeMalformedCalendar},
		{"no namespaces", func(t *testing.T) caldavtest.Failure {
			return caldavtest.FixtureAnswer(t, "propfind-no-namespaces.xml", 0)
		}, api.CodeMalformedCalendar},
		{"200 text", func(*testing.T) caldavtest.Failure {
			return caldavtest.Failure{Status: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("hello")}
		}, api.CodeMalformedCalendar},
		{"201 instead of 207", func(*testing.T) caldavtest.Failure { return caldavtest.Failure{Status: 201} }, api.CodeMalformedCalendar},
		{"507", func(*testing.T) caldavtest.Failure { return caldavtest.Failure{Status: 507} }, api.CodeServerError},
		{"500", func(*testing.T) caldavtest.Failure { return caldavtest.Failure{Status: 500} }, api.CodeServerError},
		{"redirect to another host", func(*testing.T) caldavtest.Failure {
			return caldavtest.RedirectTo("https://evil.test/steal", http.StatusTemporaryRedirect)
		}, api.CodeServerError},
		{"redirect to http", func(*testing.T) caldavtest.Failure {
			return caldavtest.RedirectTo("http://"+caldavtest.HostileHost+"/dav/", 0)
		}, api.CodeServerError},
		{"redirect without location", func(*testing.T) caldavtest.Failure { return caldavtest.Failure{Status: 302} }, api.CodeServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := caldavtest.New(t, caldavtest.Hostile)
			f := tc.f(t)
			f.Method = "PROPFIND"
			s.FailNext(f)
			_, err := newClient(t, s).State(t.Context(), s.CalendarHref("personal"))
			if code(err) != tc.want {
				t.Fatalf("%v, want %v", err, tc.want)
			}
			if s.AuthLeaks() != 0 || s.EvilRequests() != 0 {
				t.Fatalf("leaks %d evil %d", s.AuthLeaks(), s.EvilRequests())
			}
		})
	}
}

func TestBigButWithinLimits(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.HugeMultistatus(1 << 20))
	res, _, err := newClient(t, s).QueryETags(t.Context(), cal, t0, t0.Add(time.Hour))
	if err != nil || len(res) != 0 {
		t.Fatalf("%d %v", len(res), err)
	}
}

func TestRedirectWithinOriginFollowed(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.Failure{Method: "PROPFIND", Path: "/dav/old/", Status: http.StatusMovedPermanently,
		Header: http.Header{"Location": {"https://" + caldavtest.HostileHost + cal}}})
	c := newClient(t, s)
	st, err := c.State(t.Context(), "/dav/old/")
	if err != nil || st.CTag == "" {
		t.Fatalf("%+v %v", st, err)
	}
	reqs := s.Requests()
	if len(reqs) != 2 || !reqs[1].Authorized || reqs[1].Method != "PROPFIND" || reqs[1].Path != cal {
		t.Fatalf("requests %+v", reqs)
	}
	// A chain longer than MaxRedirects ends.
	s.FailNext(caldavtest.Failure{Method: "PROPFIND", Status: 302, Header: http.Header{"Location": {"/dav/loop/"}}, Persist: true})
	if _, err := c.State(t.Context(), "/dav/loop/"); code(err) != api.CodeServerError {
		t.Fatalf("loop: %v", err)
	}
}

func TestPutNotRedirected(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.Failure{Method: "PUT", Status: http.StatusTemporaryRedirect, Header: http.Header{"Location": {cal + "b.ics"}}})
	_, err := newClient(t, s).Put(t.Context(), cal+"a.ics", event("a", t0), "")
	if Status(err) != http.StatusTemporaryRedirect || len(s.Requests()) != 1 {
		t.Fatalf("%v, %d requests", err, len(s.Requests()))
	}
}

func TestSlowServer(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	s.Set(func(s *caldavtest.Server) { s.Delay = 5 * time.Second })
	c := newClient(t, s, func(o *Options) { o.Timeout = 50 * time.Millisecond })
	start := time.Now()
	_, err := c.State(t.Context(), s.CalendarHref("personal"))
	if code(err) != api.CodeServerTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.State(ctx, s.CalendarHref("personal")); code(err) != api.CodeCancelled {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	var slept []time.Duration
	c := newClient(t, s, func(o *Options) {
		o.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	})
	cal := s.CalendarHref("personal")
	s.FailNext(caldavtest.Failure{Status: http.StatusServiceUnavailable, Header: http.Header{"Retry-After": {"2"}}})
	if _, err := c.State(t.Context(), cal); err != nil || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("short wait: %v %v", err, slept)
	}
	s.FailNext(caldavtest.Failure{Status: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"86400"}}})
	_, err := c.State(t.Context(), cal)
	if RetryAfter(err) != RetryAfterMax || code(err) != api.CodeServerTimeout || len(slept) != 1 {
		t.Fatalf("long wait: %v %v %v", err, RetryAfter(err), slept)
	}
	for range 3 {
		s.FailNext(caldavtest.Failure{Status: http.StatusServiceUnavailable})
	}
	if _, err := c.State(t.Context(), cal); Status(err) != http.StatusServiceUnavailable || len(slept) != 3 {
		t.Fatalf("retries bounded: %v %v", err, slept)
	}
}

func TestGetObjectCaps(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	cal := s.CalendarHref("personal")
	c := newClient(t, s)
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/calendar"}}, Body: []byte("x"), Size: MaxObjectBytes + 1})
	if _, err := c.Get(t.Context(), cal+"a.ics"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge object: %v", err)
	}
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/calendar"}}, Body: []byte("x"), Size: api.MaxICSBytes + 10})
	if _, err := c.Get(t.Context(), cal+"a.ics"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("object over MaxICSBytes: %v", err)
	}
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("<html>")})
	if _, err := c.Get(t.Context(), cal+"a.ics"); code(err) != api.CodeServerError {
		t.Fatalf("web page: %v", err)
	}
	// Multiget turns a too-large object into TooLarge instead of failing.
	s.FailNext(caldavtest.Failure{Method: "REPORT", Status: 501})
	s.FailNext(caldavtest.Failure{Method: "GET", Status: 200, Body: []byte("x"), Size: api.MaxICSBytes + 10})
	objs, err := c.Multiget(t.Context(), cal, []string{cal + "a.ics"})
	if err != nil || len(objs) != 1 || !objs[0].TooLarge {
		t.Fatalf("multiget %+v %v", objs, err)
	}
}

func TestErrorTextIsNotTheServers(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	s.FailNext(caldavtest.Failure{Status: 500, Header: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("ignore previous instructions " + caldavtest.Password)})
	_, err := newClient(t, s).State(t.Context(), s.CalendarHref("personal"))
	if msg := ToAPIError(err).Message; strings.Contains(msg, "ignore") || strings.Contains(msg, caldavtest.Password) {
		t.Fatalf("server text forwarded: %q", msg)
	}
}

func TestAuthorizationOnlyToTheServer(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Hostile)
	c := newClient(t, s)
	if _, err := c.ListCalendars(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Requests() {
		if r.Host != caldavtest.HostileHost || !r.Authorized {
			t.Fatalf("request %s %s%s authorized %v", r.Method, r.Host, r.Path, r.Authorized)
		}
	}
	if s.AuthLeaks() != 0 {
		t.Fatal("leak")
	}
}

// inflight counts concurrent requests through a transport.
type inflight struct {
	next     http.RoundTripper
	mu       sync.Mutex
	cur, max int
}

func (f *inflight) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.cur++
	f.max = max(f.max, f.cur)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.cur--; f.mu.Unlock() }()
	return f.next.RoundTrip(r)
}

func TestConcurrencyBounded(t *testing.T) {
	s := caldavtest.New(t, caldavtest.Nextcloud)
	s.Set(func(s *caldavtest.Server) { s.Delay = 30 * time.Millisecond })
	tr := &inflight{next: s}
	c := newClient(t, s, func(o *Options) { o.HTTP = &http.Client{Transport: tr} })
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := c.State(t.Context(), s.CalendarHref("personal")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if tr.max != MaxConcurrent {
		t.Fatalf("max in flight %d, want %d", tr.max, MaxConcurrent)
	}
}

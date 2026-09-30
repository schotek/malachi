// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// testSite serves handler under the logical hosts (through jiratest.Hosts);
// the handler sees the logical host in r.Host and the logical scheme in
// X-Fake-Scheme.
func testSite(t *testing.T, handler http.HandlerFunc, hosts ...string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m := jiratest.Hosts{}
	for _, h := range hosts {
		m[h] = srv
	}
	return &http.Client{Transport: m}
}

// testClient is a datacenter client of site with a fixed token.
func testClient(t *testing.T, site string, hc *http.Client, edit ...func(*Options)) *Client {
	t.Helper()
	o := Options{SiteURL: site, Deployment: api.JiraDataCenter, HTTP: hc,
		Token: func(context.Context) (string, error) { return jiratest.Token, nil },
		Sleep: func(context.Context, time.Duration) error { return nil }}
	for _, e := range edit {
		e(&o)
	}
	c, err := NewClient(o)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func codeOf(err error) api.ErrorCode {
	if e := ToAPIError(err); e != nil {
		return e.Code
	}
	return 0
}

func TestClientAuthPerDeployment(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			r := remoteOf(f)
			me, err := r.Myself(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if me.ID != f.Me || me.Name != "Jana Dvořáková" || me.Email != jiratest.Login || me.TimeZone != "Europe/Prague" {
				t.Fatalf("myself = %+v", me)
			}
			if r.Deployment() != f.Config().Deployment || r.Gateway() {
				t.Fatalf("deployment %s, gateway %v", r.Deployment(), r.Gateway())
			}
			want := "Bearer " + jiratest.Token
			if mode == jiratest.Cloud {
				want = "Basic " + base64.StdEncoding.EncodeToString([]byte(jiratest.Login+":"+jiratest.Token))
			}
			get := f.RequestsTo(http.MethodGet, "/myself")
			if len(get) != 1 {
				t.Fatalf("myself requests = %d", len(get))
			}
			h := get[0].Header
			if get[0].Auth != want || h.Get("Accept") != "application/json" || h.Get("User-Agent") != "Malachi-Mail" ||
				h.Get("X-ExperimentalApi") != "" || h.Get("X-Atlassian-Token") != "" || h.Get("Cookie") != "" {
				t.Fatalf("GET headers = %v", h)
			}
			if _, err := r.ApproxCount(ctx, "project = 10001"); err != nil {
				t.Fatal(err)
			}
			post := f.RequestsTo(http.MethodPost, "/search")
			if len(post) != 1 {
				t.Fatalf("search requests = %d", len(post))
			}
			if h := post[0].Header; post[0].Auth != want || h.Get("X-Atlassian-Token") != "no-check" || h.Get("Content-Type") != "application/json" {
				t.Fatalf("POST headers = %v", h)
			}
		})
	}
}

func TestClientServiceDeskHeader(t *testing.T) {
	var got http.Header
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		io.WriteString(w, `{}`)
	}, "jira.acme.test")
	c := testClient(t, "https://jira.acme.test", hc, func(o *Options) { o.UserAgent = "Malachi-Mail/0.9" })
	if _, err := c.getJSON(context.Background(), "/rest/servicedeskapi/servicedesk", nil); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-ExperimentalApi") != "opt-in" || got.Get("User-Agent") != "Malachi-Mail/0.9" {
		t.Fatalf("headers = %v", got)
	}
}

func TestClientGatewayFallback(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.Cloud)
	f.Set(func(f *jiratest.Server) { f.GatewayOnly = true })
	r := remoteOf(f)
	if _, err := r.Myself(ctx); err != nil {
		t.Fatalf("scoped token through the gateway: %v", err)
	}
	if !r.Gateway() {
		t.Fatal("the gateway route is not remembered")
	}
	site, gw := 0, 0
	for _, rq := range f.RequestsTo(http.MethodGet, "/myself") {
		switch rq.Host {
		case "acme.atlassian.net":
			site++
		case jiratest.GatewayHost:
			gw++
			if !strings.HasPrefix(rq.Path, "/ex/jira/"+jiratest.CloudID+"/rest/api/3/") {
				t.Fatalf("gateway path = %s", rq.Path)
			}
		}
	}
	if site != 1 || gw != 1 {
		t.Fatalf("myself via site %d, gateway %d", site, gw)
	}
	// The next call goes straight to the gateway.
	if _, err := r.Statuses(ctx); err != nil {
		t.Fatal(err)
	}
	for _, rq := range f.RequestsTo(http.MethodGet, "/status") {
		if rq.Host != jiratest.GatewayHost {
			t.Fatalf("statuses asked the site after the switch")
		}
	}
	// A new client told to start there never asks the site.
	r2 := remoteOf(f, func(o *Options) { o.Gateway = true })
	if _, err := r2.Spaces(ctx); err != nil {
		t.Fatal(err)
	}
	for _, rq := range f.RequestsTo(http.MethodGet, "/project/search") {
		if rq.Host != jiratest.GatewayHost {
			t.Fatal("a client seeded with the gateway asked the site")
		}
	}
	// A token refused on both routes is authFailed and the route stays.
	f.Set(func(f *jiratest.Server) { f.Token = "rotated" })
	_, err := r.Myself(ctx)
	if !IsUnauthorized(err) || codeOf(err) != api.CodeAuthFailed || !r.Gateway() {
		t.Fatalf("refused token: %v (gateway %v)", err, r.Gateway())
	}
	if strings.Contains(err.Error(), jiratest.Token) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error text: %v", err)
	}
}

func TestClientGatewayBackToSite(t *testing.T) {
	f := jiratest.New(t, jiratest.Cloud)
	f.FailNext(jiratest.Failure{Host: jiratest.GatewayHost, Path: "/myself", Status: http.StatusUnauthorized})
	r := remoteOf(f, func(o *Options) { o.Gateway = true })
	if _, err := r.Myself(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Gateway() {
		t.Fatal("the route did not go back to the site")
	}
}

func TestClientNoGatewayWithoutCloudID(t *testing.T) {
	f := jiratest.New(t, jiratest.Cloud)
	f.Set(func(f *jiratest.Server) { f.GatewayOnly = true })
	r := remoteOf(f, func(o *Options) { o.CloudID = "" })
	_, err := r.Myself(context.Background())
	if !IsUnauthorized(err) || codeOf(err) != api.CodeAuthFailed {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.RequestsTo("", "/myself")); n != 1 {
		t.Fatalf("requests = %d, want the site only", n)
	}
	// A datacenter site never uses the gateway, cloud id or not.
	c := testClient(t, jiratest.DCSite, f.HTTPClient(), func(o *Options) { o.CloudID = jiratest.CloudID; o.Gateway = true })
	if c.Gateway() || c.CloudID() != strings.ToLower(jiratest.CloudID) || c.SiteURL() != jiratest.DCSite || c.Deployment() != api.JiraDataCenter {
		t.Fatalf("datacenter client: gateway %v, cloud id %q", c.Gateway(), c.CloudID())
	}
}

func TestClientRedirects(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var log []string
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.Host+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/rest/api/2/moved":
			http.Redirect(w, r, "/rest/api/2/here", http.StatusFound)
		case "/rest/api/2/here":
			if r.Header.Get("Authorization") != "Bearer "+jiratest.Token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"ok":true}`)
		case "/rest/api/2/away":
			http.Redirect(w, r, "https://login.example.test/sso?next=x", http.StatusFound)
		case "/rest/api/2/sub":
			http.Redirect(w, r, "https://files.jira.acme.test/x", http.StatusFound)
		case "/rest/api/2/plain":
			http.Redirect(w, r, "http://jira.acme.test/rest/api/2/here", http.StatusFound)
		case "/rest/api/2/loop":
			http.Redirect(w, r, "/rest/api/2/loop", http.StatusFound)
		case "/rest/api/2/empty":
			w.WriteHeader(http.StatusFound)
		case "/rest/api/2/userinfo":
			http.Redirect(w, r, "https://user:pw@jira.acme.test/rest/api/2/here", http.StatusFound)
		case "/rest/api/2/post":
			http.Redirect(w, r, "/rest/api/2/here", http.StatusTemporaryRedirect)
		case "/file":
			http.Redirect(w, r, "https://files.cdn.example.test/blob?sig=1", http.StatusSeeOther)
		case "/blob":
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, "\x89PNG\r\n\x1a\n-bytes")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, "jira.acme.test", "login.example.test", "files.jira.acme.test", "files.cdn.example.test")
	c := testClient(t, "https://jira.acme.test", hc)

	if _, err := c.getJSON(ctx, "/rest/api/2/moved", nil); err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
	for _, path := range []string{"/rest/api/2/away", "/rest/api/2/sub"} {
		_, err := c.getJSON(ctx, path, nil)
		if codeOf(err) != api.CodeServerError || !strings.Contains(err.Error(), "another host") {
			t.Fatalf("%s: %v", path, err)
		}
	}
	for path, want := range map[string]string{
		"/rest/api/2/plain":    "leaves https",
		"/rest/api/2/loop":     "too many redirects",
		"/rest/api/2/empty":    "usable location",
		"/rest/api/2/userinfo": "usable location",
	} {
		_, err := c.getJSON(ctx, path, nil)
		if codeOf(err) != api.CodeServerError || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	_, err := c.postJSON(ctx, "/rest/api/2/post", map[string]string{"jql": "x"})
	if statusOf(err) != http.StatusTemporaryRedirect || codeOf(err) != api.CodeServerError {
		t.Fatalf("POST redirect: %v", err)
	}
	// Content may leave the host, without the credentials.
	ct, err := c.OpenContent(ctx, "/file", 1<<20)
	if err != nil {
		t.Fatalf("content redirect: %v", err)
	}
	data, _ := io.ReadAll(ct.Body)
	ct.Body.Close()
	if !strings.HasSuffix(string(data), "-bytes") || ct.MediaType != "image/png" {
		t.Fatalf("content = %q, %s", data, ct.MediaType)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range log {
		host := strings.SplitN(l, "/", 2)[0]
		auth := strings.Contains(l, "auth=Bearer")
		switch {
		case host == "login.example.test":
			t.Fatalf("followed a refused redirect: %s", l)
		case host != "jira.acme.test" && auth:
			t.Fatalf("credentials sent to another host: %s", l)
		case host == "jira.acme.test" && !auth:
			t.Fatalf("credentials missing on the site: %s", l)
		}
	}
}

func TestClientHTTPSite(t *testing.T) {
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("X-Fake-Scheme") != "http":
			w.WriteHeader(http.StatusBadRequest)
		case r.URL.Path == "/jira/rest/api/2/old":
			http.Redirect(w, r, "http://jira.acme.test/jira/rest/api/2/new", http.StatusMovedPermanently)
		case r.URL.Path == "/jira/rest/api/2/new" && r.Header.Get("Authorization") != "":
			io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, "jira.acme.test")
	c := testClient(t, "http://jira.acme.test/jira", hc)
	if _, err := c.getJSON(context.Background(), "/rest/api/2/old", nil); err != nil {
		t.Fatalf("same-origin http redirect of an http site: %v", err)
	}
}

func TestClientRetryAfter(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		failure jiratest.Failure
		want    []time.Duration
		errWait time.Duration // > 0: the call fails with this RetryAfter
	}{
		{"seconds", jiratest.Failure{Status: 429, Header: map[string]string{"Retry-After": "2"}}, []time.Duration{2 * time.Second}, 0},
		{"http date", jiratest.Failure{Status: 429, Header: map[string]string{"Retry-After": now.Add(30 * time.Second).Format(http.TimeFormat)}}, []time.Duration{30 * time.Second}, 0},
		{"rate limit reset", jiratest.Failure{Status: 429, Header: map[string]string{"X-RateLimit-Reset": now.Add(10 * time.Second).Format("2006-01-02T15:04:05Z07:00")}}, []time.Duration{10 * time.Second}, 0},
		{"no header", jiratest.Failure{Status: 503}, []time.Duration{defaultThrottleWait}, 0},
		{"long wait", jiratest.Failure{Status: 429, Header: map[string]string{"Retry-After": "120"}}, nil, 120 * time.Second},
		{"exhausted", jiratest.Failure{Status: 429, Header: map[string]string{"Retry-After": "1"}, Times: 3}, []time.Duration{time.Second, time.Second}, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := jiratest.New(t, jiratest.DC)
			tc.failure.Path = "/myself"
			f.FailNext(tc.failure)
			var slept []time.Duration
			r := remoteOf(f, func(o *Options) {
				o.Now = func() time.Time { return now }
				o.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
			})
			_, err := r.Myself(ctx)
			if fmt.Sprint(slept) != fmt.Sprint(tc.want) {
				t.Fatalf("waits = %v, want %v", slept, tc.want)
			}
			if tc.errWait == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if RetryAfter(err) != tc.errWait || codeOf(err) != api.CodeServerTimeout {
				t.Fatalf("err = %v (retry after %v)", err, RetryAfter(err))
			}
		})
	}
	t.Run("cancelled wait", func(t *testing.T) {
		f := jiratest.New(t, jiratest.DC)
		f.FailNext(jiratest.Failure{Path: "/myself", Status: 429, Header: map[string]string{"Retry-After": "5"}})
		r := remoteOf(f, func(o *Options) { o.Sleep = nil })
		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(20*time.Millisecond, cancel)
		if _, err := r.Myself(cctx); codeOf(err) != api.CodeCancelled {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRetryAfterHeaders(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		h    map[string]string
		want time.Duration
	}{
		{"none", nil, 0},
		{"negative", map[string]string{"Retry-After": "-5"}, 0},
		{"huge", map[string]string{"Retry-After": "99999999999"}, retryAfterCap},
		{"past date", map[string]string{"Retry-After": now.Add(-time.Minute).Format(http.TimeFormat)}, 0},
		{"garbage", map[string]string{"Retry-After": "soon"}, 0},
		{"reset epoch", map[string]string{"X-RateLimit-Reset": fmt.Sprint(now.Add(7 * time.Second).Unix())}, 7 * time.Second},
		{"reset minutes", map[string]string{"X-RateLimit-Reset": now.Add(2 * time.Minute).Format("2006-01-02T15:04Z")}, 2 * time.Minute},
		{"reset garbage", map[string]string{"X-RateLimit-Reset": "tomorrow"}, 0},
		{"retry-after wins", map[string]string{"Retry-After": "3", "X-RateLimit-Reset": now.Add(time.Hour).Format(time.RFC3339)}, 3 * time.Second},
	} {
		h := http.Header{}
		for k, v := range tc.h {
			h.Set(k, v)
		}
		if got := retryAfter(h, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClientErrorEnvelope(t *testing.T) {
	ctx := context.Background()
	basic := base64.StdEncoding.EncodeToString([]byte(jiratest.Login + ":" + jiratest.Token))
	cases := []struct {
		name, body string
		status     int
		want       []string
		never      []string
	}{
		{"envelope", `{"errorMessages":["JQL is\u0007 bad","  "],"errors":{"jql":"field 'x' does not exist","b":"second"}}`, 400,
			[]string{"JQL is  bad", "b: second; jql: field 'x' does not exist"}, []string{"\u0007"}},
		{"token echoed", `{"errorMessages":["token ` + jiratest.Token + ` / Basic ` + basic + ` rejected"]}`, 400,
			[]string{"***"}, []string{jiratest.Token, basic}},
		{"html page", `<!DOCTYPE html><html><body><script>alert(1)</script>Oops</body></html>`, 500,
			[]string{"Internal Server Error"}, []string{"<html", "script"}},
		{"gateway", `{"code":401,"message":"Unauthorized; scope does not match"}`, 403,
			[]string{"scope does not match"}, nil},
		{"long", `{"errorMessages":["` + strings.Repeat("x", 5000) + jiratest.Token + `"]}`, 400,
			nil, []string{jiratest.Token, jiratest.Token[:6]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := jiratest.New(t, jiratest.Cloud)
			f.FailNext(jiratest.Failure{Path: "/myself", Status: tc.status, Body: tc.body})
			_, err := remoteOf(f).Myself(ctx)
			var se *StatusError
			if !errors.As(err, &se) || se.Status != tc.status {
				t.Fatalf("err = %v", err)
			}
			text := err.Error() + " | " + ToAPIError(err).Message
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("%q missing from %q", w, text)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(text, n) {
					t.Errorf("%q leaked into %q", n, text)
				}
			}
			if len(se.Message) > 250 {
				t.Errorf("message not capped: %d bytes", len(se.Message))
			}
		})
	}
}

func TestToAPIError(t *testing.T) {
	passthrough := api.NewError(api.CodeKeyringError, "keyring")
	for _, tc := range []struct {
		err  error
		want api.ErrorCode
	}{
		{&StatusError{Status: 401}, api.CodeAuthFailed},
		{&StatusError{Status: 403}, api.CodeServerError},
		{&StatusError{Status: 404}, api.CodeServerError},
		{&StatusError{Status: 429}, api.CodeServerTimeout},
		{&StatusError{Status: 503}, api.CodeServerTimeout},
		{&StatusError{Status: 504}, api.CodeServerTimeout},
		{&StatusError{Status: 500}, api.CodeServerError},
		{fmt.Errorf("wrapped: %w", &StatusError{Status: 401}), api.CodeAuthFailed},
		{passthrough, api.CodeKeyringError},
		{ErrTooLarge, api.CodeAttachmentTooBig},
		{context.Canceled, api.CodeCancelled},
		{context.DeadlineExceeded, api.CodeServerTimeout},
		{errors.New("connection reset"), api.CodeNetworkError},
	} {
		if got := codeOf(tc.err); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.err, got, tc.want)
		}
	}
	if ToAPIError(nil) != nil || ToAPIError(passthrough) != passthrough {
		t.Fatal("nil or passthrough changed")
	}
	nf := &StatusError{Status: 404}
	if !IsNotFound(nf) || IsForbidden(nf) || !IsForbidden(&StatusError{Status: 403}) || IsUnauthorized(nf) || RetryAfter(nf) != 0 || RetryAfter(errors.New("x")) != 0 {
		t.Fatal("status predicates")
	}
}

func TestClientResponseCaps(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat(`{"id":"1","name":"`+strings.Repeat("x", 1000)+`"},`, 34*1024)
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/status":
			io.WriteString(w, "["+big+`{"id":"2"}]`)
		case "/rest/api/2/myself":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html><body>Log in</body></html>")
		case "/rest/api/2/project":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "not json at all")
		case "/rest/api/2/search":
			io.WriteString(w, `{"total":3}`)
		case "/rest/api/2/issue/1/comment":
			io.WriteString(w, `{"comments":{"0":"not a list"},"total":1}`)
		}
	}, "jira.acme.test")
	r := NewRemote(testClient(t, "https://jira.acme.test", hc))
	if _, err := r.Statuses(ctx); codeOf(err) != api.CodeServerError || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized: %v", err)
	}
	if _, err := r.Myself(ctx); codeOf(err) != api.CodeServerError || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("html: %v", err)
	}
	if _, err := r.Spaces(ctx); codeOf(err) != api.CodeServerError || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("not json: %v", err)
	}
	if _, err := r.Search(ctx, SearchRequest{JQL: "project = WEB"}); codeOf(err) != api.CodeServerError {
		t.Fatalf("search without issues: %v", err)
	}
	if _, err := r.Comments(ctx, "1", 0); codeOf(err) != api.CodeServerError {
		t.Fatalf("comments that are not a list: %v", err)
	}
}

func TestClientConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int32
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		io.WriteString(w, `{"key":"JIRAUSER1","displayName":"Jana Dvořáková"}`)
	}, "jira.acme.test")
	r := NewRemote(testClient(t, "https://jira.acme.test", hc))
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Myself(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > maxConcurrent || p < 2 {
		t.Fatalf("peak concurrency = %d", p)
	}
}

func TestClientTransportErrors(t *testing.T) {
	ctx := context.Background()
	// A host the fake network does not know: networkError.
	r := NewRemote(testClient(t, "https://jira.unknown.test", &http.Client{Transport: jiratest.Hosts{}}))
	if _, err := r.Myself(ctx); codeOf(err) != api.CodeNetworkError || !strings.HasPrefix(ToAPIError(err).Message, "jira: ") {
		t.Fatalf("unknown host: %v", err)
	}
	// A certificate nobody trusts: tlsError with the details.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	r = NewRemote(testClient(t, srv.URL, nil))
	_, err := r.Myself(ctx)
	if codeOf(err) != api.CodeTLSError {
		t.Fatalf("untrusted certificate: %v", err)
	}
	if _, ok := api.TLSErrorDataOf(ToAPIError(err)); !ok {
		t.Fatal("tlsError without details")
	}
	// Cancelled and timed out.
	slow := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}, "jira.acme.test")
	r = NewRemote(testClient(t, "https://jira.acme.test", slow))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Myself(cctx); codeOf(err) != api.CodeCancelled {
		t.Fatalf("cancelled: %v", err)
	}
	tctx, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	if _, err := r.Myself(tctx); codeOf(err) != api.CodeServerTimeout && codeOf(err) != api.CodeCancelled {
		t.Fatalf("timed out: %v", err)
	}
}

func TestClientToken(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.DC)
	boom := api.NewError(api.CodeKeyringError, "keyring locked")
	r := remoteOf(f, func(o *Options) { o.Token = func(context.Context) (string, error) { return "", boom } })
	if _, err := r.Myself(ctx); codeOf(err) != api.CodeKeyringError {
		t.Fatalf("token error: %v", err)
	}
	r = remoteOf(f, func(o *Options) { o.Token = func(context.Context) (string, error) { return "", nil } })
	if _, err := r.Myself(ctx); codeOf(err) != api.CodeAuthRequired {
		t.Fatalf("empty token: %v", err)
	}
	c, err := NewClient(Options{SiteURL: jiratest.DCSite, HTTP: f.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.getJSON(ctx, "/rest/api/2/myself", nil); codeOf(err) != api.CodeAuthRequired {
		t.Fatalf("anonymous client on an authenticated call: %v", err)
	}
	if len(f.RequestsTo("", "/myself")) != 0 {
		t.Fatal("requests went out without a token")
	}
}

func TestNewClientValidation(t *testing.T) {
	tok := func(context.Context) (string, error) { return "t", nil }
	for name, o := range map[string]Options{
		"bad site":            {SiteURL: "ftp://jira.acme.test", Deployment: api.JiraDataCenter, Token: tok},
		"unknown deployment":  {SiteURL: jiratest.DCSite, Deployment: "server", Token: tok},
		"no deployment":       {SiteURL: jiratest.DCSite, Token: tok},
		"cloud without login": {SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Token: tok},
		"bad cloud id":        {SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Login: jiratest.Login, CloudID: "../../x", Token: tok},
	} {
		if _, err := NewClient(o); codeOf(err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	jar, _ := cookiejar.New(nil)
	redirect := func(*http.Request, []*http.Request) error { return nil }
	hc := &http.Client{Jar: jar, CheckRedirect: redirect}
	if _, err := NewClient(Options{SiteURL: jiratest.DCSite, Deployment: api.JiraDataCenter, Token: tok, HTTP: hc}); err != nil {
		t.Fatal(err)
	}
	if hc.Jar != jar || hc.CheckRedirect == nil {
		t.Fatal("NewClient changed the caller's http.Client")
	}
}

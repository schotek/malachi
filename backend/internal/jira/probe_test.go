// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestNormaliseSiteURL(t *testing.T) {
	ok := map[string]string{
		"acme.atlassian.net":                                    "https://acme.atlassian.net",
		"  HTTPS://Acme.Atlassian.NET/ ":                        "https://acme.atlassian.net",
		"https://acme.atlassian.net/browse/ITSD-1":              "https://acme.atlassian.net",
		"https://acme.atlassian.net/jira/software/projects/WEB": "https://acme.atlassian.net",
		"acme.atlassian.net.":                                   "https://acme.atlassian.net",
		"jira.acme.test/jira":                                   "https://jira.acme.test/jira",
		"https://jira.acme.test/jira/":                          "https://jira.acme.test/jira",
		"https://jira.acme.test/jira//":                         "https://jira.acme.test/jira",
		"https://jira.acme.test/jira/browse/WEB-1":              "https://jira.acme.test/jira",
		"https://jira.acme.test/jira/secure/Dashboard.jspa":     "https://jira.acme.test/jira",
		"https://jira.acme.test/rest/api/2/serverInfo":          "https://jira.acme.test",
		"https://jira.acme.test/login.jsp":                      "https://jira.acme.test",
		"https://JIRA.acme.test/Tracker":                        "https://jira.acme.test/Tracker",
		"http://jira.acme.test:8080/":                           "http://jira.acme.test:8080",
		"https://jira.acme.test:443":                            "https://jira.acme.test",
		"https://jira.acme.test:0443/x":                         "https://jira.acme.test/x",
		"http://jira.acme.test:80/jira":                         "http://jira.acme.test/jira",
		"https://[::1]:8443":                                    "https://[::1]:8443",
		"http://127.0.0.1:8080/jira":                            "http://127.0.0.1:8080/jira",
		"localhost:8080":                                        "https://localhost:8080",
		"jíra.acme.test":                                        "https://xn--jra-rma.acme.test",
	}
	for in, want := range ok {
		got, err := NormaliseSiteURL(in)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
			continue
		}
		if again, err := NormaliseSiteURL(got); err != nil || again != got {
			t.Errorf("not idempotent: %q -> %q -> %q (%v)", in, got, again, err)
		}
	}
	for _, in := range []string{
		"", "   ", "ftp://jira.acme.test", "javascript:alert(1)", "mailto:jana@acme.test",
		"https://user:pw@jira.acme.test", "jana@jira.acme.test", "https://jira.acme.test/?a=1", "https://jira.acme.test/#x",
		"https://jira.acme.test/a b", "https://jira.acme.test/%2e%2e/admin", "https://jira.acme.test/../admin",
		"https://jira.acme.test/./x", "https://jira.acme.test:0", "https://jira.acme.test:99999", "https://-bad-.test",
		"https://", "https://jira.acme.test\\evil", "https://jira.acme.test\x00", "https://under_score.test",
		"https://jira.acme.test\t/x", "https://" + strings.Repeat("a", 3000) + ".test",
	} {
		if got, err := NormaliseSiteURL(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		} else if codeOf(err) != api.CodeInvalidArgument {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestRealm(t *testing.T) {
	for site, want := range map[string]string{
		"https://Jira.Acme.test/Jira":  "jira.acme.test/jira",
		"https://acme.atlassian.net":   "acme.atlassian.net",
		"http://jira.acme.test:8080":   "jira.acme.test:8080",
		"https://jira.acme.test:443/x": "jira.acme.test/x",
		"":                             "",
	} {
		if got := Realm(api.JiraConfig{SiteURL: site}); got != want {
			t.Errorf("%q: %q, want %q", site, got, want)
		}
	}
}

func TestDetectSiteFake(t *testing.T) {
	ctx := context.Background()
	cloud := jiratest.New(t, jiratest.Cloud)
	res, err := DetectSite(ctx, cloud.HTTPClient(), "Acme.Atlassian.net/browse/ITSD-4")
	if err != nil {
		t.Fatal(err)
	}
	want := api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud,
		CloudID: jiratest.CloudID, Title: "Acme Jira", Version: "1001.0.0-SNAPSHOT"}
	if res != want {
		t.Fatalf("cloud = %+v", res)
	}
	for _, rq := range cloud.RequestsTo("", "") {
		if rq.Auth != "" || rq.Header.Get("Cookie") != "" {
			t.Fatalf("detection sent credentials: %+v", rq)
		}
	}

	dc := jiratest.New(t, jiratest.DC)
	res, err = DetectSite(ctx, dc.HTTPClient(), "jira.acme.test/jira/")
	if err != nil {
		t.Fatal(err)
	}
	want = api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: jiratest.DCSite, Deployment: api.JiraDataCenter,
		Title: "Acme DC Jira", Version: "9.12.4"}
	if res != want {
		t.Fatalf("dc = %+v", res)
	}
	// Without its context path the site is not found there.
	if _, err := DetectSite(ctx, dc.HTTPClient(), "jira.acme.test"); codeOf(err) != api.CodeServerError {
		t.Fatalf("no context path: %v", err)
	}
	dc.Set(func(f *jiratest.Server) { f.AnonymousBlocked = true })
	res, err = DetectSite(ctx, dc.HTTPClient(), jiratest.DCSite)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deployment != api.JiraDataCenter || res.Title != "" || res.Version != "" || res.SiteURL != jiratest.DCSite {
		t.Fatalf("anonymous blocked = %+v", res)
	}
}

func TestDetectSiteAnswers(t *testing.T) {
	ctx := context.Background()
	serverInfo := `{"version":"9.4.0","deploymentType":"Server","serverTitle":"Acme \u202eDC\u200b Jira` + "\\u0000" + `"}`
	cases := []struct {
		name    string
		typed   string
		handler http.HandlerFunc
		want    api.AccountDetectSiteResult
		code    api.ErrorCode
	}{
		{"context path by redirect", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/rest/api/2/serverInfo" {
				http.Redirect(w, r, "/tracker/rest/api/2/serverInfo", http.StatusMovedPermanently)
				return
			}
			io.WriteString(w, `{"version":"9.4.0","deploymentType":"Server"}`)
		}, api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: "https://jira.acme.test/tracker", Deployment: api.JiraDataCenter, Version: "9.4.0"}, 0},
		{"http upgraded", "http://jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Fake-Scheme") == "http" {
				http.Redirect(w, r, "https://jira.acme.test"+r.URL.Path, http.StatusMovedPermanently)
				return
			}
			io.WriteString(w, `{"version":"9.4.0"}`)
		}, api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: "https://jira.acme.test", Deployment: api.JiraDataCenter, Version: "9.4.0"}, 0},
		{"hostile title", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, serverInfo)
		}, api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: "https://jira.acme.test", Deployment: api.JiraDataCenter, Title: "Acme DC Jira", Version: "9.4.0"}, 0},
		{"cloud without tenant info", "acme.atlassian.net", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_edge/tenant_info" {
				io.WriteString(w, `{"cloudId":"../../../etc/passwd"}`)
				return
			}
			io.WriteString(w, `{"deploymentType":"cloud","serverTitle":"`+strings.Repeat("T", 1000)+`"}`)
		}, api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Title: strings.Repeat("T", maxTitleBytes)}, 0},
		{"login redirect to another host", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://sso.acme.test/login", http.StatusFound)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"html page", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html><body>Welcome to our intranet</body></html>")
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"html without content type", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.Header()["Content-Type"] = nil
			io.WriteString(w, "<html><body>{}</body></html>")
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"other JSON", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"hello":"world"}`)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"JSON array", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"version":"1"}]`)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"empty body", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"oversized", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"version":"`+strings.Repeat("9", 100<<10)+`"}`)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"401 without Jira headers", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"403 with a request id", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-AREQUESTID", "1x2x3")
			w.WriteHeader(http.StatusForbidden)
		}, api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: "https://jira.acme.test", Deployment: api.JiraDataCenter}, 0},
		{"busy", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, api.AccountDetectSiteResult{}, api.CodeServerTimeout},
		{"broken", "jira.acme.test", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}, api.AccountDetectSiteResult{}, api.CodeServerError},
		{"invalid URL", "ftp://jira.acme.test", nil, api.AccountDetectSiteResult{}, api.CodeInvalidArgument},
		{"unknown host", "nowhere.acme.test", nil, api.AccountDetectSiteResult{}, api.CodeNetworkError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sso int
			handler := tc.handler
			if handler == nil {
				handler = func(http.ResponseWriter, *http.Request) {}
			}
			hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "sso.acme.test" {
					sso++
				}
				handler(w, r)
			}, "jira.acme.test", "acme.atlassian.net", "sso.acme.test")
			res, err := DetectSite(ctx, hc, tc.typed)
			if tc.code != 0 {
				if codeOf(err) != tc.code {
					t.Fatalf("err = %v, want %v", err, tc.code)
				}
				if sso != 0 {
					t.Fatal("followed a redirect to another host")
				}
				return
			}
			if err != nil || res != tc.want {
				t.Fatalf("got %+v, %v\nwant %+v", res, err, tc.want)
			}
		})
	}
}

func TestDetectSiteTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	_, err := DetectSite(context.Background(), nil, srv.URL)
	if codeOf(err) != api.CodeTLSError {
		t.Fatalf("err = %v", err)
	}
}

func TestListSpaces(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			f.AddProject("10003", "ARCH", "archive", false)
			f.AddProject("10004", "ZET", "Zeta \u202eevil", false)
			for i := range 3 {
				f.AddIssue("ITSD", "Printer "+strconv.Itoa(i))
			}
			f.AddIssue("WEB", "Home page")
			f.AddIssue("WEB", "Old", func(is *jiratest.Issue) { is.Updated = time.Now().Add(-40 * 24 * time.Hour) })
			f.Set(func(f *jiratest.Server) { f.MaxPage = 2 })
			res, err := ListSpaces(ctx, f.HTTPClient(), f.Config(), jiratest.Token, true)
			if err != nil {
				t.Fatal(err)
			}
			if res.User.Name != "Jana Dvořáková" || res.User.Email != jiratest.Login {
				t.Fatalf("user = %+v", res.User)
			}
			var got []string
			for _, sp := range res.Spaces {
				got = append(got, sp.Key+":"+sp.Name+":"+strconv.Itoa(sp.Issues)+":"+strconv.FormatBool(sp.ServiceDesk))
			}
			want := "ARCH:archive:0:false ITSD:IT Service Desk:3:true MOB:Mobile:0:false WEB:Web:1:false ZET:Zeta evil:0:false"
			if strings.Join(got, " ") != want {
				t.Fatalf("spaces = %v", got)
			}
			var sts []string
			for _, st := range res.Statuses {
				sts = append(sts, st.ID+":"+st.Name+":"+string(st.Category))
			}
			if strings.Join(sts, ",") != "10001:Done:done,3:In Progress:inProgress,1:To Do:todo,10002:Waiting for customer:inProgress" {
				t.Fatalf("statuses = %v", sts)
			}
			counts := f.RequestsTo(http.MethodPost, "/search")
			if len(counts) != 5 {
				t.Fatalf("count requests = %d", len(counts))
			}
			for _, rq := range counts {
				if jql := jqlOf(t, rq.Body); !strings.HasPrefix(jql, "project = 1000") || !strings.HasSuffix(jql, ` AND updated >= "-30d"`) {
					t.Fatalf("count JQL = %s", jql)
				}
			}
		})
	}
}

// jqlOf is the jql of a search request body.
func jqlOf(t *testing.T, body []byte) string {
	t.Helper()
	var b struct {
		JQL string `json:"jql"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("request body %s: %v", body, err)
	}
	return b.JQL
}

func TestListSpacesCountCaps(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.Cloud)
	for i := range 120 {
		f.AddProject(strconv.Itoa(11000+i), "P"+strconv.Itoa(1000+i), "Project "+strconv.Itoa(1000+i), false)
	}
	f.FailNext(jiratest.Failure{Path: "/search/approximate-count", Status: 500, Times: 3})
	cfg := f.Config()
	cfg.OfflineDays = 7
	res, err := ListSpaces(ctx, f.HTTPClient(), cfg, jiratest.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Spaces) != 123 {
		t.Fatalf("spaces = %d", len(res.Spaces))
	}
	counted, failed := 0, 0
	for i, sp := range res.Spaces {
		switch {
		case i >= maxCountedSpaces && sp.Issues != -1:
			t.Fatalf("space %d counted", i)
		case sp.Issues >= 0:
			counted++
		case i < maxCountedSpaces:
			failed++
		}
	}
	if counted != maxCountedSpaces-3 || failed != 3 {
		t.Fatalf("counted %d, failed %d", counted, failed)
	}
	if jql := jqlOf(t, f.RequestsTo(http.MethodPost, "/approximate-count")[0].Body); !strings.HasSuffix(jql, `"-7d"`) {
		t.Fatalf("count JQL = %s", jql)
	}
	// Without counts nothing is counted.
	res, err = ListSpaces(ctx, f.HTTPClient(), cfg, jiratest.Token, false)
	if err != nil || res.Spaces[0].Issues != -1 {
		t.Fatalf("no counts: %+v, %v", res.Spaces[0], err)
	}
}

func TestListSpacesErrors(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.DC)
	cfg := f.Config()
	if _, err := ListSpaces(ctx, f.HTTPClient(), cfg, "", false); codeOf(err) != api.CodeAuthRequired {
		t.Fatalf("no token: %v", err)
	}
	if _, err := ListSpaces(ctx, f.HTTPClient(), cfg, "wrong", false); codeOf(err) != api.CodeAuthFailed {
		t.Fatalf("wrong token: %v", err)
	}
	bad := cfg
	bad.Deployment = "server"
	if _, err := ListSpaces(ctx, f.HTTPClient(), bad, jiratest.Token, false); codeOf(err) != api.CodeInvalidArgument {
		t.Fatalf("bad deployment: %v", err)
	}
	bad = cfg
	bad.SiteURL = "https://user@jira.acme.test"
	if _, err := ListSpaces(ctx, f.HTTPClient(), bad, jiratest.Token, false); codeOf(err) != api.CodeInvalidArgument {
		t.Fatalf("bad site: %v", err)
	}
	f.FailNext(jiratest.Failure{Path: "/project", Status: http.StatusForbidden})
	if _, err := ListSpaces(ctx, f.HTTPClient(), cfg, jiratest.Token, false); codeOf(err) != api.CodeServerError {
		t.Fatalf("forbidden: %v", err)
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		mode        jiratest.Mode
		gatewayOnly bool
		want        string
	}{
		{jiratest.Cloud, false, "cloud"},
		{jiratest.Cloud, true, "cloud,gateway"},
		{jiratest.DC, false, "datacenter"},
	} {
		f := jiratest.New(t, tc.mode)
		f.Set(func(f *jiratest.Server) { f.GatewayOnly = tc.gatewayOnly })
		res, err := Probe(ctx, f.HTTPClient(), f.Config(), jiratest.Token)
		if err != nil || !res.OK || strings.Join(res.Capabilities, ",") != tc.want || res.Error != nil {
			t.Fatalf("%s: %+v, %v", tc.want, res, err)
		}
	}
	f := jiratest.New(t, jiratest.Cloud)
	res, err := Probe(ctx, f.HTTPClient(), f.Config(), "wrong")
	if codeOf(err) != api.CodeAuthFailed || res.OK || res.Error == nil || res.Error.Code != api.CodeAuthFailed {
		t.Fatalf("wrong token: %+v, %v", res, err)
	}
	res, err = Probe(ctx, f.HTTPClient(), f.Config(), "")
	if codeOf(err) != api.CodeAuthRequired || res.OK || res.Error == nil {
		t.Fatalf("no token: %+v, %v", res, err)
	}
}

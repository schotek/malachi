// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package jira is the issue-tracker backend: accounts of kind jira read a
// Jira site (Cloud, REST v3, or Data Center, REST v2) like mail, beside
// internal/imap and internal/graph. The site's selected spaces and a few
// fixed views are the account's folders, every issue a thread whose
// description, comments and status or assignee changes are its messages.
//
// The remote layer (client.go, remote.go, cloud.go, datacenter.go,
// types.go) speaks to the site; the syncer builds on its Remote
// interface: supervisor.go and sync.go (one syncer per account, its state
// machine), folders.go (the views and spaces as folders), issues.go (a
// pass and the materialisation of an issue into rows), synth.go,
// images.go and events.go (the messages built from an issue), reconcile.go
// (deletions and retention), ops.go (local flags) and fetch.go
// (message.download). internal/jira/jiratest is a fake site for tests.
// This file holds what account setup needs before an account exists:
// NormaliseSiteURL, DetectSite, ListSpaces, Probe and Realm.
//
// Security stance: everything the site returns is hostile input. Every
// string is cleaned and capped before it is kept (no control characters,
// no bidirectional overrides), ids are checked for shape, a page whose
// structure is wrong is an error rather than an empty page, items that do
// not decode are dropped, JSON answers are capped at 32 MiB and HTML is
// kept only as the site's rendered HTML, for the sanitiser. The token
// goes only to the site's origin or to the Atlassian API gateway
// (api.atlassian.com, cloud with a cloud id): redirects are followed by
// the client itself, never to another host with the credentials, never
// from https to http; it is never logged and redacted from error texts.
package jira

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/idna"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// detectTimeout bounds site detection (clients give account.detectSite
	// 15 s).
	detectTimeout = 12 * time.Second
	// maxSiteURLBytes bounds what NormaliseSiteURL accepts.
	maxSiteURLBytes = 2048
	// maxInfoBytes bounds a serverInfo or tenant_info answer.
	maxInfoBytes = 64 << 10
	// ListSpaces caps: spaces returned, spaces counted, the time the
	// counting may take.
	maxListedSpaces  = 1000
	maxCountedSpaces = 100
	countBudget      = 20 * time.Second
	serverInfoPath   = "/rest/api/2/serverInfo"
)

// uiSegments start the path of a Jira web page or REST resource, never a
// context path: a pasted page link is cut before them.
var uiSegments = map[string]bool{
	"browse": true, "secure": true, "rest": true, "plugins": true,
	"servicedesk": true, "login.jsp": true,
}

// NormaliseSiteURL turns what the user typed into JiraConfig.SiteURL:
// https://host when no scheme is given; http only when typed explicitly
// (a Data Center site without TLS, a loopback test server); no user info,
// query or fragment; the host lower-cased (IDN hosts in their ASCII form)
// and the scheme's default port dropped; a Data Center context path kept
// ("https://jira.example.org/jira") but cut before the first segment
// that starts a Jira page (a pasted ".../browse/ITSD-1" link); no path at
// all for a cloud host (*.atlassian.net); no trailing slash. It is
// idempotent. Errors are invalidArgument.
func NormaliseSiteURL(raw string) (string, error) {
	bad := func(what string) (string, error) {
		return "", api.NewError(api.CodeInvalidArgument, "jira: site URL %s", what)
	}
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return bad("is empty")
	case len(s) > maxSiteURLBytes:
		return bad("is too long")
	case strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }):
		return bad("contains spaces or control characters")
	case strings.ContainsAny(s, "?#"):
		return bad("must not carry a query or fragment")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return bad("does not parse")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return bad("must be http or https")
	}
	if u.Opaque != "" || u.User != nil || u.Host == "" {
		return bad("must name a host and nothing else before the path")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	ip := strings.Contains(host, ":")
	if !ip {
		if host, err = idna.Lookup.ToASCII(host); err != nil {
			return bad("has an invalid host")
		}
	}
	if !transport.ValidHost(host) {
		return bad("has an invalid host")
	}
	hostport := host
	if ip {
		hostport = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return bad("has an invalid port")
		}
		if p = strconv.Itoa(n); p != defaultPort(u.Scheme) {
			hostport += ":" + p
		}
	}
	var segs []string
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		switch {
		case seg == "":
			continue
		case seg == "." || seg == ".." || strings.Contains(seg, "%"):
			return bad("has an invalid path")
		case uiSegments[strings.ToLower(seg)]:
		default:
			segs = append(segs, seg)
			continue
		}
		break
	}
	if strings.HasSuffix(host, ".atlassian.net") || strings.HasSuffix(host, ".jira.com") {
		segs = nil
	}
	out := u.Scheme + "://" + hostport
	if len(segs) > 0 {
		out += "/" + strings.Join(segs, "/")
	}
	return out, nil
}

// Realm is the account's uniqueness realm: the lower-cased host[:port]
// and path of the site (store.RealmOf of a jira account).
func Realm(cfg api.JiraConfig) string {
	return store.RealmOf(api.AccountConfig{Kind: api.AccountJira, Jira: &cfg})
}

// DetectSite asks the site, without credentials, what it is: GET
// /rest/api/2/serverInfo. deploymentType "Cloud" is a cloud site, whose
// cloud id is then read from /_edge/tenant_info (best effort); any other
// Jira answer is Data Center, including a site that refuses anonymous
// access (401/403 with Jira's X-Seraph-LoginReason or X-AREQUESTID
// header; title and version stay empty). A redirect on the same host is
// followed (the site URL follows it: an http site upgraded to https, a
// context path), one to another host is refused. Anything else is
// serverError "not a Jira site". Title and version are untrusted display
// text. client nil = http.DefaultClient's transport.
func DetectSite(ctx context.Context, client *http.Client, rawURL string) (api.AccountDetectSiteResult, error) {
	site, err := NormaliseSiteURL(rawURL)
	if err != nil {
		return api.AccountDetectSiteResult{}, err
	}
	c, err := NewClient(Options{SiteURL: site, HTTP: client})
	if err != nil {
		return api.AccountDetectSiteResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()
	resp, final, err := c.send(ctx, request{method: http.MethodGet, path: serverInfoPath, anonymous: true, siteOnly: true, timeout: detectTimeout}, c.siteBase+serverInfoPath, "")
	if err != nil {
		return api.AccountDetectSiteResult{}, ToAPIError(err)
	}
	defer resp.Body.Close()
	res := api.AccountDetectSiteResult{Kind: api.AccountJira, SiteURL: siteOfFinal(final, site)}
	switch {
	case resp.StatusCode == http.StatusOK:
	case (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) &&
		(resp.Header.Get("X-Seraph-LoginReason") != "" || resp.Header.Get("X-AREQUESTID") != ""):
		res.Deployment = api.JiraDataCenter
		return res, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
		return api.AccountDetectSiteResult{}, api.NewError(api.CodeServerTimeout, "jira: the site is busy (HTTP %d)", resp.StatusCode)
	default:
		return api.AccountDetectSiteResult{}, api.NewError(api.CodeServerError, "jira: not a Jira site (HTTP %d)", resp.StatusCode)
	}
	info, err := readInfo(ctx, resp)
	if err != nil {
		return api.AccountDetectSiteResult{}, err
	}
	res.Title = cleanText(string(deref(info.ServerTitle)), maxTitleBytes)
	res.Version = cleanText(string(deref(info.Version)), maxVersionBytes)
	if strings.EqualFold(strings.TrimSpace(string(deref(info.DeploymentType))), "cloud") {
		res.Deployment = api.JiraCloud
		res.CloudID = tenantCloudID(ctx, c, res.SiteURL)
	} else {
		res.Deployment = api.JiraDataCenter
	}
	return res, nil
}

func deref(s *flexString) flexString {
	if s == nil {
		return ""
	}
	return *s
}

// readInfo reads a serverInfo answer: a JSON object naming at least one
// of the fields every Jira sends.
func readInfo(ctx context.Context, resp *http.Response) (wireServerInfo, error) {
	notJira := api.NewError(api.CodeServerError, "jira: not a Jira site")
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return wireServerInfo{}, notJira
	}
	data, err := readCapped(ctx, resp, maxInfoBytes)
	if err != nil {
		return wireServerInfo{}, err
	}
	var info wireServerInfo
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") || !decodeLenient(data, &info) {
		return wireServerInfo{}, notJira
	}
	if info.Version == nil && info.DeploymentType == nil && info.ServerTitle == nil && info.BaseURL == nil && info.BuildNumber == nil {
		return wireServerInfo{}, notJira
	}
	return info, nil
}

// readCapped reads a body of at most limit bytes; more is serverError.
func readCapped(ctx context.Context, resp *http.Response, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, classifyTransport(ctx, err)
	}
	if int64(len(data)) > limit {
		return nil, api.NewError(api.CodeServerError, "jira: answer larger than %d bytes", limit)
	}
	return data, nil
}

// siteOfFinal is the site URL a detection ended on: the final URL without
// the serverInfo path, when a same-host redirect moved it; else site.
func siteOfFinal(final *url.URL, site string) string {
	if final == nil {
		return site
	}
	p, ok := strings.CutSuffix(final.EscapedPath(), serverInfoPath)
	if !ok {
		return site
	}
	s, err := NormaliseSiteURL(final.Scheme + "://" + final.Host + p)
	if err != nil {
		return site
	}
	return s
}

// tenantCloudID reads the cloud id of a cloud site; "" when the site does
// not tell.
func tenantCloudID(ctx context.Context, c *Client, site string) string {
	resp, _, err := c.send(ctx, request{method: http.MethodGet, path: "/_edge/tenant_info", anonymous: true, siteOnly: true, timeout: detectTimeout}, site+"/_edge/tenant_info", "")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := readCapped(ctx, resp, maxInfoBytes)
	if err != nil {
		return ""
	}
	var info struct {
		CloudID flexString `json:"cloudId"`
	}
	if !decodeLenient(data, &info) {
		return ""
	}
	id := strings.ToLower(strings.TrimSpace(string(info.CloudID)))
	if !cloudIDPattern.MatchString(id) {
		return ""
	}
	return id
}

// remoteFor builds the Remote of cfg with a fixed token. A missing token
// is authRequired; a bad configuration invalidArgument.
func remoteFor(client *http.Client, cfg api.JiraConfig, token string) (Remote, *Client, error) {
	if token == "" {
		return nil, nil, api.NewError(api.CodeAuthRequired, "jira: no token")
	}
	if cfg.Deployment != api.JiraCloud && cfg.Deployment != api.JiraDataCenter {
		return nil, nil, api.NewError(api.CodeInvalidArgument, "jira: unknown deployment")
	}
	c, err := NewClient(Options{
		SiteURL:    cfg.SiteURL,
		Deployment: cfg.Deployment,
		CloudID:    cfg.CloudID,
		Login:      cfg.Login,
		Token:      func(context.Context) (string, error) { return token, nil },
		HTTP:       client,
	})
	if err != nil {
		return nil, nil, err
	}
	return NewRemote(c), c, nil
}

// ListSpaces signs in and lists what the account settings choose from:
// the user, the spaces (by name, at most 1000) and the statuses (by
// name). With counts, the first 100 spaces get an estimate of their
// issues updated within cfg.OfflineDays (0 = 30), four at a time and 20 s
// in all; the others, and any count that failed, are -1. Errors are
// *api.Error: authRequired without a token, authFailed when the site
// refuses it.
func ListSpaces(ctx context.Context, client *http.Client, cfg api.JiraConfig, token string, counts bool) (api.AccountListSpacesResult, error) {
	r, _, err := remoteFor(client, cfg, token)
	if err != nil {
		return api.AccountListSpacesResult{}, err
	}
	me, err := r.Myself(ctx)
	if err != nil {
		return api.AccountListSpacesResult{}, ToAPIError(err)
	}
	spaces, err := r.Spaces(ctx)
	if err != nil {
		return api.AccountListSpacesResult{}, ToAPIError(err)
	}
	statuses, err := r.Statuses(ctx)
	if err != nil {
		return api.AccountListSpacesResult{}, ToAPIError(err)
	}
	sort.SliceStable(spaces, func(i, j int) bool {
		a, b := strings.ToLower(spaces[i].Name), strings.ToLower(spaces[j].Name)
		if a != b {
			return a < b
		}
		return spaces[i].Key < spaces[j].Key
	})
	if len(spaces) > maxListedSpaces {
		spaces = spaces[:maxListedSpaces]
	}
	res := api.AccountListSpacesResult{
		User:     api.SiteUser{Name: me.Name, Email: me.Email},
		Spaces:   make([]api.Space, len(spaces)),
		Statuses: make([]api.IssueStatus, 0, len(statuses)),
	}
	for i, sp := range spaces {
		res.Spaces[i] = api.Space{ID: sp.ID, Key: sp.Key, Name: sp.Name, ServiceDesk: sp.ServiceDesk, Issues: -1}
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return strings.ToLower(statuses[i].Name) < strings.ToLower(statuses[j].Name)
	})
	for _, st := range statuses {
		res.Statuses = append(res.Statuses, api.IssueStatus{ID: st.ID, Name: st.Name, Category: st.Category})
	}
	if counts {
		days := cfg.OfflineDays
		if days <= 0 {
			days = api.DefaultJiraOfflineDays
		}
		countSpaces(ctx, r, res.Spaces[:min(len(res.Spaces), maxCountedSpaces)], min(days, api.MaxJiraOfflineDays))
	}
	return res, nil
}

// countSpaces fills Issues of spaces with ApproxCount of the issues
// updated within days, maxConcurrent at a time and within countBudget;
// a failed or unfinished count stays -1.
func countSpaces(ctx context.Context, r Remote, spaces []api.Space, days int) {
	ctx, cancel := context.WithTimeout(ctx, countBudget)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	for i := range spaces {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(sp *api.Space) {
			defer wg.Done()
			defer func() { <-sem }()
			jql := "project = " + JQLValue(sp.ID) + ` AND updated >= "-` + strconv.Itoa(days) + `d"`
			if n, err := r.ApproxCount(ctx, jql); err == nil {
				sp.Issues = n
			}
		}(&spaces[i])
	}
	wg.Wait()
}

// Probe checks that the token opens the site (GET myself) for
// account.test. The result's capabilities are the deployment ("cloud" or
// "datacenter") and "gateway" when the request went through the
// Atlassian API gateway. On failure the error is returned and also set in
// the result (OK false).
func Probe(ctx context.Context, client *http.Client, cfg api.JiraConfig, token string) (api.EndpointTestResult, error) {
	fail := func(err error, latency time.Duration) (api.EndpointTestResult, error) {
		e := ToAPIError(err)
		return api.EndpointTestResult{Error: e, LatencyMS: int(latency / time.Millisecond)}, e
	}
	r, c, err := remoteFor(client, cfg, token)
	if err != nil {
		return fail(err, 0)
	}
	start := time.Now()
	_, err = r.Myself(ctx)
	latency := time.Since(start)
	if err != nil {
		return fail(err, latency)
	}
	caps := []string{string(cfg.Deployment)}
	if c.Gateway() {
		caps = append(caps, "gateway")
	}
	return api.EndpointTestResult{OK: true, Capabilities: caps, LatencyMS: int(latency / time.Millisecond)}, nil
}

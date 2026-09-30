// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package jiratest is a fake Jira site for tests: internal/jira's own
// (client, remote layer, syncer) and those of packages that drive a Jira
// account end to end (internal/core). It speaks the subset of the REST API
// internal/jira uses, in both flavours, and never touches the network.
// Test data is fictional: the users Jana Dvořáková and Petr Svoboda of
// Acme, the spaces ITSD, WEB and MOB.
package jiratest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Server is an in-memory Jira site behind the subset of the REST API
// internal/jira uses, in either flavour (Cloud: v3, search/jql with page
// tokens, bulk fetches, the API gateway route and a media host for
// attachment bytes; DC: v2, startAt paging, a context path,
// expand=changelog). Tests reach it through HTTPClient(), whose transport
// maps the logical hosts (acme.atlassian.net, api.atlassian.com,
// api.media.atlassian.com, jira.acme.test, and OffSiteHost) onto local
// httptest servers and refuses every other host: no test ever touches the
// network.
//
// Build state with the builder methods (AddIssue, AddComment, SetStatus,
// Assign, AddAttachment, EditComment, SetWatching, SetTransitions,
// AddSiteFile, ...);
// change it later only through them or Update, which hold the lock and
// bump the issue's updated time. Knobs, set through Set: GatewayOnly (a
// scoped token the site refuses), AnonymousBlocked (a DC site without
// anonymous access), Lagging (issues changed while set stay out of
// searches until Reindex, or until named in reconcileIssues), MaxPage (the
// server returns smaller pages than asked); FailNext queues failures
// (401/403/404/429 with Retry-After, HTML pages...). The clock (Now, set
// through Set) stamps every change and anchors relative JQL times ("-5m");
// the JQL the fake understands is described in jql.go. A client that
// snapshots Token keeps it: changing Token afterwards simulates a revoked
// or rotated token.
type Server struct {
	t     testing.TB
	mode  Mode
	srv   *httptest.Server // the site and the gateway
	media *httptest.Server // cloud attachment bytes
	Site  *url.URL         // logical site URL

	CloudID string
	Login   string
	Token   string
	Me      string // user id of the token's user (Jana Dvořáková)
	Petr    string // another user (Petr Svoboda)

	mu               sync.Mutex
	Now              func() time.Time
	users            map[string]*User
	projects         []*Project
	statuses         []*Status
	issues           map[string]*Issue
	seq              int
	keySeq           map[string]int
	GatewayOnly      bool
	AnonymousBlocked bool
	Lagging          bool
	MaxPage          int
	failures         []*Failure
	requests         []Request
	mediaAuthLeaks   int
	siteFiles        map[string]siteFile // route-relative path → file
	offSite          *httptest.Server
	offSiteRequests  int
}

// siteFile is a file the site serves at a path of its own (AddSiteFile).
type siteFile struct {
	contentType string
	data        []byte
}

// Mode is the flavour of the site: Cloud (REST v3) or DC (Data Center,
// REST v2).
type Mode string

const (
	Cloud Mode = "cloud"
	DC    Mode = "dc"
)

const (
	CloudSite   = "https://acme.atlassian.net"
	DCSite      = "https://jira.acme.test/jira"
	GatewayHost = "api.atlassian.com"
	MediaHost   = "api.media.atlassian.com"
	// OffSiteHost is a host outside the site that rendered HTML may point
	// at; it serves a picture for any path and counts the requests
	// (OffSiteRequests), which a client must never make.
	OffSiteHost = "cdn.elsewhere.test"
	CloudID     = "3f1c2b7a-5d4e-4c2b-9a8f-1e2d3c4b5a69"
	Login       = "jana.dvorakova@acme.test"
	Token       = "tok-Secret-4242"
	TimeLayout  = "2006-01-02T15:04:05.000-0700"
)

// User is a user of the site.
type User struct {
	ID      string // accountId (cloud) or key (dc)
	Name    string // dc user name
	Display string
	Email   string
	TZ      string
}

// Project is a space (a Jira project).
type Project struct {
	ID, Key, Name string
	ServiceDesk   bool
}

// Status is a workflow status.
type Status struct {
	ID, Name string
	Category string // new | indeterminate | done
}

// Issue is an issue; change it only through the Server's builders (or
// in their edit functions), which hold the lock.
type Issue struct {
	ID, Key     string
	Project     string // space id
	Summary     string
	Description string // rendered HTML
	Status      string // status id
	IssueType   string
	Priority    string
	Assignee    string // user id, "" = unassigned
	Reporter    string
	Created     time.Time
	Updated     time.Time
	Watching    bool
	Indexed     bool
	Comments    []*Comment
	Histories   []*History
	Attachments []*Attachment
	// Extra replaces entries of the issue's "fields" object as sent
	// (pathological values: wrong types, huge strings...).
	Extra map[string]any
	// Transitions is the issue's workflow (nil = DefaultTransitions);
	// performed records the transitions clients performed
	// (TransitionsPerformed).
	Transitions []*Transition
	performed   []string
}

// Comment is a comment of an issue; HTML is its rendered body.
type Comment struct {
	ID        string
	Author    string
	HTML      string
	Created   time.Time
	Updated   time.Time
	JsdPublic *bool
	Props     map[string]any
	// Body is the body a client posted (PostComment): the ADF document
	// (cloud) or the wiki markup as a JSON string (dc); nil for comments
	// the test added.
	Body json.RawMessage
}

// History is a changelog entry.
type History struct {
	ID      string
	Author  string
	Created time.Time
	Items   []Item
}

// Item is one field change of a changelog entry.
type Item struct {
	Field, FieldType string
	From, FromString string
	To, ToString     string
}

// Attachment is a file attached to an issue.
type Attachment struct {
	ID, Filename, MimeType string
	Data                   []byte
	Created                time.Time
}

// Failure answers matching requests with status instead of serving
// them, times times (0 = once).
type Failure struct {
	Method string // "" = any
	Host   string // "" = any; GatewayHost fails the gateway route only
	Path   string // substring of the route-relative path ("/myself", "/comment"...)
	Status int
	Header map[string]string
	Body   string // "" = a Jira error envelope
	Times  int
	// After serves the request first and answers with the failure then:
	// the site did what was asked, the answer was lost (a timeout after a
	// POST that went through).
	After bool
}

// Request is a request the site received (RequestsTo).
type Request struct {
	Method, Host, Path, Query, Auth string
	Header                          http.Header
	Body                            []byte
}

// New starts a site of the mode with Jana Dvořáková (the token's
// user) and Petr Svoboda, the spaces ITSD (10000, a service desk), WEB
// (10001) and MOB (10002), and the statuses To Do (1), In Progress (3),
// Done (10001) and Waiting for customer (10002).
func New(t testing.TB, mode Mode) *Server {
	t.Helper()
	f := &Server{
		t: t, mode: mode, CloudID: CloudID, Login: Login, Token: Token,
		Now: time.Now, users: map[string]*User{}, issues: map[string]*Issue{},
		keySeq: map[string]int{}, seq: 20000, siteFiles: map[string]siteFile{},
	}
	site := CloudSite
	if mode == DC {
		site = DCSite
		f.AddUser(&User{ID: "JIRAUSER10100", Name: "jana.dvorakova", Display: "Jana Dvořáková", Email: Login, TZ: "Europe/Prague"})
		f.AddUser(&User{ID: "JIRAUSER10101", Name: "petr.svoboda", Display: "Petr Svoboda", Email: "petr.svoboda@acme.test", TZ: "Europe/Prague"})
		f.Me, f.Petr = "JIRAUSER10100", "JIRAUSER10101"
	} else {
		f.AddUser(&User{ID: "5b10ac8d82e05b22cc7d4ef5", Display: "Jana Dvořáková", Email: Login, TZ: "Europe/Prague"})
		f.AddUser(&User{ID: "712020:0c9f3c3e-7a9c-4b6e-9d55-3b1f0e8a2c41", Display: "Petr Svoboda", TZ: "Europe/Prague"})
		f.Me, f.Petr = "5b10ac8d82e05b22cc7d4ef5", "712020:0c9f3c3e-7a9c-4b6e-9d55-3b1f0e8a2c41"
	}
	f.Site, _ = url.Parse(site)
	f.projects = []*Project{
		{ID: "10000", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true},
		{ID: "10001", Key: "WEB", Name: "Web", ServiceDesk: false},
		{ID: "10002", Key: "MOB", Name: "Mobile", ServiceDesk: false},
	}
	f.statuses = []*Status{
		{ID: "1", Name: "To Do", Category: "new"},
		{ID: "3", Name: "In Progress", Category: "indeterminate"},
		{ID: "10001", Name: "Done", Category: "done"},
		{ID: "10002", Name: "Waiting for customer", Category: "indeterminate"},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	f.media = httptest.NewServer(http.HandlerFunc(f.handleMedia))
	t.Cleanup(f.media.Close)
	f.offSite = httptest.NewServer(http.HandlerFunc(f.handleOffSite))
	t.Cleanup(f.offSite.Close)
	t.Cleanup(func() {
		if f.mediaAuthLeaks > 0 {
			t.Errorf("the media host got an Authorization header %d times", f.mediaAuthLeaks)
		}
	})
	return f
}

// Builders.

// AddUser adds (or replaces) a user.
func (f *Server) AddUser(u *User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = u
}

// AddProject adds a space.
func (f *Server) AddProject(id, key, name string, serviceDesk bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects = append(f.projects, &Project{ID: id, Key: key, Name: name, ServiceDesk: serviceDesk})
}

func (f *Server) nextID() string {
	f.seq++
	return strconv.Itoa(f.seq)
}

// AddIssue creates an issue in the space with the key: status To Do, type
// Task, priority Medium, reported by Jana, created and updated now. edit
// runs before it is stored.
func (f *Server) AddIssue(spaceKey, summary string, edit ...func(*Issue)) *Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projectLocked(spaceKey)
	if p == nil {
		f.t.Fatalf("fake jira: no space %s", spaceKey)
	}
	f.keySeq[p.Key]++
	now := f.Now()
	is := &Issue{
		ID: f.nextID(), Key: fmt.Sprintf("%s-%d", p.Key, f.keySeq[p.Key]), Project: p.ID, Summary: summary,
		Status: "1", IssueType: "Task", Priority: "Medium", Reporter: f.Me,
		Created: now, Updated: now, Indexed: !f.Lagging,
	}
	for _, e := range edit {
		e(is)
	}
	f.issues[is.ID] = is
	return is
}

// Update changes an issue and bumps its updated time.
func (f *Server) Update(id string, edit func(*Issue)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(id)
	edit(is)
	f.touchLocked(is, f.Now())
}

// touchLocked marks an issue changed at now (index lag applied).
func (f *Server) touchLocked(is *Issue, now time.Time) {
	is.Updated = now
	if f.Lagging {
		is.Indexed = false
	}
}

func (f *Server) mustIssue(id string) *Issue {
	is, ok := f.issues[id]
	if !ok {
		f.t.Fatalf("fake jira: no issue %s", id)
	}
	return is
}

// AddComment adds a comment by author (a user id) with the rendered HTML.
func (f *Server) AddComment(issueID, author, html string, edit ...func(*Comment)) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	now := f.Now()
	c := &Comment{ID: f.nextID(), Author: author, HTML: html, Created: now, Updated: now}
	for _, e := range edit {
		e(c)
	}
	is.Comments = append(is.Comments, c)
	f.touchLocked(is, now)
	return c.ID
}

// AddHistory adds a changelog entry.
func (f *Server) AddHistory(issueID, author string, items ...Item) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	now := f.Now()
	h := &History{ID: f.nextID(), Author: author, Created: now, Items: items}
	is.Histories = append(is.Histories, h)
	f.touchLocked(is, now)
	return h.ID
}

// SetStatus moves the issue to the status, as user by did.
func (f *Server) SetStatus(issueID, statusID, by string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	from, to := f.statusLocked(is.Status), f.statusLocked(statusID)
	is.Status = statusID
	now := f.Now()
	is.Histories = append(is.Histories, &History{ID: f.nextID(), Author: by, Created: now, Items: []Item{{
		Field: "status", FieldType: "jira", From: from.ID, FromString: from.Name, To: to.ID, ToString: to.Name,
	}}})
	f.touchLocked(is, now)
}

// Assign assigns the issue to user ("" = unassign), as user by did.
func (f *Server) Assign(issueID, user, by string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	it := Item{Field: "assignee", FieldType: "jira", From: is.Assignee, To: user}
	if u := f.users[is.Assignee]; u != nil {
		it.FromString = u.Display
	}
	if u := f.users[user]; u != nil {
		it.ToString = u.Display
	}
	is.Assignee = user
	now := f.Now()
	is.Histories = append(is.Histories, &History{ID: f.nextID(), Author: by, Created: now, Items: []Item{it}})
	f.touchLocked(is, now)
}

// AddAttachment attaches a file.
func (f *Server) AddAttachment(issueID, filename, mimeType string, data []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	now := f.Now()
	a := &Attachment{ID: f.nextID(), Filename: filename, MimeType: mimeType, Data: data, Created: now}
	is.Attachments = append(is.Attachments, a)
	f.touchLocked(is, now)
	return a.ID
}

// DeleteIssue removes an issue: searches skip it, direct reads get 404.
func (f *Server) DeleteIssue(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustIssue(id)
	delete(f.issues, id)
}

// EditComment replaces a comment's rendered HTML, as its author editing
// it now: the comment's and the issue's updated times move.
func (f *Server) EditComment(issueID, commentID, html string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	now := f.Now()
	for _, c := range is.Comments {
		if c.ID == commentID {
			c.HTML, c.Updated = html, now
			f.touchLocked(is, now)
			return
		}
	}
	f.t.Fatalf("fake jira: no comment %s on issue %s", commentID, issueID)
}

// DeleteComment removes a comment; the issue's updated time moves.
func (f *Server) DeleteComment(issueID, commentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(issueID)
	for i, c := range is.Comments {
		if c.ID == commentID {
			is.Comments = slices.Delete(is.Comments, i, i+1)
			f.touchLocked(is, f.Now())
			return
		}
	}
	f.t.Fatalf("fake jira: no comment %s on issue %s", commentID, issueID)
}

// SetWatching starts or stops the token's user watching the issue. As on
// a real site, the issue's updated time stays.
func (f *Server) SetWatching(issueID string, watching bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustIssue(issueID).Watching = watching
}

// AddSiteFile serves data at path (relative to the site, such as
// "/images/icons/emoticons/smile.png") to authorised GET requests, with
// the content type claimed.
func (f *Server) AddSiteFile(path, contentType string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.siteFiles[path] = siteFile{contentType: contentType, data: data}
}

// OffSiteRequests counts the requests OffSiteHost received.
func (f *Server) OffSiteRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offSiteRequests
}

// MoveIssue moves an issue to another space; it gets a key there.
func (f *Server) MoveIssue(id, spaceKey string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.mustIssue(id)
	p := f.projectLocked(spaceKey)
	f.keySeq[p.Key]++
	is.Project, is.Key = p.ID, fmt.Sprintf("%s-%d", p.Key, f.keySeq[p.Key])
	f.touchLocked(is, f.Now())
	return is.Key
}

// Reindex ends the index lag: every issue is searchable again.
func (f *Server) Reindex() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, is := range f.issues {
		is.Indexed = true
	}
}

// FailNext queues a failure.
func (f *Server) FailNext(ff Failure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ff.Times == 0 {
		ff.Times = 1
	}
	f.failures = append(f.failures, &ff)
}

// Set changes knobs under the lock.
func (f *Server) Set(edit func(f *Server)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f)
}

// RequestsTo lists the requests whose route-relative path contains part
// (method "" = any).
func (f *Server) RequestsTo(method, part string) []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Request
	for _, r := range f.requests {
		if (method == "" || r.Method == method) && strings.Contains(r.Path, part) {
			out = append(out, r)
		}
	}
	return out
}

// Client side.

// HTTPClient is the client tests hand to the package: the fake's hosts
// only.
func (f *Server) HTTPClient() *http.Client {
	hosts := Hosts{f.Site.Host: f.srv, MediaHost: f.media, OffSiteHost: f.offSite}
	if f.mode == Cloud {
		hosts[GatewayHost] = f.srv
	}
	return &http.Client{Transport: hosts}
}

// Config is a JiraConfig for the site with the three spaces.
func (f *Server) Config() api.JiraConfig {
	cfg := api.JiraConfig{SiteURL: f.Site.String(), Deployment: api.JiraDataCenter,
		Spaces: []api.SpaceRef{{ID: "10000", Key: "ITSD"}, {ID: "10001", Key: "WEB"}, {ID: "10002", Key: "MOB"}}}
	if f.mode == Cloud {
		cfg.Deployment, cfg.CloudID, cfg.Login = api.JiraCloud, f.CloudID, f.Login
	}
	return cfg
}

// TB is the test the server belongs to.
func (f *Server) TB() testing.TB { return f.t }

// Hosts is a RoundTripper that sends each logical host to its test
// server (keeping the logical Host header and scheme for the handler) and
// fails any other: tests never reach the network.
type Hosts map[string]*httptest.Server

// RoundTrip implements http.RoundTripper.
func (h Hosts) RoundTrip(req *http.Request) (*http.Response, error) {
	srv, ok := h[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("fake network: no route to host %s", req.URL.Host)
	}
	target, _ := url.Parse(srv.URL)
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
	r.Host = req.URL.Host
	r.Header.Set("X-Fake-Scheme", req.URL.Scheme)
	return http.DefaultTransport.RoundTrip(r)
}

// Server side.

func (f *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, Request{Method: r.Method, Host: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery,
		Auth: r.Header.Get("Authorization"), Header: r.Header.Clone(), Body: body})
	if f.mode == DC {
		w.Header().Set("X-AREQUESTID", "1234x5678x1")
	}
	path, gateway, ok := f.routePath(r)
	if !ok {
		f.htmlPage(w, http.StatusNotFound)
		return
	}
	for i, ff := range f.failures {
		if (ff.Method == "" || ff.Method == r.Method) && (ff.Host == "" || ff.Host == r.Host) && strings.Contains(path, ff.Path) {
			if ff.Times--; ff.Times == 0 {
				f.failures = slices.Delete(f.failures, i, i+1)
			}
			if ff.After && f.authorised(r, gateway) {
				f.route(httptest.NewRecorder(), r, path, gateway, body)
			}
			for k, v := range ff.Header {
				w.Header().Set(k, v)
			}
			if ff.Body != "" {
				w.WriteHeader(ff.Status)
				io.WriteString(w, ff.Body)
				return
			}
			f.fail(w, ff.Status, "fake failure "+strconv.Itoa(ff.Status))
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && path == "/rest/api/2/serverInfo" && !gateway:
		f.serverInfo(w)
		return
	case r.Method == http.MethodGet && path == "/_edge/tenant_info" && !gateway && f.mode == Cloud:
		f.reply(w, map[string]any{"cloudId": f.CloudID})
		return
	}
	if !f.authorised(r, gateway) {
		f.unauthorised(w, gateway)
		return
	}
	f.route(w, r, path, gateway, body)
}

// routePath strips the site's context path or the gateway prefix.
func (f *Server) routePath(r *http.Request) (string, bool, bool) {
	p := r.URL.Path
	switch r.Host {
	case GatewayHost:
		rest, ok := strings.CutPrefix(p, "/ex/jira/"+f.CloudID+"/")
		return "/" + rest, true, ok && f.mode == Cloud
	case f.Site.Host:
		if f.Site.Path == "" {
			return p, false, true
		}
		rest, ok := strings.CutPrefix(p, f.Site.Path+"/")
		return "/" + rest, false, ok
	}
	return "", false, false
}

func (f *Server) authorised(r *http.Request, gateway bool) bool {
	h := r.Header.Get("Authorization")
	if f.mode == DC {
		return h == "Bearer "+f.Token
	}
	if f.GatewayOnly && !gateway {
		return false
	}
	return h == "Basic "+base64.StdEncoding.EncodeToString([]byte(f.Login+":"+f.Token))
}

func (f *Server) unauthorised(w http.ResponseWriter, gateway bool) {
	if gateway {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"code":401,"message":"Unauthorized; scope does not match"}`)
		return
	}
	if f.mode == DC {
		w.Header().Set("X-Seraph-LoginReason", "AUTHENTICATED_FAILED")
	}
	f.fail(w, http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.")
}

func (f *Server) serverInfo(w http.ResponseWriter) {
	if f.mode == Cloud {
		f.reply(w, map[string]any{"baseUrl": CloudSite, "version": "1001.0.0-SNAPSHOT", "versionNumbers": []int{1001, 0, 0},
			"deploymentType": "Cloud", "buildNumber": 100287, "serverTitle": "Acme Jira"})
		return
	}
	if f.AnonymousBlocked {
		w.Header().Set("X-Seraph-LoginReason", "AUTHENTICATION_DENIED")
		f.fail(w, http.StatusUnauthorized, "anonymous access denied")
		return
	}
	f.reply(w, map[string]any{"baseUrl": DCSite, "version": "9.12.4", "versionNumbers": []int{9, 12, 4},
		"deploymentType": "Server", "buildNumber": 9120004, "serverTitle": "Acme DC Jira"})
}

func (f *Server) apiPrefix() string {
	if f.mode == Cloud {
		return "/rest/api/3"
	}
	return "/rest/api/2"
}

func (f *Server) route(w http.ResponseWriter, r *http.Request, path string, gateway bool, body []byte) {
	cloud := f.mode == Cloud
	if cloud && r.Method == http.MethodGet && strings.HasPrefix(path, "/rest/api/3/attachment/content/") {
		f.cloudContent(w, strings.TrimPrefix(path, "/rest/api/3/attachment/content/"))
		return
	}
	if cloud && r.Method == http.MethodGet && strings.HasPrefix(path, "/rest/api/3/attachment/thumbnail/") {
		// A thumbnail is the picture itself here.
		f.cloudContent(w, strings.TrimPrefix(path, "/rest/api/3/attachment/thumbnail/"))
		return
	}
	if !cloud && r.Method == http.MethodGet && strings.HasPrefix(path, "/secure/attachment/") {
		f.dcContent(w, strings.TrimPrefix(path, "/secure/attachment/"))
		return
	}
	if !cloud && r.Method == http.MethodGet && strings.HasPrefix(path, "/secure/thumbnail/") {
		// A thumbnail is the picture itself here.
		f.dcContent(w, strings.TrimPrefix(path, "/secure/thumbnail/"))
		return
	}
	if cloud && !gateway && r.Method == http.MethodGet {
		// The site's own attachment links (rendered HTML names them) lead
		// to the media host, like the REST content endpoint.
		for _, prefix := range []string{"/secure/attachment/", "/secure/thumbnail/"} {
			if rest, ok := strings.CutPrefix(path, prefix); ok {
				id, _, _ := strings.Cut(rest, "/")
				f.cloudContent(w, id)
				return
			}
		}
	}
	if sf, ok := f.siteFiles[path]; ok && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", sf.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(sf.data)))
		w.Write(sf.data)
		return
	}
	rest, ok := strings.CutPrefix(path, f.apiPrefix())
	if !ok {
		f.fail(w, http.StatusNotFound, "unknown resource "+path)
		return
	}
	get, post := r.Method == http.MethodGet, r.Method == http.MethodPost
	q := r.URL.Query()
	switch {
	case get && rest == "/myself":
		f.reply(w, f.userJSON(f.Me, true))
	case get && rest == "/status":
		var out []any
		for _, st := range f.statuses {
			out = append(out, f.statusJSON(st.ID))
		}
		f.reply(w, out)
	case get && rest == "/project" && !cloud:
		var out []any
		for _, p := range f.projects {
			out = append(out, projectJSON(p))
		}
		f.reply(w, out)
	case get && rest == "/project/search" && cloud:
		f.projectSearch(w, q)
	case post && rest == "/search/jql" && cloud:
		f.cloudSearch(w, body, gateway)
	case post && rest == "/search" && !cloud:
		f.dcSearch(w, body)
	case post && rest == "/search/approximate-count" && cloud:
		f.approximateCount(w, body)
	case post && rest == "/issue/bulkfetch" && cloud:
		f.bulkFetch(w, body, gateway)
	case post && rest == "/changelog/bulkfetch" && cloud:
		f.changelogBulk(w, body)
	case get && strings.HasPrefix(rest, "/issue/") && strings.HasSuffix(rest, "/comment"):
		f.comments(w, strings.TrimSuffix(strings.TrimPrefix(rest, "/issue/"), "/comment"), q)
	case post && strings.HasPrefix(rest, "/issue/") && strings.HasSuffix(rest, "/comment"):
		f.postComment(w, strings.TrimSuffix(strings.TrimPrefix(rest, "/issue/"), "/comment"), body, q)
	case get && strings.HasPrefix(rest, "/issue/") && strings.HasSuffix(rest, "/transitions"):
		f.transitions(w, strings.TrimSuffix(strings.TrimPrefix(rest, "/issue/"), "/transitions"), q)
	case post && strings.HasPrefix(rest, "/issue/") && strings.HasSuffix(rest, "/transitions"):
		f.postTransition(w, strings.TrimSuffix(strings.TrimPrefix(rest, "/issue/"), "/transitions"), body)
	case get && strings.HasPrefix(rest, "/issue/") && !strings.Contains(strings.TrimPrefix(rest, "/issue/"), "/"):
		f.issue(w, strings.TrimPrefix(rest, "/issue/"), q, gateway)
	default:
		f.fail(w, http.StatusNotFound, "unhandled "+r.Method+" "+path)
	}
}

func (f *Server) projectLocked(idOrKey string) *Project {
	for _, p := range f.projects {
		if p.ID == idOrKey || strings.EqualFold(p.Key, idOrKey) {
			return p
		}
	}
	return nil
}

func (f *Server) statusLocked(id string) Status {
	for _, st := range f.statuses {
		if st.ID == id {
			return *st
		}
	}
	return Status{ID: id, Name: id}
}

// issueByRef finds an issue by id or key.
func (f *Server) issueByRef(ref string) *Issue {
	if is, ok := f.issues[ref]; ok {
		return is
	}
	for _, is := range f.issues {
		if strings.EqualFold(is.Key, ref) {
			return is
		}
	}
	return nil
}

func (f *Server) projectSearch(w http.ResponseWriter, q url.Values) {
	start, _ := strconv.Atoi(q.Get("startAt"))
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 50
	}
	if f.MaxPage > 0 {
		size = min(size, f.MaxPage)
	}
	ps := slices.Clone(f.projects)
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	start = min(start, len(ps))
	end := min(start+size, len(ps))
	var vals []any
	for _, p := range ps[start:end] {
		vals = append(vals, projectJSON(p))
	}
	f.reply(w, map[string]any{"startAt": start, "maxResults": size, "total": len(ps), "isLast": end == len(ps), "values": orEmpty(vals)})
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

// matching evaluates JQL over the stored issues (index lag applied unless
// an issue is reconciled) and orders the result.
func (f *Server) matching(jql string, reconcile []int64) ([]*Issue, error) {
	q, err := parseJQL(jql)
	if err != nil {
		return nil, err
	}
	var out []*Issue
	for _, is := range f.issues {
		if !is.Indexed && !slices.Contains(reconcile, mustInt(is.ID)) {
			continue
		}
		if q.where == nil || q.where.eval(f, is) {
			out = append(out, is)
		}
	}
	q.sort(out)
	return out, nil
}

func mustInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func (f *Server) cloudSearch(w http.ResponseWriter, body []byte, gateway bool) {
	var req struct {
		JQL             string   `json:"jql"`
		MaxResults      int      `json:"maxResults"`
		Fields          []string `json:"fields"`
		Expand          string   `json:"expand"`
		NextPageToken   string   `json:"nextPageToken"`
		ReconcileIssues []int64  `json:"reconcileIssues"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		f.fail(w, http.StatusBadRequest, "bad body")
		return
	}
	if len(req.ReconcileIssues) > 50 {
		f.fail(w, http.StatusBadRequest, "reconcileIssues takes at most 50 ids")
		return
	}
	found, err := f.matching(req.JQL, req.ReconcileIssues)
	if err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	start := 0
	if req.NextPageToken != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(req.NextPageToken, "tok:"))
		if err != nil || !strings.HasPrefix(req.NextPageToken, "tok:") {
			f.fail(w, http.StatusBadRequest, "bad nextPageToken")
			return
		}
		start = min(n, len(found))
	}
	size := f.pageSizeOf(req.MaxResults, 50)
	end := min(start+size, len(found))
	var issues []any
	for _, is := range found[start:end] {
		issues = append(issues, f.issueJSON(is, req.Fields, req.Expand == "renderedFields", gateway))
	}
	out := map[string]any{"issues": orEmpty(issues), "isLast": end == len(found)}
	if end < len(found) {
		out["nextPageToken"] = "tok:" + strconv.Itoa(end)
	}
	f.reply(w, out)
}

func (f *Server) pageSizeOf(asked, def int) int {
	if asked <= 0 {
		asked = def
	}
	if f.MaxPage > 0 {
		asked = min(asked, f.MaxPage)
	}
	return asked
}

func (f *Server) dcSearch(w http.ResponseWriter, body []byte) {
	var req struct {
		JQL           string   `json:"jql"`
		StartAt       int      `json:"startAt"`
		MaxResults    *int     `json:"maxResults"`
		Fields        []string `json:"fields"`
		Expand        []string `json:"expand"`
		ValidateQuery *bool    `json:"validateQuery"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		f.fail(w, http.StatusBadRequest, "bad body")
		return
	}
	found, err := f.matching(req.JQL, nil)
	if err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// A validated query naming an id that does not exist fails, as Jira's.
	if req.ValidateQuery == nil || *req.ValidateQuery {
		if q, _ := parseJQL(req.JQL); q.where != nil {
			for _, id := range q.where.ids() {
				if f.issues[id] == nil {
					f.fail(w, http.StatusBadRequest, "An issue with key '"+id+"' does not exist for field 'id'.")
					return
				}
			}
		}
	}
	size := 50
	if req.MaxResults != nil {
		size = *req.MaxResults
	}
	if f.MaxPage > 0 {
		size = min(size, f.MaxPage)
	}
	start := min(max(req.StartAt, 0), len(found))
	end := min(start+size, len(found))
	var issues []any
	for _, is := range found[start:end] {
		issues = append(issues, f.issueJSON(is, req.Fields, slices.Contains(req.Expand, "renderedFields"), false))
	}
	f.reply(w, map[string]any{"startAt": start, "maxResults": size, "total": len(found), "issues": orEmpty(issues)})
}

func (f *Server) approximateCount(w http.ResponseWriter, body []byte) {
	var req struct {
		JQL string `json:"jql"`
	}
	json.Unmarshal(body, &req)
	found, err := f.matching(req.JQL, nil)
	if err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	f.reply(w, map[string]any{"count": len(found)})
}

func (f *Server) bulkFetch(w http.ResponseWriter, body []byte, gateway bool) {
	var req struct {
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		Fields         []string `json:"fields"`
		Expand         []string `json:"expand"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.IssueIDsOrKeys) > 100 {
		f.fail(w, http.StatusBadRequest, "bad bulk fetch")
		return
	}
	var issues, errs []any
	for _, ref := range req.IssueIDsOrKeys {
		if is := f.issueByRef(ref); is != nil {
			issues = append(issues, f.issueJSON(is, req.Fields, slices.Contains(req.Expand, "renderedFields"), gateway))
		} else {
			errs = append(errs, map[string]any{"id": ref, "errorMessage": "Issue does not exist or you do not have permission to see it."})
		}
	}
	f.reply(w, map[string]any{"issues": orEmpty(issues), "issueErrors": orEmpty(errs)})
}

func (f *Server) changelogBulk(w http.ResponseWriter, body []byte) {
	var req struct {
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		FieldIDs       []string `json:"fieldIds"`
		MaxResults     int      `json:"maxResults"`
		NextPageToken  string   `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.IssueIDsOrKeys) > 1000 {
		f.fail(w, http.StatusBadRequest, "bad changelog bulk fetch")
		return
	}
	type entry struct {
		issue string
		h     map[string]any
	}
	var all []entry
	for _, ref := range req.IssueIDsOrKeys {
		is := f.issueByRef(ref)
		if is == nil {
			continue
		}
		for _, h := range is.Histories {
			if j := f.historyJSON(h, req.FieldIDs); j != nil {
				all = append(all, entry{is.ID, j})
			}
		}
	}
	start := 0
	if req.NextPageToken != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(req.NextPageToken, "cl:"))
	}
	start = min(start, len(all))
	end := min(start+f.pageSizeOf(req.MaxResults, 1000), len(all))
	var logs []any
	var cur map[string]any
	for _, e := range all[start:end] {
		if cur == nil || cur["issueId"] != e.issue {
			cur = map[string]any{"issueId": e.issue, "changeHistories": []any{}}
			logs = append(logs, cur)
		}
		cur["changeHistories"] = append(cur["changeHistories"].([]any), e.h)
	}
	out := map[string]any{"issueChangeLogs": orEmpty(logs)}
	if end < len(all) {
		out["nextPageToken"] = "cl:" + strconv.Itoa(end)
	}
	f.reply(w, out)
}

func (f *Server) comments(w http.ResponseWriter, ref string, q url.Values) {
	is := f.issueByRef(ref)
	if is == nil {
		f.fail(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	cs := slices.Clone(is.Comments)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Created.Before(cs[j].Created) })
	if q.Get("orderBy") == "-created" {
		slices.Reverse(cs)
	}
	start, _ := strconv.Atoi(q.Get("startAt"))
	size, _ := strconv.Atoi(q.Get("maxResults"))
	size = f.pageSizeOf(min(max(size, 0), 100), 50)
	start = min(max(start, 0), len(cs))
	end := min(start+size, len(cs))
	expand := strings.Split(q.Get("expand"), ",")
	var out []any
	for _, c := range cs[start:end] {
		out = append(out, f.commentJSON(c, slices.Contains(expand, "renderedBody"), slices.Contains(expand, "properties")))
	}
	f.reply(w, map[string]any{"startAt": start, "maxResults": size, "total": len(cs), "comments": orEmpty(out)})
}

func (f *Server) issue(w http.ResponseWriter, ref string, q url.Values, gateway bool) {
	is := f.issueByRef(ref)
	if is == nil {
		f.fail(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	expand := strings.Split(q.Get("expand"), ",")
	var fields []string
	if v := q.Get("fields"); v != "" {
		fields = strings.Split(v, ",")
	}
	out := f.issueJSON(is, fields, slices.Contains(expand, "renderedFields"), gateway)
	if slices.Contains(expand, "changelog") {
		var hs []any
		for _, h := range is.Histories {
			hs = append(hs, f.historyJSON(h, nil))
		}
		out["changelog"] = map[string]any{"startAt": 0, "maxResults": len(hs), "total": len(hs), "histories": orEmpty(hs)}
	}
	f.reply(w, out)
}

func (f *Server) cloudContent(w http.ResponseWriter, id string) {
	if f.attachmentLocked(id) == nil {
		f.fail(w, http.StatusNotFound, "no attachment")
		return
	}
	w.Header().Set("Location", "https://"+MediaHost+"/file/"+id+"/binary?token=media-jwt&client=acme&dl=true")
	w.WriteHeader(http.StatusSeeOther)
}

func (f *Server) dcContent(w http.ResponseWriter, rest string) {
	id, _, _ := strings.Cut(rest, "/")
	a := f.attachmentLocked(id)
	if a == nil {
		f.htmlPage(w, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", a.MimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(a.Data)))
	w.Write(a.Data)
}

func (f *Server) attachmentLocked(id string) *Attachment {
	for _, is := range f.issues {
		for _, a := range is.Attachments {
			if a.ID == id {
				return a
			}
		}
	}
	return nil
}

// handleMedia serves attachment bytes as Atlassian's media service does,
// behind a signed URL, and counts any request that carries credentials.
func (f *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, Request{Method: r.Method, Host: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery,
		Auth: r.Header.Get("Authorization"), Header: r.Header.Clone()})
	if r.Header.Get("Authorization") != "" {
		f.mediaAuthLeaks++
		w.WriteHeader(http.StatusForbidden)
		return
	}
	id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/file/"), "/binary")
	a := f.attachmentLocked(id)
	if !ok || a == nil || r.URL.Query().Get("token") != "media-jwt" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", a.MimeType)
	w.Write(a.Data)
}

// handleOffSite serves a small PNG for any path and counts the request.
func (f *Server) handleOffSite(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.offSiteRequests++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "image/png")
	w.Write(PNG)
}

// PNG is a valid 1×1 picture.
var PNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0x00, 0x02, 0x00,
	0x00, 0x05, 0x00, 0x01, 0xe9, 0xfa, 0xdc, 0xd8, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
	0xae, 0x42, 0x60, 0x82,
}

// JSON of the resources.

func (f *Server) userJSON(id string, self bool) any {
	u := f.users[id]
	if u == nil {
		return nil
	}
	out := map[string]any{"displayName": u.Display, "active": true}
	if f.mode == Cloud {
		out["accountId"] = u.ID
		out["accountType"] = "atlassian"
	} else {
		out["key"], out["name"] = u.ID, u.Name
	}
	if u.Email != "" && (self || f.mode == DC) {
		out["emailAddress"] = u.Email
	}
	if self {
		out["timeZone"] = u.TZ
	}
	return out
}

func (f *Server) statusJSON(id string) any {
	st := f.statusLocked(id)
	catIDs := map[string]int{"new": 2, "indeterminate": 4, "done": 3}
	return map[string]any{"id": st.ID, "name": st.Name, "statusCategory": map[string]any{"id": catIDs[st.Category], "key": st.Category}}
}

func projectJSON(p *Project) any {
	typ := "software"
	if p.ServiceDesk {
		typ = "service_desk"
	}
	return map[string]any{"id": p.ID, "key": p.Key, "name": p.Name, "projectTypeKey": typ}
}

func jiraTime(t time.Time) string { return t.Format(TimeLayout) }

// contentURL is an attachment's content link as the route it was asked
// through names it.
func (f *Server) contentURL(a *Attachment, gateway bool) string {
	switch {
	case f.mode == DC:
		return f.Site.String() + "/secure/attachment/" + a.ID + "/" + url.PathEscape(a.Filename)
	case gateway:
		return "https://" + GatewayHost + "/ex/jira/" + f.CloudID + "/rest/api/3/attachment/content/" + a.ID
	}
	return f.Site.String() + "/rest/api/3/attachment/content/" + a.ID
}

func (f *Server) issueJSON(is *Issue, fields []string, rendered, gateway bool) map[string]any {
	if len(fields) == 1 && fields[0] == "id" && f.mode == Cloud {
		return map[string]any{"id": is.ID}
	}
	all := len(fields) == 0 || slices.Contains(fields, "*all") || slices.Contains(fields, "*navigable")
	want := func(name string) bool { return all || slices.Contains(fields, name) }
	fm := map[string]any{}
	p := f.projectLocked(is.Project)
	if want("summary") {
		fm["summary"] = is.Summary
	}
	if want("status") {
		fm["status"] = f.statusJSON(is.Status)
	}
	if want("issuetype") {
		fm["issuetype"] = map[string]any{"id": "10004", "name": is.IssueType}
	}
	if want("priority") {
		fm["priority"] = map[string]any{"id": "3", "name": is.Priority}
	}
	if want("assignee") {
		fm["assignee"] = f.userJSON(is.Assignee, false)
	}
	if want("reporter") {
		fm["reporter"] = f.userJSON(is.Reporter, false)
	}
	if want("created") {
		fm["created"] = jiraTime(is.Created)
	}
	if want("updated") {
		fm["updated"] = jiraTime(is.Updated)
	}
	if want("project") && p != nil {
		fm["project"] = projectJSON(p)
	}
	if want("attachment") {
		var as []any
		for _, a := range is.Attachments {
			as = append(as, map[string]any{"id": a.ID, "filename": a.Filename, "size": len(a.Data), "mimeType": a.MimeType,
				"created": jiraTime(a.Created), "content": f.contentURL(a, gateway), "author": f.userJSON(f.Me, false)})
		}
		fm["attachment"] = orEmpty(as)
	}
	if want("watches") {
		fm["watches"] = map[string]any{"watchCount": 1, "isWatching": is.Watching}
	}
	if want("description") {
		if f.mode == Cloud {
			fm["description"] = map[string]any{"type": "doc", "version": 1, "content": []any{
				map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "ADF is never read"}}}}}
		} else {
			fm["description"] = "h1. wiki markup is never read"
		}
	}
	for k, v := range is.Extra {
		fm[k] = v
	}
	out := map[string]any{"id": is.ID, "key": is.Key, "self": f.Site.String() + f.apiPrefix() + "/issue/" + is.ID, "fields": fm}
	if rendered && want("description") {
		out["renderedFields"] = map[string]any{"description": is.Description}
	}
	return out
}

func (f *Server) commentJSON(c *Comment, rendered, props bool) any {
	out := map[string]any{"id": c.ID, "author": f.userJSON(c.Author, false), "updateAuthor": f.userJSON(c.Author, false),
		"created": jiraTime(c.Created), "updated": jiraTime(c.Updated)}
	if f.mode == Cloud {
		out["body"] = map[string]any{"type": "doc", "version": 1, "content": []any{}}
	} else {
		out["body"] = "wiki markup is never read"
	}
	if c.JsdPublic != nil {
		out["jsdPublic"] = *c.JsdPublic
	}
	if rendered {
		out["renderedBody"] = c.HTML
	}
	if props {
		var ps []any
		keys := make([]string, 0, len(c.Props))
		for k := range c.Props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps = append(ps, map[string]any{"key": k, "value": c.Props[k]})
		}
		out["properties"] = orEmpty(ps)
	}
	return out
}

// historyJSON renders a changelog entry, with only the items of fieldIDs
// when given (nil when none is left).
func (f *Server) historyJSON(h *History, fieldIDs []string) map[string]any {
	var items []any
	for _, it := range h.Items {
		if fieldIDs != nil && !slices.Contains(fieldIDs, it.Field) {
			continue
		}
		j := map[string]any{"field": it.Field, "fieldtype": it.FieldType, "from": nullable(it.From),
			"fromString": nullable(it.FromString), "to": nullable(it.To), "toString": nullable(it.ToString)}
		if f.mode == Cloud && it.FieldType == "jira" {
			j["fieldId"] = it.Field
		}
		items = append(items, j)
	}
	if len(items) == 0 && fieldIDs != nil {
		return nil
	}
	// The bulk endpoint (fieldIDs != nil) gives created as epoch
	// milliseconds, as the real site does; expand=changelog as a string.
	var created any = jiraTime(h.Created)
	if fieldIDs != nil {
		created = h.Created.UnixMilli()
	}
	return map[string]any{"id": h.ID, "author": f.userJSON(h.Author, false), "created": created, "items": orEmpty(items)}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *Server) reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	json.NewEncoder(w).Encode(v)
}

// fail answers with Jira's error envelope.
func (f *Server) fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"errorMessages": []string{msg}, "errors": map[string]string{}})
}

func (f *Server) htmlPage(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	w.WriteHeader(status)
	io.WriteString(w, "<!DOCTYPE html><html><body><h1>Oops, you&#39;ve found a dead link.</h1></body></html>")
}

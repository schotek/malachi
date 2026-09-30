// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

var modes = []jiratest.Mode{jiratest.Cloud, jiratest.DC}

// stepClock makes the fake's clock start at start and move one minute on
// every reading, so every change gets its own time.
func stepClock(f *jiratest.Server, start time.Time) {
	t := start
	f.Set(func(f *jiratest.Server) {
		f.Now = func() time.Time { t = t.Add(time.Minute); return t }
	})
}

var t0 = time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)

func TestRemoteSearch(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			stepClock(f, t0)
			var ids []string
			for i := range 5 {
				is := f.AddIssue("WEB", fmt.Sprintf("Stránka %d", i), func(is *jiratest.Issue) {
					is.Description = fmt.Sprintf("<p>popis %d</p>", i)
					if i == 2 {
						is.Assignee, is.Watching, is.Priority, is.IssueType = f.Petr, true, "High", "Bug"
					}
				})
				ids = append(ids, is.ID)
			}
			f.AddIssue("MOB", "Jiný prostor")
			aid := f.AddAttachment(ids[4], "log.txt", "text/plain", []byte("hello"))
			r := remoteOf(f)

			var got []Issue
			complete, err := SearchAll(ctx, r, SearchRequest{JQL: "project = WEB ORDER BY updated DESC",
				IssueOptions: IssueOptions{Rendered: true}, PageSize: 2}, 0, func(p []Issue) error {
				got = append(got, p...)
				return nil
			})
			if err != nil || !complete {
				t.Fatalf("search: %v, complete %v", err, complete)
			}
			var order []string
			for _, is := range got {
				order = append(order, is.ID)
			}
			if strings.Join(order, ",") != strings.Join([]string{ids[4], ids[3], ids[2], ids[1], ids[0]}, ",") {
				t.Fatalf("order = %v (ids %v)", order, ids)
			}
			if n := len(f.RequestsTo(http.MethodPost, "/search")); n != 3 {
				t.Fatalf("pages = %d", n)
			}
			top := got[0]
			if top.Key != "WEB-5" || top.Summary != "Stránka 4" || top.DescriptionHTML != "<p>popis 4</p>" ||
				top.SpaceID != "10001" || top.SpaceKey != "WEB" || top.Status != (Status{ID: "1", Name: "To Do", Category: api.StatusCategoryTodo}) ||
				top.Type != "Task" || top.Priority != "Medium" || top.Reporter.ID != f.Me || top.Reporter.Name != "Jana Dvořáková" ||
				top.Assignee != (User{}) || top.Watching {
				t.Fatalf("issue = %+v", top)
			}
			if !top.Updated.Equal(t0.Add(7*time.Minute)) || !top.Created.Equal(t0.Add(5*time.Minute)) {
				t.Fatalf("times: created %v, updated %v", top.Created, top.Updated)
			}
			if len(top.Attachments) != 1 {
				t.Fatalf("attachments = %+v", top.Attachments)
			}
			a := top.Attachments[0]
			wantURL := jiratest.CloudSite + "/rest/api/3/attachment/content/" + aid
			if mode == jiratest.DC {
				wantURL = jiratest.DCSite + "/secure/attachment/" + aid + "/log.txt"
			}
			if a.ID != aid || a.Filename != "log.txt" || a.Size != 5 || a.MimeType != "text/plain" || a.ContentURL != wantURL || !a.Created.Equal(t0.Add(7*time.Minute)) {
				t.Fatalf("attachment = %+v", a)
			}
			mid := got[2]
			if mid.Assignee.ID != f.Petr || mid.Assignee.Name != "Petr Svoboda" || !mid.Watching || mid.Priority != "High" || mid.Type != "Bug" {
				t.Fatalf("assigned issue = %+v", mid)
			}

			page, err := r.Search(ctx, SearchRequest{JQL: "project = WEB"})
			if err != nil || len(page.Issues) != 5 || page.Issues[0].DescriptionHTML != "" || page.Next != "" {
				t.Fatalf("unrendered page: %+v, %v", page, err)
			}
			if wantTotal := map[jiratest.Mode]int{jiratest.Cloud: -1, jiratest.DC: 5}[mode]; page.Total != wantTotal {
				t.Fatalf("total = %d", page.Total)
			}
			page, err = r.Search(ctx, SearchRequest{JQL: "project = WEB", IssueOptions: IssueOptions{Fields: FieldsStamp, Rendered: true}})
			if err != nil || len(page.Issues) != 5 {
				t.Fatalf("stamps: %v", err)
			}
			if s := page.Issues[0]; s.Summary != "" || s.Updated.IsZero() || s.SpaceKey != "WEB" || s.Key == "" || s.Status.ID != "" || s.Attachments != nil {
				t.Fatalf("stamp = %+v", s)
			}
			var capped []Issue
			complete, err = SearchAll(ctx, r, SearchRequest{JQL: "project = WEB", PageSize: 2}, 3, func(p []Issue) error {
				capped = append(capped, p...)
				return nil
			})
			if err != nil || complete || len(capped) != 3 {
				t.Fatalf("capped: %d, complete %v, %v", len(capped), complete, err)
			}
			stop := errors.New("stop")
			if _, err := SearchAll(ctx, r, SearchRequest{JQL: "project = WEB"}, 0, func([]Issue) error { return stop }); !errors.Is(err, stop) {
				t.Fatalf("callback error: %v", err)
			}
			if _, err := r.Search(ctx, SearchRequest{JQL: "resolution = Unresolved"}); statusOf(err) != http.StatusBadRequest {
				t.Fatalf("JQL the site refuses: %v", err)
			}
			if _, err := r.Search(ctx, SearchRequest{JQL: "  "}); codeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("empty JQL: %v", err)
			}
		})
	}
}

func TestRemoteSearchIDs(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			stepClock(f, t0)
			want := map[string]bool{}
			for i := range 7 {
				want[f.AddIssue("ITSD", "Požadavek "+strconv.Itoa(i)).ID] = true
			}
			f.Set(func(f *jiratest.Server) { f.MaxPage = 3 })
			r := remoteOf(f)
			ids, complete, err := AllIDs(ctx, r, "project = ITSD", 0)
			if err != nil || !complete || len(ids) != 7 {
				t.Fatalf("ids = %v, complete %v, %v", ids, complete, err)
			}
			for _, id := range ids {
				if !want[id] {
					t.Fatalf("unexpected id %s", id)
				}
			}
			var body struct {
				Fields     []string `json:"fields"`
				MaxResults int      `json:"maxResults"`
			}
			json.Unmarshal(f.RequestsTo(http.MethodPost, "/search")[0].Body, &body)
			if strings.Join(body.Fields, ",") != "id" || body.MaxResults != maxIDPage {
				t.Fatalf("id search body = %+v", body)
			}
			ids, complete, err = AllIDs(ctx, r, "project = ITSD", 5)
			if err != nil || complete || len(ids) != 5 {
				t.Fatalf("capped ids = %v, complete %v, %v", ids, complete, err)
			}
		})
	}
	if _, err := remoteOf(jiratest.New(t, jiratest.DC)).SearchIDs(ctx, "project = WEB", "x"); codeOf(err) != api.CodeInvalidArgument {
		t.Fatalf("bad cursor: %v", err)
	}
}

// stuckRemote repeats its cursor forever.
type stuckRemote struct{ Remote }

func (stuckRemote) Search(context.Context, SearchRequest) (SearchPage, error) {
	return SearchPage{Issues: []Issue{{ID: "1", Key: "WEB-1"}}, Next: "again"}, nil
}

func (stuckRemote) SearchIDs(context.Context, string, Cursor) (IDPage, error) {
	return IDPage{IDs: []string{"1"}, Next: "again"}, nil
}

func TestPagingDoesNotLoop(t *testing.T) {
	ctx := context.Background()
	if _, _, err := AllIDs(ctx, stuckRemote{}, "x", 0); codeOf(err) != api.CodeServerError {
		t.Fatalf("ids: %v", err)
	}
	n := 0
	_, err := SearchAll(ctx, stuckRemote{}, SearchRequest{JQL: "x"}, 0, func(p []Issue) error { n += len(p); return nil })
	if codeOf(err) != api.CodeServerError || n != 1 {
		t.Fatalf("search: %v, %d issues", err, n)
	}
}

func TestRemoteReconcile(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.Cloud)
	f.Set(func(f *jiratest.Server) { f.Lagging = true })
	is := f.AddIssue("WEB", "Právě založeno")
	r := remoteOf(f)
	find := func(reconcile []string) bool {
		t.Helper()
		p, err := r.Search(ctx, SearchRequest{JQL: "project = WEB", Reconcile: reconcile})
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Issues) == 1 && p.Issues[0].ID == is.ID
	}
	if find(nil) {
		t.Fatal("the index lag is not simulated")
	}
	if !find([]string{"abc", "12x", is.ID, "-4"}) {
		t.Fatal("a reconciled issue is missing")
	}
	var body struct {
		ReconcileIssues []int64 `json:"reconcileIssues"`
	}
	reqs := f.RequestsTo(http.MethodPost, "/search/jql")
	json.Unmarshal(reqs[len(reqs)-1].Body, &body)
	if fmt.Sprint(body.ReconcileIssues) != "["+is.ID+"]" {
		t.Fatalf("reconcileIssues = %v", body.ReconcileIssues)
	}
	many := make([]string, 80)
	for i := range many {
		many[i] = strconv.Itoa(30000 + i)
	}
	// The cap keeps the first 50 (the fake refuses more): the issue at
	// the end is not sent.
	if find(append(many, is.ID)) {
		t.Fatal("an id past the cap reached the site")
	}
	f.Reindex()
	if !find(nil) {
		t.Fatal("reindexed issue missing")
	}
	// Data Center has no reconciliation: the ids are not sent.
	dc := jiratest.New(t, jiratest.DC)
	dis := dc.AddIssue("WEB", "x")
	if _, err := remoteOf(dc).Search(ctx, SearchRequest{JQL: "project = WEB", Reconcile: []string{dis.ID}}); err != nil {
		t.Fatal(err)
	}
	if b := string(dc.RequestsTo(http.MethodPost, "/search")[0].Body); strings.Contains(b, "reconcile") {
		t.Fatalf("datacenter body = %s", b)
	}
}

func TestRemoteBulkIssues(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			stepClock(f, t0)
			a := f.AddIssue("WEB", "První", func(is *jiratest.Issue) { is.Description = "<p>A</p>" })
			b := f.AddIssue("WEB", "Smazaná")
			c := f.AddIssue("WEB", "Přesunutá")
			f.DeleteIssue(b.ID)
			newKey := f.MoveIssue(c.ID, "MOB")
			r := remoteOf(f)
			got, err := r.BulkIssues(ctx, []string{a.ID, b.ID, c.ID, a.ID}, IssueOptions{Rendered: true})
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]Issue{}
			for _, is := range got {
				byID[is.ID] = is
			}
			if len(got) != 2 || byID[a.ID].DescriptionHTML != "<p>A</p>" || byID[c.ID].Key != newKey || byID[c.ID].SpaceKey != "MOB" {
				t.Fatalf("bulk = %+v", got)
			}
			// Wrongly typed fields cost the fields, not the issue.
			f.Update(a.ID, func(is *jiratest.Issue) {
				is.Extra = map[string]any{"summary": map[string]any{"type": "doc"}, "updated": "garbage", "status": []int{1}}
			})
			got, err = r.BulkIssues(ctx, []string{a.ID}, IssueOptions{})
			if err != nil || len(got) != 1 || got[0].Summary != "" || !got[0].Updated.IsZero() || got[0].Status != (Status{}) || got[0].Key != a.Key {
				t.Fatalf("wrongly typed fields: %+v, %v", got, err)
			}
			if _, err := r.BulkIssues(ctx, []string{"1) OR project = X"}, IssueOptions{}); codeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("hostile id: %v", err)
			}
			if got, err := r.BulkIssues(ctx, nil, IssueOptions{}); err != nil || len(got) != 0 {
				t.Fatalf("no ids: %v, %v", got, err)
			}
		})
	}
}

func TestRemoteBulkIssuesChunks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		mode     jiratest.Mode
		path     string
		requests int
	}{{jiratest.Cloud, "/issue/bulkfetch", 2}, {jiratest.DC, "/search", 3}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			f := jiratest.New(t, tc.mode)
			var ids []string
			for i := range 120 {
				ids = append(ids, f.AddIssue("ITSD", "Hromadný "+strconv.Itoa(i)).ID)
			}
			f.DeleteIssue(ids[7])
			got, err := remoteOf(f).BulkIssues(ctx, ids, IssueOptions{Fields: FieldsStamp})
			if err != nil || len(got) != 119 {
				t.Fatalf("bulk: %d, %v", len(got), err)
			}
			reqs := f.RequestsTo(http.MethodPost, tc.path)
			if len(reqs) != tc.requests {
				t.Fatalf("requests = %d", len(reqs))
			}
			if tc.mode == jiratest.DC && !strings.Contains(string(reqs[0].Body), `"validateQuery":false`) {
				t.Fatalf("body = %s", reqs[0].Body)
			}
		})
	}
	// A datacenter that refuses the query: one by one.
	f := jiratest.New(t, jiratest.DC)
	a, b := f.AddIssue("WEB", "a"), f.AddIssue("WEB", "b")
	f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/search", Status: http.StatusBadRequest})
	got, err := remoteOf(f).BulkIssues(ctx, []string{a.ID, b.ID, "999999"}, IssueOptions{})
	if err != nil || len(got) != 2 {
		t.Fatalf("fallback: %+v, %v", got, err)
	}
	if n := len(f.RequestsTo(http.MethodGet, "/rest/api/2/issue/")); n != 3 {
		t.Fatalf("single fetches = %d", n)
	}
	f.FailNext(jiratest.Failure{Method: http.MethodPost, Path: "/search", Status: http.StatusInternalServerError})
	if _, err := remoteOf(f).BulkIssues(ctx, []string{a.ID}, IssueOptions{}); statusOf(err) != 500 {
		t.Fatalf("server error: %v", err)
	}
}

func TestRemoteComments(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			stepClock(f, t0)
			is := f.AddIssue("ITSD", "Dlouhé vlákno")
			no, yes := false, true
			var ids []string
			for i := range 230 {
				author := f.Me
				if i%2 == 1 {
					author = f.Petr
				}
				ids = append(ids, f.AddComment(is.ID, author, fmt.Sprintf("<p>Komentář %d</p>", i), func(c *jiratest.Comment) {
					switch i {
					case 1:
						c.JsdPublic = &no
					case 2:
						c.JsdPublic = &yes
					case 3:
						c.Props = map[string]any{"sd.public.comment": map[string]any{"internal": true}, "malachi.outbox": map[string]any{"id": "ob_7"}}
					case 229:
						c.Updated = c.Created.Add(time.Hour)
					}
				}))
			}
			r := remoteOf(f)
			list, err := r.Comments(ctx, is.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Comments) != 230 || list.Total != 230 || list.Truncated {
				t.Fatalf("all: %d of %d, truncated %v", len(list.Comments), list.Total, list.Truncated)
			}
			for i, c := range list.Comments {
				if c.ID != ids[i] || c.IssueID != is.ID || c.BodyHTML != fmt.Sprintf("<p>Komentář %d</p>", i) {
					t.Fatalf("comment %d = %+v", i, c)
				}
			}
			c := list.Comments
			if c[0].Author.ID != f.Me || c[1].Author.Name != "Petr Svoboda" || c[0].Visibility != "" ||
				c[1].Visibility != api.CommentInternal || c[2].Visibility != api.CommentPublic || c[3].Visibility != api.CommentInternal {
				t.Fatalf("authors or visibility: %+v %+v %+v %+v", c[0], c[1], c[2], c[3])
			}
			if string(c[3].Properties["malachi.outbox"]) != `{"id":"ob_7"}` || c[0].Properties != nil {
				t.Fatalf("properties = %v / %v", c[3].Properties, c[0].Properties)
			}
			if !c[229].Updated.After(c[229].Created) || !c[228].Updated.Equal(c[228].Created) {
				t.Fatal("edit times")
			}
			pages := f.RequestsTo(http.MethodGet, "/comment")
			if len(pages) != 3 {
				t.Fatalf("pages = %d", len(pages))
			}
			q, _ := url.ParseQuery(pages[0].Query)
			if q.Get("orderBy") != "-created" || q.Get("expand") != "renderedBody,properties" || q.Get("maxResults") != "100" {
				t.Fatalf("query = %v", q)
			}

			newest, err := r.Comments(ctx, is.ID, 50)
			if err != nil || len(newest.Comments) != 50 || !newest.Truncated || newest.Total != 230 {
				t.Fatalf("newest: %d, truncated %v, total %d, %v", len(newest.Comments), newest.Truncated, newest.Total, err)
			}
			if newest.Comments[0].ID != ids[180] || newest.Comments[49].ID != ids[229] {
				t.Fatalf("newest = %s..%s", newest.Comments[0].ID, newest.Comments[49].ID)
			}
			if n := len(f.RequestsTo(http.MethodGet, "/comment")); n != 4 {
				t.Fatalf("pages after the capped read = %d", n)
			}

			f.Set(func(f *jiratest.Server) { f.MaxPage = 7 })
			small, err := r.Comments(ctx, is.ID, 0)
			if err != nil || len(small.Comments) != 230 || small.Truncated {
				t.Fatalf("small pages: %d, %v", len(small.Comments), err)
			}

			f.DeleteIssue(is.ID)
			if _, err := r.Comments(ctx, is.ID, 0); !IsNotFound(err) {
				t.Fatalf("deleted issue: %v", err)
			}
			if _, err := r.Comments(ctx, "../10000", 0); codeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("bad id: %v", err)
			}
		})
	}
}

func TestRemoteChangelogs(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			stepClock(f, t0)
			a := f.AddIssue("WEB", "Se změnami")
			b := f.AddIssue("WEB", "Hotová")
			c := f.AddIssue("WEB", "Beze změn")
			d := f.AddIssue("WEB", "Smazaná")
			f.SetStatus(a.ID, "3", f.Me)
			f.AddHistory(a.ID, f.Petr, jiratest.Item{Field: "Status", FieldType: "custom", FromString: "A", ToString: "B"})
			f.AddHistory(a.ID, f.Me, jiratest.Item{Field: "summary", FieldType: "jira", FromString: "x", ToString: "Se změnami"})
			f.Assign(a.ID, f.Petr, f.Me)
			f.SetStatus(b.ID, "10001", f.Petr)
			f.SetStatus(d.ID, "3", f.Petr)
			f.DeleteIssue(d.ID)
			f.Set(func(f *jiratest.Server) { f.MaxPage = 1 })
			r := remoteOf(f)
			got, err := r.Changelogs(ctx, []string{a.ID, b.ID, c.ID, d.ID, a.ID})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || len(got[a.ID]) != 2 || len(got[b.ID]) != 1 {
				t.Fatalf("changelogs = %+v", got)
			}
			st, as := got[a.ID][0], got[a.ID][1]
			if st.IssueID != a.ID || st.Author.ID != f.Me || st.Created.IsZero() ||
				len(st.Changes) != 1 || st.Changes[0] != (Change{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress", FromID: "1", ToID: "3"}) {
				t.Fatalf("status change = %+v", st)
			}
			if len(as.Changes) != 1 || as.Changes[0] != (Change{Field: api.IssueFieldAssignee, To: "Petr Svoboda", ToID: f.Petr}) || !as.Created.After(st.Created) {
				t.Fatalf("assignee change = %+v", as)
			}
			if got[b.ID][0].Changes[0].To != "Done" || got[b.ID][0].Author.ID != f.Petr {
				t.Fatalf("b = %+v", got[b.ID])
			}
			if mode == jiratest.Cloud {
				if n := len(f.RequestsTo(http.MethodPost, "/changelog/bulkfetch")); n < 3 {
					t.Fatalf("changelog pages = %d", n)
				}
			} else if n := len(f.RequestsTo(http.MethodGet, "/rest/api/2/issue/")); n != 4 {
				t.Fatalf("issue reads = %d", n)
			}
			if _, err := r.Changelogs(ctx, []string{"x y"}); codeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("bad id: %v", err)
			}
			f.FailNext(jiratest.Failure{Path: "/" + b.ID, Status: 500})
			f.FailNext(jiratest.Failure{Path: "/changelog/bulkfetch", Status: 500})
			if _, err := r.Changelogs(ctx, []string{a.ID, b.ID}); statusOf(err) != 500 {
				t.Fatalf("server error: %v", err)
			}
		})
	}
}

func TestRemoteChangelogChunks(t *testing.T) {
	f := jiratest.New(t, jiratest.Cloud)
	var ids []string
	for i := range 150 {
		is := f.AddIssue("MOB", "Změna "+strconv.Itoa(i))
		f.SetStatus(is.ID, "3", f.Petr)
		ids = append(ids, is.ID)
	}
	got, err := remoteOf(f).Changelogs(context.Background(), ids)
	if err != nil || len(got) != 150 {
		t.Fatalf("changelogs: %d, %v", len(got), err)
	}
	if n := len(f.RequestsTo(http.MethodPost, "/changelog/bulkfetch")); n != 2 {
		t.Fatalf("requests = %d", n)
	}
}

func TestRemoteCountsSpacesStatuses(t *testing.T) {
	ctx := context.Background()
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			var last *jiratest.Issue
			for i := range 4 {
				last = f.AddIssue("WEB", "Počítaná "+strconv.Itoa(i))
			}
			f.SetStatus(last.ID, "10001", f.Me)
			f.Set(func(f *jiratest.Server) { f.MaxPage = 2 })
			r := remoteOf(f)
			for jql, want := range map[string]int{
				"project = WEB": 4,
				"project = WEB AND statusCategory != Done":    3,
				`project = 10001 AND updated >= "-30d"`:       4,
				"project in (WEB, MOB) AND assignee IS EMPTY": 4,
			} {
				if n, err := r.ApproxCount(ctx, jql); err != nil || n != want {
					t.Fatalf("%s: %d, %v", jql, n, err)
				}
			}
			if _, err := r.ApproxCount(ctx, ""); codeOf(err) != api.CodeInvalidArgument {
				t.Fatalf("empty JQL: %v", err)
			}
			spaces, err := r.Spaces(ctx)
			if err != nil || len(spaces) != 3 {
				t.Fatalf("spaces = %+v, %v", spaces, err)
			}
			byKey := map[string]Space{}
			for _, sp := range spaces {
				byKey[sp.Key] = sp
			}
			if byKey["ITSD"] != (Space{ID: "10000", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true}) || byKey["WEB"].ServiceDesk {
				t.Fatalf("spaces = %+v", spaces)
			}
			statuses, err := r.Statuses(ctx)
			if err != nil || len(statuses) != 4 || statuses[2] != (Status{ID: "10001", Name: "Done", Category: api.StatusCategoryDone}) {
				t.Fatalf("statuses = %+v, %v", statuses, err)
			}
		})
	}
}

func TestRemoteOpenContent(t *testing.T) {
	ctx := context.Background()
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 100<<10)...)
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := jiratest.New(t, mode)
			is := f.AddIssue("WEB", "S přílohou")
			small := f.AddAttachment(is.ID, "log.txt", "text/plain", []byte("Dobrý den"))
			big := f.AddAttachment(is.ID, "screen.png", "image/png", png)
			r := remoteOf(f)
			issues, err := r.BulkIssues(ctx, []string{is.ID}, IssueOptions{})
			if err != nil || len(issues) != 1 || len(issues[0].Attachments) != 2 {
				t.Fatalf("issue: %+v, %v", issues, err)
			}
			urls := map[string]string{}
			for _, a := range issues[0].Attachments {
				urls[a.ID] = a.ContentURL
			}
			ct, err := r.OpenContent(ctx, urls[small], 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(ct.Body)
			ct.Body.Close()
			if err != nil || string(data) != "Dobrý den" || ct.MediaType != "text/plain" {
				t.Fatalf("small: %q, %s, %v", data, ct.MediaType, err)
			}
			// Declared over the limit: refused before reading.
			if _, err := r.OpenContent(ctx, urls[small], 3); !errors.Is(err, ErrTooLarge) || codeOf(err) != api.CodeAttachmentTooBig {
				t.Fatalf("declared too large: %v", err)
			}
			// Growing over the limit while read.
			ct, err = r.OpenContent(ctx, urls[big], 50<<10)
			if err == nil {
				_, err = io.ReadAll(ct.Body)
				ct.Body.Close()
			}
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("streamed too large: %v", err)
			}
			ct, err = r.OpenContent(ctx, urls[big], int64(len(png)))
			if err != nil {
				t.Fatal(err)
			}
			data, err = io.ReadAll(ct.Body)
			ct.Body.Close()
			if err != nil || !bytes.Equal(data, png) {
				t.Fatalf("exact limit: %d bytes, %v", len(data), err)
			}
			if mode == jiratest.Cloud {
				media := 0
				for _, rq := range f.RequestsTo(http.MethodGet, "/file/") {
					media++
					if rq.Auth != "" {
						t.Fatal("credentials reached the media host")
					}
				}
				if media == 0 {
					t.Fatal("the media redirect was not followed")
				}
			} else if rq := f.RequestsTo(http.MethodGet, "/secure/attachment/"); len(rq) == 0 || rq[0].Auth != "Bearer "+jiratest.Token {
				t.Fatal("the site's attachment was fetched without credentials")
			}
			site := f.Site.String()
			rel := strings.TrimPrefix(urls[small], "https://"+f.Site.Host)
			if ct, err := r.OpenContent(ctx, rel, 1<<20); err != nil {
				t.Fatalf("relative URL: %v", err)
			} else {
				ct.Body.Close()
			}
			for _, bad := range []string{
				"https://evil.example.test/rest/api/3/attachment/content/" + small,
				"http://" + f.Site.Host + f.Site.Path + "/secure/attachment/" + small + "/log.txt",
				"https://" + f.Site.Host + ":8443" + f.Site.Path + "/x",
				site + "/%2e%2e/admin",
				"javascript:alert(1)",
				"",
			} {
				if _, err := r.OpenContent(ctx, bad, 1<<20); codeOf(err) != api.CodeInvalidArgument {
					t.Errorf("%q: %v", bad, err)
				}
			}
			if mode == jiratest.DC {
				for _, bad := range []string{"https://jira.acme.test/other/secure/attachment/1/x", site + "/../admin", "https://jira.acme.test/jiranot/x"} {
					if _, err := r.OpenContent(ctx, bad, 1<<20); codeOf(err) != api.CodeInvalidArgument {
						t.Errorf("outside the context path %q: %v", bad, err)
					}
				}
			}
			if _, err := r.OpenContent(ctx, urls[small], 0); codeOf(err) != api.CodeInvalidArgument {
				t.Errorf("zero limit: %v", err)
			}
			f.DeleteIssue(is.ID)
			if _, err := r.OpenContent(ctx, urls[small], 1<<20); statusOf(err) != http.StatusNotFound {
				t.Fatalf("gone: %v", err)
			}
		})
	}
}

func TestRemoteOpenContentGateway(t *testing.T) {
	ctx := context.Background()
	f := jiratest.New(t, jiratest.Cloud)
	f.Set(func(f *jiratest.Server) { f.GatewayOnly = true })
	is := f.AddIssue("WEB", "Přes bránu")
	aid := f.AddAttachment(is.ID, "a.txt", "text/plain", []byte("brána"))
	r := remoteOf(f)
	issues, err := r.BulkIssues(ctx, []string{is.ID}, IssueOptions{})
	if err != nil || !r.Gateway() {
		t.Fatalf("bulk: %v, gateway %v", err, r.Gateway())
	}
	for _, u := range []string{
		issues[0].Attachments[0].ContentURL,                          // the gateway's link
		jiratest.CloudSite + "/rest/api/3/attachment/content/" + aid, // the site's link
		jiratest.CloudSite + "/secure/attachment/" + aid + "/a.txt",  // a picture link of rendered HTML
		"/secure/attachment/" + aid + "/a.txt",
		"/secure/thumbnail/" + aid + "/_thumb_" + aid + ".png",
	} {
		ct, err := r.OpenContent(ctx, u, 1<<10)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		data, _ := io.ReadAll(ct.Body)
		ct.Body.Close()
		if string(data) != "brána" {
			t.Fatalf("%s: %q", u, data)
		}
	}
	if !strings.HasPrefix(issues[0].Attachments[0].ContentURL, GatewayBaseURL+jiratest.CloudID+"/") {
		t.Fatalf("content URL = %s", issues[0].Attachments[0].ContentURL)
	}
}

// fixtureSite serves the files of testdata/jira by request path.
func fixtureSite(t *testing.T, routes map[string]string, hosts ...string) *http.Client {
	t.Helper()
	return testSite(t, func(w http.ResponseWriter, r *http.Request) {
		name, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "jira", name))
		if err != nil {
			t.Errorf("fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}, hosts...)
}

func TestRemotePathologicalCloud(t *testing.T) {
	ctx := context.Background()
	hc := fixtureSite(t, map[string]string{
		"POST /rest/api/3/search/jql":      "cloud-search-pathological.json",
		"GET /rest/api/3/project/search":   "cloud-projects-pathological.json",
		"GET /rest/api/3/status":           "statuses-pathological.json",
		"POST /rest/api/3/issue/bulkfetch": "cloud-search-pathological.json",
	}, "acme.atlassian.net")
	c, err := NewClient(Options{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Login: jiratest.Login, HTTP: hc,
		Token: func(context.Context) (string, error) { return jiratest.Token, nil }})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRemote(c)
	page, err := r.Search(ctx, SearchRequest{JQL: "project = ITSD", IssueOptions: IssueOptions{Rendered: true}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != "tok:9" || len(page.Issues) != 3 {
		t.Fatalf("page: next %q, %d issues", page.Next, len(page.Issues))
	}
	good, typed, hostile := page.Issues[0], page.Issues[1], page.Issues[2]

	if good.ID != "10100" || good.Key != "ITSD-1" || good.Summary != "Tiskárna ve 2. patře nefunguje" ||
		good.Status.Category != api.StatusCategoryInProgress || good.Type != "Incident" || good.Priority != "High" ||
		good.Assignee.Name != "Petr Svoboda" || good.Reporter.ID != "5b10ac8d82e05b22cc7d4ef5" || !good.Watching ||
		good.SpaceKey != "ITSD" {
		t.Fatalf("good = %+v", good)
	}
	if want := time.Date(2026, 9, 1, 6, 15, 30, 0, time.UTC); !good.Created.Equal(want) {
		t.Fatalf("created = %v", good.Created)
	}
	if want := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC); !good.Updated.Equal(want) {
		t.Fatalf("updated (offset with a colon) = %v", good.Updated)
	}
	if strings.Contains(good.DescriptionHTML, "\x00") || !strings.Contains(good.DescriptionHTML, "<script>") {
		t.Fatalf("description = %q (NUL dropped, the rest left to the sanitiser)", good.DescriptionHTML)
	}
	wantAtt := []Attachment{
		{ID: "20001", Filename: ".._.._etc_passwd", Size: 1234, MimeType: "text/plain",
			ContentURL: jiratest.CloudSite + "/rest/api/3/attachment/content/20001", Created: time.Date(2026, 9, 1, 6, 16, 0, 0, time.UTC)},
		{ID: "20002", Filename: "screengnp.exe", Size: -1},
		{ID: "20003", Filename: "relative.png", Size: 77, MimeType: "image/png", ContentURL: jiratest.CloudSite + "/rest/api/3/attachment/content/20003"},
	}
	if fmt.Sprintf("%+v", good.Attachments) != fmt.Sprintf("%+v", wantAtt) {
		t.Fatalf("attachments =\n%+v\nwant\n%+v", good.Attachments, wantAtt)
	}

	if typed.ID != "10104" || typed.Key != "WEB-4" || typed.Summary != "" || typed.Status != (Status{}) || typed.Type != "" ||
		typed.Assignee.ID != "" || !typed.Created.IsZero() || !typed.Updated.IsZero() || typed.SpaceID != "10001" ||
		typed.Watching || typed.Attachments != nil {
		t.Fatalf("wrongly typed = %+v", typed)
	}

	if hostile.Key != "MOB-6" || hostile.Assignee.ID != "" || hostile.Assignee.Name != "Petr  Svoboda" ||
		hostile.Reporter.ID != "JIRAUSER10100" || hostile.Status != (Status{ID: "999", Name: "Custom"}) || hostile.Created.IsZero() {
		t.Fatalf("hostile = %+v", hostile)
	}
	if !strings.HasPrefix(hostile.Summary, "Zpráva  [31m s řízením nový řádek xxx") || len(hostile.Summary) > maxSummaryBytes {
		t.Fatalf("summary = %q (%d bytes)", hostile.Summary[:60], len(hostile.Summary))
	}
	for _, is := range page.Issues {
		for _, s := range []string{is.Summary, is.Assignee.Name, is.Reporter.Name, is.Status.Name} {
			if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || isInvisible(r) || r == 0x2028 }) {
				t.Fatalf("unclean text %q", s)
			}
		}
	}

	// Bulk answers keep only the issues asked for.
	bulk, err := r.BulkIssues(ctx, []string{"10106"}, IssueOptions{})
	if err != nil || len(bulk) != 1 || bulk[0].Key != "MOB-6" {
		t.Fatalf("bulk = %+v, %v", bulk, err)
	}

	spaces, err := r.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantSpaces := []Space{{ID: "10000", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true},
		{ID: "10001", Key: "WEB", Name: "Web stránky"}, {ID: "10004", Key: "MOB", Name: "Mobile eliboM", ServiceDesk: true}}
	if fmt.Sprint(spaces) != fmt.Sprint(wantSpaces) {
		t.Fatalf("spaces = %+v", spaces)
	}
	statuses, err := r.Statuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantStatuses := []Status{{"1", "To Do", api.StatusCategoryTodo}, {"3", "In Progress", api.StatusCategoryInProgress},
		{"10001", "Done", api.StatusCategoryDone}, {"10005", "Odd", api.StatusCategoryTodo}, {"10006", "Weird", ""}}
	if fmt.Sprint(statuses) != fmt.Sprint(wantStatuses) {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestRemotePathologicalDC(t *testing.T) {
	ctx := context.Background()
	hc := fixtureSite(t, map[string]string{
		"GET /jira/rest/api/2/issue/10100/comment": "dc-comments-pathological.json",
		"GET /jira/rest/api/2/issue/10100":         "dc-changelog-odd.json",
		"GET /jira/rest/api/2/issue/10101":         "dc-changelog-odd.json", // answers for another issue
	}, "jira.acme.test")
	r := NewRemote(testClient(t, jiratest.DCSite, hc))
	list, err := r.Comments(ctx, "10100", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range list.Comments {
		got = append(got, c.ID+":"+string(c.Visibility))
	}
	if strings.Join(got, " ") != "30006:internal 30001:public 30002:internal 30003:internal 30004:" {
		t.Fatalf("comments = %v", got)
	}
	if list.Total != 7 || !list.Truncated {
		t.Fatalf("total %d, truncated %v", list.Total, list.Truncated)
	}
	byID := map[string]Comment{}
	for _, c := range list.Comments {
		byID[c.ID] = c
	}
	if c := byID["30002"]; c.Author.ID != "JIRAUSER10100" || c.UpdateAuthor.Name != "Petr Svoboda" || !c.Updated.After(c.Created) {
		t.Fatalf("edited comment = %+v", c)
	}
	props := byID["30003"].Properties
	if len(props) != 2 || string(props["sd.public.comment"]) != `{"internal":true}` || string(props["malachi.outbox"]) != `{"id":"ob_1"}` {
		t.Fatalf("properties = %v", props)
	}
	if c := byID["30006"]; c.BodyHTML != "<p>nulbyte</p>" || !c.Created.IsZero() || c.Properties != nil {
		t.Fatalf("hostile comment = %+v", c)
	}
	if byID["30003"].Author != (User{}) {
		t.Fatalf("anonymous author = %+v", byID["30003"].Author)
	}

	logs, err := r.Changelogs(ctx, []string{"10100", "10101"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("an answer for another issue was taken: %v", logs)
	}
	var hs []string
	for _, h := range logs["10100"] {
		for _, ch := range h.Changes {
			hs = append(hs, h.ID+":"+string(ch.Field)+":"+ch.From+">"+ch.To)
		}
	}
	if strings.Join(hs, " | ") != "40002:assignee:>Petr Svoboda | 40001:status:To Do>In Progress | 40007:assignee:>Jana Dvořáková" {
		t.Fatalf("histories = %v", hs)
	}
}

func TestRemoteMalformedPageToken(t *testing.T) {
	for _, token := range []string{"has space", strings.Repeat("t", maxCursorBytes+1), `tok\u0000`} {
		hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"issues":[],"nextPageToken":"`+token+`"}`)
		}, "acme.atlassian.net")
		c, err := NewClient(Options{SiteURL: jiratest.CloudSite, Deployment: api.JiraCloud, Login: jiratest.Login, HTTP: hc,
			Token: func(context.Context) (string, error) { return jiratest.Token, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewRemote(c).SearchIDs(context.Background(), "project = WEB", ""); codeOf(err) != api.CodeServerError {
			t.Errorf("token %.20q: %v", token, err)
		}
	}
}

func TestRemoteMyselfWithoutIdentity(t *testing.T) {
	hc := testSite(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"displayName":"Jana Dvořáková","key":"`+strings.Repeat("k", 400)+`"}`)
	}, "jira.acme.test")
	if _, err := NewRemote(testClient(t, "https://jira.acme.test", hc)).Myself(context.Background()); codeOf(err) != api.CodeServerError {
		t.Fatalf("err = %v", err)
	}
}

func TestCleaners(t *testing.T) {
	for in, want := range map[string]string{
		"2024-01-15T10:30:00.000+0100":  "2024-01-15T09:30:00Z",
		"2024-01-15T10:30:00.000+01:00": "2024-01-15T09:30:00Z",
		"2024-01-15T10:30:00+0100":      "2024-01-15T09:30:00Z",
		"2024-01-15T10:30:00Z":          "2024-01-15T10:30:00Z",
		"2024-01-15T10:30+0100":         "2024-01-15T09:30:00Z",
		"":                              "0001-01-01T00:00:00Z",
		"yesterday":                     "0001-01-01T00:00:00Z",
		"1969-12-31T23:59:59.000+0000":  "0001-01-01T00:00:00Z",
		"2024-02-30T10:30:00.000+0100":  "0001-01-01T00:00:00Z",
		"2024-01-15T10:30:00.000+2500":  "0001-01-01T00:00:00Z",
		"2024-01-15 10:30":              "0001-01-01T00:00:00Z",
		// POST /changelog/bulkfetch gives created as epoch milliseconds.
		"1705311000000":     "2024-01-15T09:30:00Z",
		"1705311000":        "2024-01-15T09:30:00Z",
		"0":                 "1970-01-01T00:00:00Z",
		"-1":                "0001-01-01T00:00:00Z",
		"99999999999999999": "0001-01-01T00:00:00Z",
	} {
		if got := parseTime(flexString(in)).Format(time.RFC3339); got != want {
			t.Errorf("parseTime(%q) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"jana.dvorakova@acme.test": "jana.dvorakova@acme.test",
		"<x@y>":                    "",
		"no-at":                    "",
		"a@b@c":                    "",
		"@acme.test":               "",
	} {
		if got := cleanEmail(flexString(in)); got != want {
			t.Errorf("cleanEmail(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{
		"image/PNG; name=x.png": "image/png",
		"text/html":             "text/html",
		"nonsense":              "",
		"":                      "",
		"a/b/c":                 "",
	} {
		if got := cleanMediaType(in); got != want {
			t.Errorf("cleanMediaType(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"10000": "10000", "WEB": `"WEB"`, `a"b\c`: `"a\"b\\c"`, "": `""`} {
		if got := JQLValue(in); got != want {
			t.Errorf("JQLValue(%q) = %s", in, got)
		}
	}
	if !idLess("9", "10") || idLess("10", "9") || !idLess("a", "b") {
		t.Error("idLess")
	}
	if s := cleanText("é"+strings.Repeat("ř", 10), 5); s != "éř" {
		t.Errorf("cut at a rune boundary: %q", s)
	}
	if got := cleanTimeZone("Europe/Prague\"><script>"); got != "" {
		t.Errorf("time zone = %q", got)
	}
}

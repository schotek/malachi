// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/schotek/malachi/backend/pkg/api"
)

// dcRemote speaks Jira Data Center's REST API v2: search with
// startAt/total, the project list as one array, changelogs through
// expand=changelog on each issue, counts as a search's total, issues by
// id through an "id in (...)" search.
type dcRemote struct {
	base
}

// dcBulkIssues is how many ids one "id in (...)" search names.
const dcBulkIssues = 50

func (r *dcRemote) Spaces(ctx context.Context) ([]Space, error) {
	data, err := r.c.getJSON(ctx, r.api+"/project", nil)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := decodeStrict(data, &items); err != nil {
		return nil, err
	}
	return spacesOf(items, map[string]bool{}, nil), nil
}

// dcSearchBody is the body of POST /rest/api/2/search.
type dcSearchBody struct {
	JQL           string   `json:"jql"`
	StartAt       int      `json:"startAt"`
	MaxResults    int      `json:"maxResults"`
	Fields        []string `json:"fields"`
	Expand        []string `json:"expand,omitempty"`
	ValidateQuery *bool    `json:"validateQuery,omitempty"`
}

// search runs one page and returns its raw issues, the next cursor and
// the server's total.
func (r *dcRemote) search(ctx context.Context, body dcSearchBody) ([]json.RawMessage, Cursor, int, error) {
	if err := checkJQL(body.JQL); err != nil {
		return nil, "", 0, err
	}
	data, err := r.c.postJSON(ctx, r.api+"/search", body)
	if err != nil {
		return nil, "", 0, err
	}
	var env struct {
		Issues *[]json.RawMessage `json:"issues"`
		Total  *flexInt           `json:"total"`
	}
	if err := decodeStrict(data, &env); err != nil {
		return nil, "", 0, err
	}
	if env.Issues == nil || env.Total == nil {
		return nil, "", 0, api.NewError(api.CodeServerError, "jira: search answer without issues or total")
	}
	total := int(max(*env.Total, 0))
	var next Cursor
	if n := len(*env.Issues); n > 0 && body.StartAt+n < total {
		next = Cursor(strconv.Itoa(body.StartAt + n))
	}
	return *env.Issues, next, total, nil
}

// dcStart reads a datacenter cursor (a startAt).
func dcStart(c Cursor) (int, error) {
	if c == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(string(c))
	if err != nil || n < 0 {
		return 0, api.NewError(api.CodeInvalidArgument, "jira: bad search cursor")
	}
	return n, nil
}

func (r *dcRemote) Search(ctx context.Context, req SearchRequest) (SearchPage, error) {
	start, err := dcStart(req.Cursor)
	if err != nil {
		return SearchPage{}, err
	}
	body := dcSearchBody{
		JQL:        req.JQL,
		StartAt:    start,
		MaxResults: pageSize(req.PageSize, defaultSearchPage, maxSearchPage),
		Fields:     issueFields(req.IssueOptions),
	}
	if req.Rendered && req.Fields == FieldsAll {
		body.Expand = []string{"renderedFields"}
	}
	raws, next, total, err := r.search(ctx, body)
	if err != nil {
		return SearchPage{}, err
	}
	return SearchPage{Issues: r.issues(raws, req.IssueOptions), Next: next, Total: total}, nil
}

func (r *dcRemote) SearchIDs(ctx context.Context, jql string, cursor Cursor) (IDPage, error) {
	start, err := dcStart(cursor)
	if err != nil {
		return IDPage{}, err
	}
	raws, next, _, err := r.search(ctx, dcSearchBody{JQL: jql, StartAt: start, MaxResults: maxIDPage, Fields: []string{"id"}})
	if err != nil {
		return IDPage{}, err
	}
	return IDPage{IDs: idsOf(raws), Next: next}, nil
}

// BulkIssues searches "id in (...)" without query validation (an id that
// is gone matches nothing instead of failing the query); numeric ids
// only, anything else, or a site that refuses the query (400), is
// fetched one by one.
func (r *dcRemote) BulkIssues(ctx context.Context, ids []string, opts IssueOptions) ([]Issue, error) {
	ids, err := checkIDs(ids)
	if err != nil {
		return nil, err
	}
	var numeric, single []string
	for _, id := range ids {
		if isDigits(id) {
			numeric = append(numeric, id)
		} else {
			single = append(single, id)
		}
	}
	var out []Issue
	noValidation := false
	for _, chunk := range chunks(numeric, dcBulkIssues) {
		body := dcSearchBody{
			JQL:           "id in (" + strings.Join(chunk, ",") + ")",
			MaxResults:    len(chunk),
			Fields:        issueFields(opts),
			ValidateQuery: &noValidation,
		}
		if opts.Rendered && opts.Fields == FieldsAll {
			body.Expand = []string{"renderedFields"}
		}
		raws, _, _, err := r.search(ctx, body)
		if statusOf(err) == http.StatusBadRequest {
			single = append(single, chunk...)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, onlyAsked(r.issues(raws, opts), chunk)...)
	}
	for _, id := range single {
		is, ok, err := r.issue(ctx, id, opts)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, is)
		}
	}
	return dedupeIssues(out), nil
}

// issue fetches one issue; false when it is gone (404).
func (r *dcRemote) issue(ctx context.Context, id string, opts IssueOptions) (Issue, bool, error) {
	q := url.Values{}
	q.Set("fields", strings.Join(issueFields(opts), ","))
	if opts.Rendered && opts.Fields == FieldsAll {
		q.Set("expand", "renderedFields")
	}
	data, err := r.c.getJSON(ctx, r.api+"/issue/"+url.PathEscape(id), q)
	if IsNotFound(err) {
		return Issue{}, false, nil
	}
	if err != nil {
		return Issue{}, false, err
	}
	is := r.issues([]json.RawMessage{data}, opts)
	if len(is) == 0 || (is[0].ID != id && !strings.EqualFold(is[0].Key, id)) {
		return Issue{}, false, nil
	}
	return is[0], true, nil
}

// Changelogs reads each issue with expand=changelog, maxConcurrent at a
// time. An issue that is gone (404) is absent; any other failure fails
// the call.
func (r *dcRemote) Changelogs(ctx context.Context, issueIDs []string) (map[string][]History, error) {
	ids, err := checkIDs(issueIDs)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		out      = map[string][]History{}
		firstErr error
		wg       sync.WaitGroup
		work     = make(chan string)
	)
	for range min(maxConcurrent, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				hs, err := r.changelog(ctx, id)
				mu.Lock()
				switch {
				case err != nil && firstErr == nil:
					firstErr = err
					cancel()
				case err == nil && len(hs) > 0:
					out[id] = hs
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for _, id := range ids {
		select {
		case work <- id:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (r *dcRemote) changelog(ctx context.Context, id string) ([]History, error) {
	q := url.Values{}
	q.Set("fields", "status")
	q.Set("expand", "changelog")
	data, err := r.c.getJSON(ctx, r.api+"/issue/"+url.PathEscape(id), q)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var w struct {
		ID        flexString     `json:"id"`
		Changelog *wireChangelog `json:"changelog"`
	}
	if err := decodeStrict(data, &w); err != nil {
		return nil, err
	}
	if cleanID(w.ID) != id || w.Changelog == nil {
		return nil, nil
	}
	return histories(w.Changelog.Histories, id, nil), nil
}

func (r *dcRemote) ApproxCount(ctx context.Context, jql string) (int, error) {
	_, _, total, err := r.search(ctx, dcSearchBody{JQL: jql, MaxResults: 0, Fields: []string{"id"}})
	if err != nil {
		return 0, err
	}
	return total, nil
}

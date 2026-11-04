// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// cloudRemote speaks Jira Cloud's REST API v3: search through
// /search/jql with page tokens, bulk fetches of issues and changelogs,
// approximate counts, paged project search.
type cloudRemote struct {
	base
}

// Page sizes of the cloud bulk endpoints.
const (
	cloudBulkIssues    = 100  // /issue/bulkfetch accepts 100 ids
	cloudBulkChangelog = 100  // ids per /changelog/bulkfetch request (the API takes 1000)
	cloudChangelogPage = 1000 // histories per /changelog/bulkfetch page
)

func (r *cloudRemote) Spaces(ctx context.Context) ([]Space, error) {
	var out []Space
	seen := map[string]bool{}
	start := 0
	for page := 0; page < maxPages && len(out) < maxSpaces; page++ {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(start))
		q.Set("maxResults", strconv.Itoa(projectPage))
		q.Set("orderBy", "name")
		data, err := r.c.getJSON(ctx, r.api+"/project/search", q)
		if err != nil {
			return nil, err
		}
		var env struct {
			Values *[]json.RawMessage `json:"values"`
			IsLast *flexBool          `json:"isLast"`
			Total  *flexInt           `json:"total"`
		}
		if err := decodeStrict(data, &env); err != nil {
			return nil, err
		}
		if env.Values == nil {
			return nil, api.NewError(api.CodeServerError, "jira: project page without values")
		}
		out = spacesOf(*env.Values, seen, out)
		n := len(*env.Values)
		start += n
		if n == 0 || (env.IsLast != nil && bool(*env.IsLast)) || (env.IsLast == nil && env.Total != nil && start >= int(*env.Total)) {
			break
		}
	}
	return out, nil
}

// cloudSearchBody is the body of POST /rest/api/3/search/jql.
type cloudSearchBody struct {
	JQL             string   `json:"jql"`
	MaxResults      int      `json:"maxResults"`
	Fields          []string `json:"fields"`
	Expand          string   `json:"expand,omitempty"`
	NextPageToken   string   `json:"nextPageToken,omitempty"`
	ReconcileIssues []int64  `json:"reconcileIssues,omitempty"`
}

type cloudSearchPage struct {
	Issues        *[]json.RawMessage `json:"issues"`
	NextPageToken flexString         `json:"nextPageToken"`
	IsLast        *flexBool          `json:"isLast"`
}

// search runs one page and returns its raw issues and the next cursor.
func (r *cloudRemote) search(ctx context.Context, body cloudSearchBody) ([]json.RawMessage, Cursor, error) {
	if err := checkJQL(body.JQL); err != nil {
		return nil, "", err
	}
	data, err := r.c.postJSON(ctx, r.api+"/search/jql", body)
	if err != nil {
		return nil, "", err
	}
	var env cloudSearchPage
	if err := decodeStrict(data, &env); err != nil {
		return nil, "", err
	}
	if env.Issues == nil {
		return nil, "", api.NewError(api.CodeServerError, "jira: search answer without issues")
	}
	next, err := cloudCursor(env.NextPageToken, env.IsLast)
	if err != nil {
		return nil, "", err
	}
	return *env.Issues, next, nil
}

// cloudCursor is the next page's cursor: the token unless the page says
// it is the last.
func cloudCursor(token flexString, isLast *flexBool) (Cursor, error) {
	t := string(token)
	if t == "" || (isLast != nil && bool(*isLast)) {
		return "", nil
	}
	if len(t) > maxCursorBytes || !printableASCII(t) {
		return "", api.NewError(api.CodeServerError, "jira: malformed page token")
	}
	return Cursor(t), nil
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func (r *cloudRemote) Search(ctx context.Context, req SearchRequest) (SearchPage, error) {
	body := cloudSearchBody{
		JQL:           req.JQL,
		MaxResults:    pageSize(req.PageSize, defaultSearchPage, maxSearchPage),
		Fields:        issueFields(req.IssueOptions),
		NextPageToken: string(req.Cursor),
	}
	if req.Rendered && req.Fields == FieldsAll {
		body.Expand = "renderedFields"
	}
	for _, id := range req.Reconcile {
		if len(body.ReconcileIssues) == maxReconcile {
			break
		}
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && n > 0 && isDigits(id) {
			body.ReconcileIssues = append(body.ReconcileIssues, n)
		}
	}
	raws, next, err := r.search(ctx, body)
	if err != nil {
		return SearchPage{}, err
	}
	return SearchPage{Issues: r.issues(raws, req.IssueOptions), Next: next, Total: -1}, nil
}

func (r *cloudRemote) SearchIDs(ctx context.Context, jql string, cursor Cursor) (IDPage, error) {
	raws, next, err := r.search(ctx, cloudSearchBody{JQL: jql, MaxResults: maxIDPage, Fields: []string{"id"}, NextPageToken: string(cursor)})
	if err != nil {
		return IDPage{}, err
	}
	return IDPage{IDs: idsOf(raws), Next: next}, nil
}

// idsOf reads the ids of an id-only search page.
func idsOf(raws []json.RawMessage) []string {
	out := make([]string, 0, len(raws))
	seen := make(map[string]bool, len(raws))
	for _, raw := range raws {
		var w struct {
			ID flexString `json:"id"`
		}
		if !decodeLenient(raw, &w) {
			continue
		}
		if id := cleanID(w.ID); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (r *cloudRemote) BulkIssues(ctx context.Context, ids []string, opts IssueOptions) ([]Issue, error) {
	ids, err := checkIDs(ids)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, chunk := range chunks(ids, cloudBulkIssues) {
		body := struct {
			IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
			Fields         []string `json:"fields"`
			Expand         []string `json:"expand,omitempty"`
		}{IssueIDsOrKeys: chunk, Fields: issueFields(opts)}
		if opts.Rendered && opts.Fields == FieldsAll {
			body.Expand = []string{"renderedFields"}
		}
		data, err := r.c.postJSON(ctx, r.api+"/issue/bulkfetch", body)
		if err != nil {
			return nil, err
		}
		var env struct {
			Issues *[]json.RawMessage `json:"issues"`
		}
		if err := decodeStrict(data, &env); err != nil {
			return nil, err
		}
		if env.Issues == nil {
			return nil, api.NewError(api.CodeServerError, "jira: bulk answer without issues")
		}
		out = append(out, onlyAsked(r.issues(*env.Issues, opts), chunk)...)
	}
	return dedupeIssues(out), nil
}

// onlyAsked keeps the issues whose id, or key (ignoring case), was asked
// for: an answer cannot smuggle in others.
func onlyAsked(issues []Issue, ids []string) []Issue {
	asked := make(map[string]bool, len(ids))
	for _, id := range ids {
		asked[strings.ToUpper(id)] = true
	}
	out := issues[:0]
	for _, is := range issues {
		if asked[is.ID] || asked[strings.ToUpper(is.Key)] {
			out = append(out, is)
		}
	}
	return out
}

func dedupeIssues(in []Issue) []Issue {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, is := range in {
		if !seen[is.ID] {
			seen[is.ID] = true
			out = append(out, is)
		}
	}
	return out
}

func (r *cloudRemote) Changelogs(ctx context.Context, issueIDs []string) (map[string][]History, error) {
	ids, err := checkIDs(issueIDs)
	if err != nil {
		return nil, err
	}
	out := map[string][]History{}
	for _, chunk := range chunks(ids, cloudBulkChangelog) {
		asked := make(map[string]bool, len(chunk))
		for _, id := range chunk {
			asked[id] = true
		}
		raw := map[string][]json.RawMessage{}
		token := ""
		tokens := map[string]bool{}
		for page := 0; page < maxPages; page++ {
			body := struct {
				IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
				FieldIDs       []string `json:"fieldIds"`
				MaxResults     int      `json:"maxResults"`
				NextPageToken  string   `json:"nextPageToken,omitempty"`
			}{chunk, []string{"status", "assignee"}, cloudChangelogPage, token}
			data, err := r.c.postJSON(ctx, r.api+"/changelog/bulkfetch", body)
			if err != nil {
				return nil, err
			}
			var env struct {
				IssueChangeLogs *[]struct {
					IssueID         flexString        `json:"issueId"`
					ChangeHistories []json.RawMessage `json:"changeHistories"`
				} `json:"issueChangeLogs"`
				NextPageToken flexString `json:"nextPageToken"`
			}
			if err := decodeStrict(data, &env); err != nil {
				return nil, err
			}
			if env.IssueChangeLogs == nil {
				return nil, api.NewError(api.CodeServerError, "jira: changelog answer without changelogs")
			}
			for _, l := range *env.IssueChangeLogs {
				if id := cleanID(l.IssueID); asked[id] {
					raw[id] = append(raw[id], l.ChangeHistories...)
				}
			}
			next, err := cloudCursor(env.NextPageToken, nil)
			if err != nil {
				return nil, err
			}
			if next == "" {
				break
			}
			if tokens[string(next)] {
				return nil, api.NewError(api.CodeServerError, "jira: changelog pages do not advance")
			}
			tokens[string(next)] = true
			token = string(next)
		}
		for id, rs := range raw {
			if hs := histories(rs, id, nil); len(hs) > 0 {
				out[id] = hs
			}
		}
	}
	return out, nil
}

func (r *cloudRemote) ApproxCount(ctx context.Context, jql string) (int, error) {
	if err := checkJQL(jql); err != nil {
		return 0, err
	}
	data, err := r.c.postJSON(ctx, r.api+"/search/approximate-count", struct {
		JQL string `json:"jql"`
	}{jql})
	if err != nil {
		return 0, err
	}
	var env struct {
		Count *flexInt `json:"count"`
	}
	if err := decodeStrict(data, &env); err != nil {
		return 0, err
	}
	if env.Count == nil {
		return 0, api.NewError(api.CodeServerError, "jira: count answer without a count")
	}
	return int(max(*env.Count, 0)), nil
}

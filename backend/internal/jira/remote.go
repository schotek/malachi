// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Remote is what the syncer asks of a site, the same for both flavours
// (NewRemote picks cloud or datacenter). Every string it returns is
// cleaned (valid UTF-8, no control or bidirectional-override characters,
// capped) and every id checked for shape; items without a usable id are
// dropped and duplicates within one answer removed (the first wins).
// HTML (Issue.DescriptionHTML, Comment.BodyHTML) is the site's rendered
// HTML, capped but otherwise untouched: hostile until the sanitiser has
// seen it. Errors are *StatusError (IsNotFound, IsForbidden,
// IsUnauthorized, RetryAfter) or *api.Error; ToAPIError maps both.
//
// The one write is AddComment (ADF on cloud, wiki markup on datacenter,
// BuildComment); finding a comment again by the property it was posted
// with needs nothing more: Comments returns the properties.
type Remote interface {
	// Deployment is the flavour behind the interface.
	Deployment() api.JiraDeployment
	// Gateway reports whether requests go through the Atlassian API
	// gateway (a scoped token); a new client for the account should start
	// there (Options.Gateway).
	Gateway() bool
	// Myself is the user the token signs in as. Its ID is never empty.
	Myself(ctx context.Context) (User, error)
	// Spaces lists the spaces (projects) the user can browse, in the
	// site's order, at most maxSpaces.
	Spaces(ctx context.Context) ([]Space, error)
	// Statuses lists the site's workflow statuses.
	Statuses(ctx context.Context) ([]Status, error)
	// Search runs one page of a JQL search.
	Search(ctx context.Context, req SearchRequest) (SearchPage, error)
	// SearchIDs runs one page of a JQL search for issue ids only, in big
	// pages (maxIDPage).
	SearchIDs(ctx context.Context, jql string, cursor Cursor) (IDPage, error)
	// BulkIssues fetches issues by id (or by key: an issue whose key a
	// notification names); one that is gone or not visible is simply
	// absent from the result. The order is the server's.
	BulkIssues(ctx context.Context, ids []string, opts IssueOptions) ([]Issue, error)
	// Comments returns the newest limit comments of an issue (0 =
	// api.MaxThreadMessages), oldest first, with rendered bodies and
	// properties; it pages with orderBy=-created, which cloud and current
	// Data Center honour. A 404 means the issue is gone (IsNotFound).
	Comments(ctx context.Context, issueID string, limit int) (CommentList, error)
	// Changelogs returns the status and assignee changes of the issues,
	// keyed by issue id, oldest first; an issue without such changes, or
	// gone, is absent.
	Changelogs(ctx context.Context, issueIDs []string) (map[string][]History, error)
	// ApproxCount estimates how many issues jql matches.
	ApproxCount(ctx context.Context, jql string) (int, error)
	// OpenContent downloads an attachment or a picture of the site (see
	// Client.OpenContent).
	OpenContent(ctx context.Context, rawURL string, limit int64) (*Content, error)
	// AddComment posts a comment to the issue with the entity properties
	// props, in the one request (their keys in order). body must carry the
	// deployment's format. The answer is the comment as the site
	// describes it; a 2xx whose body tells too little is still success (the
	// comment exists), with only IssueID set.
	AddComment(ctx context.Context, issueID string, body CommentBody, props map[string]json.RawMessage) (Comment, error)
}

// NewRemote returns the Remote for the client's deployment. The client
// must have a token (Options.Token).
func NewRemote(c *Client) Remote {
	b := base{c: c}
	if c.deployment == api.JiraDataCenter {
		b.api = "/rest/api/2"
		return &dcRemote{base: b}
	}
	b.api = "/rest/api/3"
	return &cloudRemote{base: b}
}

// Page sizes and caps.
const (
	defaultSearchPage = 100
	maxSearchPage     = 100
	maxIDPage         = 1000
	commentPage       = 100
	projectPage       = 50
	maxSpaces         = 5000
	maxStatuses       = 5000
	maxHistories      = 1000 // per issue, the newest kept
	maxReconcile      = 50   // cloud reconcileIssues
	maxPages          = 1000 // any paged loop of one call
)

// User is a user of the site as one answer names it.
type User struct {
	// ID is the account id (cloud) or the user key, else the user name
	// (datacenter); "" when the answer named nobody usable (anonymous,
	// deleted, or an id over maxUserIDBytes).
	ID    string
	Name  string // display name
	Email string // "" when the site does not reveal it
	// TimeZone is the user's profile time zone as the site names it
	// (Myself only; checked for shape, not resolved).
	TimeZone string
}

// Space is a space (a Jira project).
type Space struct {
	ID          string
	Key         string
	Name        string
	ServiceDesk bool // projectTypeKey "service_desk"
}

// Status is a workflow status.
type Status struct {
	ID       string
	Name     string
	Category api.IssueStatusCategory // "" when the site's category is unknown
}

// Attachment is a file attached to an issue. ContentURL is absolute (the
// site's own, or the gateway's); MimeType is the site's claim.
type Attachment struct {
	ID         string
	Filename   string
	Size       int64 // the site's claim; -1 unknown
	MimeType   string
	ContentURL string
	Created    time.Time
}

// FieldSet chooses the fields an issue answer carries.
type FieldSet int

const (
	// FieldsAll fills every field of Issue (DescriptionHTML only with
	// IssueOptions.Rendered).
	FieldsAll FieldSet = iota
	// FieldsStamp fills ID, Key, SpaceID, SpaceKey and Updated only:
	// enough to tell what changed.
	FieldsStamp
)

// IssueOptions shape an issue answer.
type IssueOptions struct {
	Fields FieldSet
	// Rendered asks for the rendered description (Issue.DescriptionHTML).
	Rendered bool
}

// Issue is an issue as one answer described it.
type Issue struct {
	ID       string
	Key      string
	SpaceID  string
	SpaceKey string
	Summary  string
	// DescriptionHTML is the rendered description; "" when empty or not
	// asked for.
	DescriptionHTML string
	Status          Status
	Type            string
	Priority        string
	Assignee        User // zero = unassigned
	Reporter        User
	Created         time.Time
	Updated         time.Time
	Watching        bool // the user watches the issue (watches.isWatching)
	Attachments     []Attachment
}

// Comment is a comment of an issue.
type Comment struct {
	ID           string
	IssueID      string
	Author       User
	UpdateAuthor User
	BodyHTML     string // rendered body
	Created      time.Time
	Updated      time.Time
	// Visibility is internal when the service desk marks the comment
	// internal (jsdPublic false, or the sd.public.comment property says
	// internal), public when it marks it public, "" when it says nothing
	// (not a service-desk issue, or a site that does not tell).
	Visibility api.CommentVisibility
	// Properties are the comment's entity properties (expand=properties),
	// raw compact JSON by key, at most maxProperties of at most
	// maxPropertyBytes each.
	Properties map[string]json.RawMessage
}

// CommentList is Comments' answer.
type CommentList struct {
	Comments []Comment // oldest first
	// Total is the site's count of the issue's comments. Truncated says
	// the list may miss some: older ones left out (limit), or the site
	// counted more than it sent (items that did not decode, duplicates).
	// A caller must not take a comment missing from a truncated list for
	// deleted.
	Total     int
	Truncated bool
}

// Change is one change of a watched field in a changelog entry. From and
// To are display values ("" = none); FromID and ToID the site's ids.
type Change struct {
	Field  api.IssueField
	From   string
	To     string
	FromID string
	ToID   string
}

// History is one changelog entry with its status and assignee changes.
type History struct {
	ID      string
	IssueID string
	Author  User
	Created time.Time
	Changes []Change
}

// Cursor is an opaque position in a paged search: cloud's nextPageToken
// or datacenter's startAt. "" is the first page.
type Cursor string

// SearchRequest is one page of Search.
type SearchRequest struct {
	JQL string
	IssueOptions
	// Reconcile (cloud) names issue ids whose latest state the search must
	// reflect despite index lag (at most maxReconcile numeric ids are
	// sent; ignored by datacenter).
	Reconcile []string
	Cursor    Cursor
	PageSize  int // 0 = 100; at most 100
}

// SearchPage is Search's answer.
type SearchPage struct {
	Issues []Issue
	Next   Cursor // "" = this was the last page
	Total  int    // datacenter: the server's total; cloud: -1
}

// IDPage is SearchIDs' answer.
type IDPage struct {
	IDs  []string
	Next Cursor
}

// Content is a download in progress. Reading Body past the limit fails
// with ErrTooLarge; the caller closes it.
type Content struct {
	Body io.ReadCloser
	// Length is the declared length (-1 unknown); MediaType the server's
	// claim, untrusted: whoever uses the bytes sniffs them.
	Length    int64
	MediaType string
}

// base is what both flavours share.
type base struct {
	c   *Client
	api string // "/rest/api/3" or "/rest/api/2"
}

func (b *base) Deployment() api.JiraDeployment { return b.c.deployment }
func (b *base) Gateway() bool                  { return b.c.Gateway() }

func (b *base) OpenContent(ctx context.Context, rawURL string, limit int64) (*Content, error) {
	return b.c.OpenContent(ctx, rawURL, limit)
}

func (b *base) Myself(ctx context.Context) (User, error) {
	data, err := b.c.getJSON(ctx, b.api+"/myself", nil)
	if err != nil {
		return User{}, err
	}
	var w wireUser
	if err := decodeStrict(data, &w); err != nil {
		return User{}, err
	}
	u := userOf(&w)
	if u.ID == "" {
		return User{}, api.NewError(api.CodeServerError, "jira: the site did not say who the user is")
	}
	u.Email = cleanEmail(w.EmailAddress)
	u.TimeZone = cleanTimeZone(w.TimeZone)
	return u, nil
}

func (b *base) Statuses(ctx context.Context) ([]Status, error) {
	data, err := b.c.getJSON(ctx, b.api+"/status", nil)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := decodeStrict(data, &items); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Status
	for _, raw := range items {
		var w wireStatus
		if !decodeLenient(raw, &w) {
			continue
		}
		st := statusFrom(&w)
		if st.ID == "" || seen[st.ID] {
			continue
		}
		seen[st.ID] = true
		out = append(out, st)
		if len(out) == maxStatuses {
			break
		}
	}
	return out, nil
}

// Comments pages the issue's comments newest first until limit, then
// returns them oldest first. Both flavours share the endpoint.
func (b *base) Comments(ctx context.Context, issueID string, limit int) (CommentList, error) {
	if cleanID(flexString(issueID)) == "" {
		return CommentList{}, api.NewError(api.CodeInvalidArgument, "jira: bad issue id")
	}
	if limit <= 0 {
		limit = api.MaxThreadMessages
	}
	var list CommentList
	seen := map[string]bool{}
	start := 0
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(start))
		q.Set("maxResults", strconv.Itoa(commentPage))
		q.Set("orderBy", "-created")
		q.Set("expand", "renderedBody,properties")
		data, err := b.c.getJSON(ctx, b.api+"/issue/"+url.PathEscape(issueID)+"/comment", q)
		if err != nil {
			return CommentList{}, err
		}
		var env wireCommentPage
		if err := decodeStrict(data, &env); err != nil {
			return CommentList{}, err
		}
		if env.Comments == nil {
			return CommentList{}, api.NewError(api.CodeServerError, "jira: comment page without comments")
		}
		list.Total = max(list.Total, int(env.Total))
		for _, raw := range *env.Comments {
			var w wireComment
			if !decodeLenient(raw, &w) {
				continue
			}
			cm, ok := commentOf(&w, issueID)
			if !ok || seen[cm.ID] {
				continue
			}
			seen[cm.ID] = true
			if len(list.Comments) == limit {
				list.Truncated = true
				break
			}
			list.Comments = append(list.Comments, cm)
		}
		n := len(*env.Comments)
		start += n
		if list.Truncated || len(list.Comments) == limit || n == 0 || start >= int(env.Total) {
			break
		}
	}
	if list.Total > len(list.Comments) {
		list.Truncated = true
	}
	list.Total = max(list.Total, len(list.Comments))
	sort.SliceStable(list.Comments, func(i, j int) bool {
		a, b := list.Comments[i], list.Comments[j]
		if !a.Created.Equal(b.Created) {
			return a.Created.Before(b.Created)
		}
		return idLess(a.ID, b.ID)
	})
	return list, nil
}

// commentProperty is an entity property of a comment as a request
// carries it.
type commentProperty struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// AddComment posts a comment. Both flavours share the endpoint; the body is
// an ADF document on cloud and a wiki markup string on datacenter.
func (b *base) AddComment(ctx context.Context, issueID string, body CommentBody, props map[string]json.RawMessage) (Comment, error) {
	if cleanID(flexString(issueID)) != issueID {
		return Comment{}, api.NewError(api.CodeInvalidArgument, "jira: bad issue id")
	}
	var payload struct {
		Body       any               `json:"body"`
		Properties []commentProperty `json:"properties,omitempty"`
	}
	switch b.c.deployment {
	case api.JiraCloud:
		if len(body.ADF) == 0 {
			return Comment{}, api.NewError(api.CodeInvalidArgument, "jira: a cloud comment needs an ADF body")
		}
		payload.Body = body.ADF
	case api.JiraDataCenter:
		if body.Wiki == "" {
			return Comment{}, api.NewError(api.CodeInvalidArgument, "jira: a datacenter comment needs a wiki body")
		}
		payload.Body = body.Wiki
	default:
		return Comment{}, api.NewError(api.CodeInvalidArgument, "jira: unknown deployment")
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		payload.Properties = append(payload.Properties, commentProperty{Key: k, Value: props[k]})
	}
	q := url.Values{}
	q.Set("expand", "renderedBody,properties")
	data, err := b.c.postJSON(ctx, b.api+"/issue/"+url.PathEscape(issueID)+"/comment?"+q.Encode(), payload)
	if err != nil {
		return Comment{}, err
	}
	var w wireComment
	if decodeLenient(data, &w) {
		if c, ok := commentOf(&w, issueID); ok {
			return c, nil
		}
	}
	return Comment{IssueID: issueID}, nil
}

// idLess orders numeric ids numerically, others as strings.
func idLess(a, b string) bool {
	if len(a) != len(b) && isDigits(a) && isDigits(b) {
		return len(a) < len(b)
	}
	return a < b
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// issueFields is the fields parameter for opts.
func issueFields(opts IssueOptions) []string {
	// "key" is named although it is no field: cloud's search/jql sends
	// only what it is asked for, and a site that does not know the name
	// ignores it.
	if opts.Fields == FieldsStamp {
		return []string{"key", "project", "updated"}
	}
	f := []string{"key", "summary", "status", "issuetype", "priority", "assignee", "reporter",
		"created", "updated", "project", "attachment", "watches"}
	if opts.Rendered {
		f = append(f, "description")
	}
	return f
}

// issues converts the raw items of an answer, dropping unusable and
// duplicate ones.
func (b *base) issues(raws []json.RawMessage, opts IssueOptions) []Issue {
	seen := make(map[string]bool, len(raws))
	out := make([]Issue, 0, len(raws))
	for _, raw := range raws {
		var w wireIssue
		if !decodeLenient(raw, &w) {
			continue
		}
		is, ok := b.issueOf(&w, opts)
		if !ok || seen[is.ID] {
			continue
		}
		seen[is.ID] = true
		out = append(out, is)
	}
	return out
}

// issueOf converts one issue; false when it has no usable id or key.
func (b *base) issueOf(w *wireIssue, opts IssueOptions) (Issue, bool) {
	is := Issue{ID: cleanID(w.ID), Key: cleanIssueKey(w.Key)}
	if is.ID == "" || is.Key == "" {
		return Issue{}, false
	}
	f := &w.Fields
	if f.Project != nil {
		is.SpaceID = cleanID(f.Project.ID)
		is.SpaceKey = cleanSpaceKey(f.Project.Key)
	}
	is.Updated = parseTime(f.Updated)
	if opts.Fields == FieldsStamp {
		return is, true
	}
	is.Summary = cleanText(string(f.Summary), maxSummaryBytes)
	if f.Status != nil {
		is.Status = statusFrom(f.Status)
	}
	if f.IssueType != nil {
		is.Type = cleanText(string(f.IssueType.Name), maxNameBytes)
	}
	if f.Priority != nil {
		is.Priority = cleanText(string(f.Priority.Name), maxNameBytes)
	}
	is.Assignee = userOf(f.Assignee)
	is.Reporter = userOf(f.Reporter)
	is.Created = parseTime(f.Created)
	if f.Watches != nil {
		is.Watching = bool(f.Watches.IsWatching)
	}
	if opts.Rendered && w.RenderedFields != nil {
		is.DescriptionHTML = cleanHTML(string(w.RenderedFields.Description))
	}
	seen := map[string]bool{}
	for _, raw := range f.Attachment {
		var wa wireAttachment
		if !decodeLenient(raw, &wa) {
			continue
		}
		a, ok := b.attachmentOf(&wa)
		if !ok || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		is.Attachments = append(is.Attachments, a)
		if len(is.Attachments) == maxAttachments {
			break
		}
	}
	return is, true
}

func (b *base) attachmentOf(w *wireAttachment) (Attachment, bool) {
	a := Attachment{
		ID:         cleanID(w.ID),
		Filename:   cleanFilename(w.Filename),
		Size:       int64(w.Size),
		MimeType:   cleanMediaType(string(w.MimeType)),
		ContentURL: absoluteURL(b.c.site, string(w.Content)),
		Created:    parseTime(w.Created),
	}
	if a.ID == "" {
		return Attachment{}, false
	}
	if a.Size < 0 {
		a.Size = -1
	}
	return a, true
}

// userOf converts a user reference; the zero User for none.
func userOf(w *wireUser) User {
	if w == nil {
		return User{}
	}
	id := string(w.AccountID)
	if strings.TrimSpace(id) == "" {
		id = string(w.Key)
	}
	if strings.TrimSpace(id) == "" {
		id = string(w.Name)
	}
	return User{ID: cleanUserID(id), Name: cleanText(string(w.DisplayName), maxNameBytes)}
}

func statusFrom(w *wireStatus) Status {
	st := Status{ID: cleanID(w.ID), Name: cleanText(string(w.Name), maxNameBytes)}
	if w.StatusCategory != nil {
		st.Category = statusCategory(string(w.StatusCategory.Key))
	}
	return st
}

// commentOf converts one comment; false without a usable id.
func commentOf(w *wireComment, issueID string) (Comment, bool) {
	c := Comment{
		ID:           cleanID(w.ID),
		IssueID:      issueID,
		Author:       userOf(w.Author),
		UpdateAuthor: userOf(w.UpdateAuthor),
		BodyHTML:     cleanHTML(string(w.RenderedBody)),
		Created:      parseTime(w.Created),
		Updated:      parseTime(w.Updated),
	}
	if c.ID == "" {
		return Comment{}, false
	}
	internal, public := false, false
	if w.JsdPublic != nil {
		if *w.JsdPublic {
			public = true
		} else {
			internal = true
		}
	}
	for _, raw := range w.Properties {
		var p wireProperty
		if !decodeLenient(raw, &p) {
			continue
		}
		key := cleanText(string(p.Key), maxNameBytes)
		if key == "" || len(p.Value) == 0 {
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, p.Value) != nil || compact.Len() > maxPropertyBytes {
			continue
		}
		if c.Properties == nil {
			c.Properties = map[string]json.RawMessage{}
		}
		if _, dup := c.Properties[key]; dup {
			continue
		}
		if len(c.Properties) == maxProperties {
			break
		}
		c.Properties[key] = json.RawMessage(compact.Bytes())
		if key == "sd.public.comment" {
			var v struct {
				Internal *flexBool `json:"internal"`
			}
			if decodeLenient(compact.Bytes(), &v) && v.Internal != nil {
				if *v.Internal {
					internal = true
				} else {
					public = true
				}
			}
		}
	}
	switch {
	case internal:
		c.Visibility = api.CommentInternal
	case public:
		c.Visibility = api.CommentPublic
	}
	return c, true
}

// historyOf converts one changelog entry to its status and assignee
// changes; false when it has no usable id or no such change. A custom
// field that happens to be called "Status" is not the status.
func historyOf(w *wireHistory, issueID string) (History, bool) {
	h := History{ID: cleanID(w.ID), IssueID: issueID, Author: userOf(w.Author), Created: parseTime(w.Created)}
	if h.ID == "" {
		return History{}, false
	}
	for _, it := range w.Items {
		if ft := strings.TrimSpace(string(it.FieldType)); ft != "" && !strings.EqualFold(ft, "jira") {
			continue
		}
		name := strings.TrimSpace(string(it.FieldID))
		if name == "" {
			name = strings.TrimSpace(string(it.Field))
		}
		var field api.IssueField
		switch strings.ToLower(name) {
		case "status":
			field = api.IssueFieldStatus
		case "assignee":
			field = api.IssueFieldAssignee
		default:
			continue
		}
		h.Changes = append(h.Changes, Change{
			Field:  field,
			From:   cleanText(string(it.FromString), maxNameBytes),
			To:     cleanText(string(it.ToString), maxNameBytes),
			FromID: cleanUserID(string(it.From)),
			ToID:   cleanUserID(string(it.To)),
		})
		if len(h.Changes) == maxChanges {
			break
		}
	}
	return h, len(h.Changes) > 0
}

// histories converts, deduplicates, orders (oldest first) and caps the
// changelog entries of one issue.
func histories(raws []json.RawMessage, issueID string, into []History) []History {
	seen := make(map[string]bool, len(into))
	for _, h := range into {
		seen[h.ID] = true
	}
	for _, raw := range raws {
		var w wireHistory
		if !decodeLenient(raw, &w) {
			continue
		}
		h, ok := historyOf(&w, issueID)
		if !ok || seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		into = append(into, h)
	}
	sort.SliceStable(into, func(i, j int) bool {
		a, b := into[i], into[j]
		if !a.Created.Equal(b.Created) {
			return a.Created.Before(b.Created)
		}
		return idLess(a.ID, b.ID)
	})
	if len(into) > maxHistories {
		into = into[len(into)-maxHistories:]
	}
	return into
}

// spacesOf converts projects, dropping unusable and duplicate ones.
func spacesOf(raws []json.RawMessage, seen map[string]bool, into []Space) []Space {
	for _, raw := range raws {
		if len(into) == maxSpaces {
			break
		}
		var w wireProject
		if !decodeLenient(raw, &w) {
			continue
		}
		sp := Space{
			ID:          cleanID(w.ID),
			Key:         cleanSpaceKey(w.Key),
			Name:        cleanText(string(w.Name), maxNameBytes),
			ServiceDesk: strings.EqualFold(strings.TrimSpace(string(w.ProjectTypeKey)), "service_desk"),
		}
		if sp.ID == "" || sp.Key == "" || seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		into = append(into, sp)
	}
	return into
}

// checkJQL refuses an empty or oversized query.
func checkJQL(jql string) error {
	if strings.TrimSpace(jql) == "" || len(jql) > maxJQLBytes {
		return api.NewError(api.CodeInvalidArgument, "jira: empty or oversized JQL")
	}
	return nil
}

// checkIDs refuses ids that cannot be the site's; it returns them
// without duplicates.
func checkIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if cleanID(flexString(id)) != id {
			return nil, api.NewError(api.CodeInvalidArgument, "jira: bad issue id")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func chunks(ids []string, n int) [][]string {
	var out [][]string
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

func pageSize(n, def, limit int) int {
	if n <= 0 {
		return def
	}
	return min(n, limit)
}

// JQLValue quotes s as a JQL value: a number stays bare, anything else
// becomes a double-quoted string with '"' and '\' escaped.
func JQLValue(s string) string {
	if isDigits(s) {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// OpenContent downloads an attachment or a picture of the site, at most
// limit bytes. rawURL may be absolute or relative to the site; it must
// lie within the site (its origin and path) or, with a cloud id, within
// the gateway route. It is fetched through the route in use (a site URL
// through the gateway once the gateway is the route, and back), with the
// credentials; a redirect to another host (Jira Cloud sends attachment
// bytes from its media service) is followed over https without them.
// A declared length over limit fails at once, a body that grows past it
// when read; both with ErrTooLarge. Any other status is a *StatusError.
func (c *Client) OpenContent(ctx context.Context, rawURL string, limit int64) (*Content, error) {
	if limit <= 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: content limit must be positive")
	}
	path, err := c.routePath(rawURL)
	if err != nil {
		return nil, err
	}
	resp, err := c.exchange(ctx, request{method: http.MethodGet, path: path, accept: "*/*", crossHost: true, timeout: contentTimeout})
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > limit {
		drain(resp)
		return nil, ErrTooLarge
	}
	return &Content{
		Body:      &cappedBody{rc: resp.Body, left: limit},
		Length:    resp.ContentLength,
		MediaType: cleanMediaType(resp.Header.Get("Content-Type")),
	}, nil
}

// routePath is the part of a site or gateway URL after the route's base,
// with its query; an error when the URL lies outside both. With the
// gateway route, a cloud /secure/attachment/<id>/... or
// /secure/thumbnail/<id>/... link (which the gateway does not serve)
// becomes the REST content or thumbnail endpoint of the attachment.
func (c *Client) routePath(rawURL string) (string, error) {
	abs := absoluteURL(c.site, rawURL)
	if abs == "" {
		return "", api.NewError(api.CodeInvalidArgument, "jira: not a content URL")
	}
	u, _ := url.Parse(abs)
	rel, ok := within(u, c.site)
	if !ok && c.gatewayBase != "" {
		g, _ := url.Parse(c.gatewayBase)
		rel, ok = within(u, g)
	}
	if !ok {
		return "", api.NewError(api.CodeInvalidArgument, "jira: content URL outside the site")
	}
	if c.Gateway() && c.deployment == api.JiraCloud {
		for prefix, endpoint := range map[string]string{
			"/secure/attachment/": "/rest/api/3/attachment/content/",
			"/secure/thumbnail/":  "/rest/api/3/attachment/thumbnail/",
		} {
			if rest, found := strings.CutPrefix(rel, prefix); found {
				if id, _, _ := strings.Cut(rest, "/"); cleanID(flexString(id)) != "" {
					rel = endpoint + id
				}
			}
		}
	}
	if u.RawQuery != "" {
		rel += "?" + u.RawQuery
	}
	return rel, nil
}

// within reports whether u lies under base (same host and port, a scheme
// no weaker, the base's path a prefix at a segment boundary) and returns
// the escaped path after it.
func within(u, base *url.URL) (string, bool) {
	if !sameHost(u, base) || (base.Scheme == "https" && u.Scheme != "https") {
		return "", false
	}
	p, bp := u.EscapedPath(), strings.TrimRight(base.EscapedPath(), "/")
	if p == bp {
		return "/", true
	}
	rest, ok := strings.CutPrefix(p, bp+"/")
	if !ok {
		return "", false
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == ".." || seg == "." || strings.EqualFold(seg, "%2e%2e") {
			return "", false
		}
	}
	return "/" + rest, true
}

// cappedBody fails a read that goes past the limit.
type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - int(-b.left), ErrTooLarge
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// AllIDs pages SearchIDs through the whole result, at most limit ids (0
// = no cap beyond maxPages pages). complete is false when it stopped at
// the limit. A cursor that repeats is a malformed answer, not an endless
// loop.
func AllIDs(ctx context.Context, r Remote, jql string, limit int) (ids []string, complete bool, err error) {
	seen := map[string]bool{}
	cursors := map[Cursor]bool{}
	var cur Cursor
	for page := 0; page < maxPages; page++ {
		p, err := r.SearchIDs(ctx, jql, cur)
		if err != nil {
			return nil, false, err
		}
		for _, id := range p.IDs {
			if seen[id] {
				continue
			}
			if limit > 0 && len(ids) == limit {
				return ids, false, nil
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if p.Next == "" {
			return ids, true, nil
		}
		if cursors[p.Next] {
			return nil, false, api.NewError(api.CodeServerError, "jira: search does not advance")
		}
		cursors[p.Next] = true
		cur = p.Next
	}
	return ids, false, nil
}

// SearchAll pages Search from req.Cursor to the end, handing each page's
// issues to fn, at most limit issues in all (0 = no cap beyond maxPages
// pages). An issue a later page repeats is not handed over again.
// complete is false when it stopped at the limit.
func SearchAll(ctx context.Context, r Remote, req SearchRequest, limit int, fn func([]Issue) error) (complete bool, err error) {
	seen := map[string]bool{}
	cursors := map[Cursor]bool{}
	total := 0
	for page := 0; page < maxPages; page++ {
		p, err := r.Search(ctx, req)
		if err != nil {
			return false, err
		}
		batch := p.Issues[:0:0]
		stopped := false
		for _, is := range p.Issues {
			if seen[is.ID] {
				continue
			}
			if limit > 0 && total == limit {
				stopped = true
				break
			}
			seen[is.ID] = true
			total++
			batch = append(batch, is)
		}
		if len(batch) > 0 {
			if err := fn(batch); err != nil {
				return false, err
			}
		}
		if stopped {
			return false, nil
		}
		if p.Next == "" {
			return true, nil
		}
		if cursors[p.Next] {
			return false, api.NewError(api.CodeServerError, "jira: search does not advance")
		}
		cursors[p.Next] = true
		req.Cursor = p.Next
	}
	return false, nil
}

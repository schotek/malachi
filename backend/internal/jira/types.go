// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The subset of Jira's REST resources the package reads, for both
// flavours (cloud /rest/api/3, datacenter /rest/api/2). Decoding is
// strict for the envelope of a page (a list that is not a list is a
// malformed response, never an empty page) and tolerant for the items in
// it: a scalar of the wrong type decodes as its zero value, an item that
// does not decode at all is skipped. Descriptions and comment bodies are
// read only as the server's rendered HTML; raw ADF and wiki markup are
// never decoded.

// Limits on what is kept of the site's text (hostile input).
const (
	maxIDBytes       = 64      // entity ids (issue, comment, history, attachment, space, status)
	maxUserIDBytes   = 255     // user ids (accountId, key or name)
	maxKeyBytes      = 80      // issue and space keys
	maxNameBytes     = 256     // display names, status, type, priority, space names
	maxSummaryBytes  = 1024    // an issue's summary (Jira allows 255 characters)
	maxFilenameBytes = 255     // attachment file names
	maxURLBytes      = 2048    // attachment content URLs
	maxHTMLBytes     = 4 << 20 // one rendered description or comment
	maxTitleBytes    = 256     // serverInfo title
	maxVersionBytes  = 64      // serverInfo version
	maxEmailBytes    = 254
	maxCursorBytes   = 8192 // a cloud nextPageToken
	maxJQLBytes      = 64 << 10
	maxPropertyBytes = 4096 // one comment property value
	maxProperties    = 32   // properties kept per comment
	maxAttachments   = 500  // per issue
	maxChanges       = 20   // status/assignee items kept per history entry
)

// flexString decodes a JSON string or number (ids arrive as either);
// null, booleans, objects and arrays decode as "".
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0:
		*s = ""
	case b[0] == '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = flexString(v)
	case b[0] == '-' || (b[0] >= '0' && b[0] <= '9'):
		*s = flexString(b)
	default:
		*s = ""
	}
	return nil
}

// flexBool decodes true/false, "true"/"false"; anything else is false.
type flexBool bool

func (v *flexBool) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch string(b) {
	case "true", `"true"`:
		*v = true
	default:
		*v = false
	}
	return nil
}

// flexInt decodes a JSON number or numeric string; anything else, a
// fraction or an overflow is 0.
type flexInt int64

func (v *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(bytes.TrimSpace(b), `"`)
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		n = 0
	}
	*v = flexInt(n)
	return nil
}

// decodeStrict decodes the envelope of a response: any mismatch is a
// malformed response (serverError).
func decodeStrict(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return api.NewError(api.CodeServerError, "jira: malformed response: %s", transport.CleanMessage(err.Error()))
	}
	return nil
}

// decodeLenient decodes one item: a value of the wrong type leaves its
// field zero (encoding/json carries on after an UnmarshalTypeError). It
// reports false when data does not decode at all.
func decodeLenient(data []byte, v any) bool {
	err := json.Unmarshal(data, v)
	var ute *json.UnmarshalTypeError
	return err == nil || errors.As(err, &ute)
}

// Wire types.

type wireUser struct {
	AccountID    flexString `json:"accountId"` // cloud
	Key          flexString `json:"key"`       // datacenter (stable)
	Name         flexString `json:"name"`      // datacenter (user name, may be renamed)
	DisplayName  flexString `json:"displayName"`
	EmailAddress flexString `json:"emailAddress"`
	TimeZone     flexString `json:"timeZone"`
}

type wireProject struct {
	ID             flexString `json:"id"`
	Key            flexString `json:"key"`
	Name           flexString `json:"name"`
	ProjectTypeKey flexString `json:"projectTypeKey"`
}

type wireStatusCategory struct {
	Key flexString `json:"key"`
}

type wireStatus struct {
	ID             flexString          `json:"id"`
	Name           flexString          `json:"name"`
	StatusCategory *wireStatusCategory `json:"statusCategory"`
}

type wireNamed struct {
	ID   flexString `json:"id"`
	Name flexString `json:"name"`
}

type wireAttachment struct {
	ID       flexString `json:"id"`
	Filename flexString `json:"filename"`
	Size     flexInt    `json:"size"`
	MimeType flexString `json:"mimeType"`
	Content  flexString `json:"content"`
	Created  flexString `json:"created"`
}

type wireWatches struct {
	IsWatching flexBool `json:"isWatching"`
}

type wireIssueFields struct {
	Summary    flexString        `json:"summary"`
	Status     *wireStatus       `json:"status"`
	IssueType  *wireNamed        `json:"issuetype"`
	Priority   *wireNamed        `json:"priority"`
	Assignee   *wireUser         `json:"assignee"`
	Reporter   *wireUser         `json:"reporter"`
	Created    flexString        `json:"created"`
	Updated    flexString        `json:"updated"`
	Project    *wireProject      `json:"project"`
	Attachment []json.RawMessage `json:"attachment"`
	Watches    *wireWatches      `json:"watches"`
}

type wireRendered struct {
	Description flexString `json:"description"`
}

type wireIssue struct {
	ID             flexString      `json:"id"`
	Key            flexString      `json:"key"`
	Fields         wireIssueFields `json:"fields"`
	RenderedFields *wireRendered   `json:"renderedFields"`
}

type wireProperty struct {
	Key   flexString      `json:"key"`
	Value json.RawMessage `json:"value"`
}

type wireComment struct {
	ID           flexString        `json:"id"`
	Author       *wireUser         `json:"author"`
	UpdateAuthor *wireUser         `json:"updateAuthor"`
	RenderedBody flexString        `json:"renderedBody"`
	Created      flexString        `json:"created"`
	Updated      flexString        `json:"updated"`
	JsdPublic    *flexBool         `json:"jsdPublic"`
	Properties   []json.RawMessage `json:"properties"`
}

type wireCommentPage struct {
	StartAt    flexInt            `json:"startAt"`
	MaxResults flexInt            `json:"maxResults"`
	Total      flexInt            `json:"total"`
	Comments   *[]json.RawMessage `json:"comments"`
}

type wireChangeItem struct {
	Field      flexString `json:"field"`
	FieldType  flexString `json:"fieldtype"`
	FieldID    flexString `json:"fieldId"`
	From       flexString `json:"from"`
	FromString flexString `json:"fromString"`
	To         flexString `json:"to"`
	ToString   flexString `json:"toString"`
}

type wireHistory struct {
	ID      flexString       `json:"id"`
	Author  *wireUser        `json:"author"`
	Created flexString       `json:"created"`
	Items   []wireChangeItem `json:"items"`
}

type wireChangelog struct {
	StartAt    flexInt           `json:"startAt"`
	MaxResults flexInt           `json:"maxResults"`
	Total      flexInt           `json:"total"`
	Histories  []json.RawMessage `json:"histories"`
}

// wireTransition is one entry of GET /issue/{id}/transitions. Fields is
// present with expand=transitions.fields: the fields of the transition's
// screen, keyed by field id; only "required" is read of each.
type wireTransition struct {
	ID        flexString                      `json:"id"`
	Name      flexString                      `json:"name"`
	To        *wireStatus                     `json:"to"`
	HasScreen flexBool                        `json:"hasScreen"`
	Available *flexBool                       `json:"isAvailable"`
	Fields    map[string]*wireTransitionField `json:"fields"`
}

type wireTransitionField struct {
	Required flexBool `json:"required"`
}

type wireServerInfo struct {
	BaseURL        *flexString `json:"baseUrl"`
	Version        *flexString `json:"version"`
	DeploymentType *flexString `json:"deploymentType"`
	ServerTitle    *flexString `json:"serverTitle"`
	BuildNumber    *flexInt    `json:"buildNumber"`
}

// String cleaning.

var (
	entityIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	spaceKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	issueKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]{1,18}$`)
	timeZonePattern = regexp.MustCompile(`^[A-Za-z0-9/_+-]{1,64}$`)
	mimeTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/[a-z0-9][a-z0-9!#$&^_.+-]{0,127}$`)
)

// cleanText makes a line of the site's display text safe to keep: valid
// UTF-8, control characters and line or paragraph separators turned into
// spaces, bidirectional overrides, zero-width spaces and byte order marks
// dropped, trimmed, and at most limit bytes, cut at a rune boundary.
func cleanText(s string, limit int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == 0x2028, r == 0x2029:
			return ' '
		case isInvisible(r):
			return -1
		}
		return r
	}, s)
	return truncate(strings.TrimSpace(s), limit)
}

// isInvisible reports the characters that can make displayed text lie
// about itself: bidirectional embeddings, overrides and isolates, the
// directional marks, zero-width spaces, the byte order mark and the soft
// hyphen. Zero-width joiners stay (scripts and emoji need them).
func isInvisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x200B, r == 0x200E, r == 0x200F, r == 0x061C, r == 0xFEFF, r == 0x00AD, r == 0x2060:
		return true
	}
	return false
}

// truncate cuts s to at most limit bytes at a rune boundary.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for s != "" && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}

// cleanHTML keeps the site's HTML as valid UTF-8 without NUL bytes and at
// most maxHTMLBytes; everything else is the sanitiser's job at display.
func cleanHTML(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	return truncate(s, maxHTMLBytes)
}

// cleanID is an entity id (numeric on every Jira, but only its shape is
// assumed): letters, digits, '_' and '-', at most maxIDBytes; "" when it
// is not one.
func cleanID(s flexString) string {
	v := strings.TrimSpace(string(s))
	if len(v) > maxIDBytes || !entityIDPattern.MatchString(v) {
		return ""
	}
	return v
}

// cleanUserID is a user id: display-clean and at most maxUserIDBytes; ""
// when longer (never cut: a cut id could name someone else).
func cleanUserID(s string) string {
	v := cleanText(s, maxUserIDBytes+1)
	if len(v) > maxUserIDBytes {
		return ""
	}
	return v
}

func cleanSpaceKey(s flexString) string {
	v := strings.TrimSpace(string(s))
	if len(v) > maxKeyBytes || !spaceKeyPattern.MatchString(v) {
		return ""
	}
	return v
}

func cleanIssueKey(s flexString) string {
	v := strings.TrimSpace(string(s))
	if len(v) > maxKeyBytes || !issueKeyPattern.MatchString(v) {
		return ""
	}
	return v
}

func cleanEmail(s flexString) string {
	v := cleanText(string(s), maxEmailBytes+1)
	if len(v) > maxEmailBytes || strings.ContainsAny(v, " <>\"") || strings.Count(v, "@") != 1 ||
		strings.HasPrefix(v, "@") || strings.HasSuffix(v, "@") {
		return ""
	}
	return v
}

func cleanTimeZone(s flexString) string {
	v := strings.TrimSpace(string(s))
	if !timeZonePattern.MatchString(v) {
		return ""
	}
	return v
}

// cleanMediaType is the server's claim about a file's type, lower-cased
// and without parameters; "" when it does not look like a media type. It
// is untrusted: whoever uses the bytes sniffs them.
func cleanMediaType(s string) string {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	mt = strings.ToLower(mt)
	if !mimeTypePattern.MatchString(mt) {
		return ""
	}
	return mt
}

// cleanFilename is a display-clean file name without path separators.
func cleanFilename(s flexString) string {
	v := cleanText(string(s), maxFilenameBytes)
	return strings.NewReplacer("/", "_", `\`, "_").Replace(v)
}

// statusCategory maps a status category key ("new", "indeterminate",
// "done"; the statuses/search spelling "TODO", "IN_PROGRESS", "DONE" too)
// to the contract's; "" when unknown.
func statusCategory(key string) api.IssueStatusCategory {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "new", "undefined", "todo", "to_do":
		return api.StatusCategoryTodo
	case "indeterminate", "in_progress", "inprogress":
		return api.StatusCategoryInProgress
	case "done":
		return api.StatusCategoryDone
	}
	return ""
}

// parseTime reads Jira's time stamps ("2024-01-15T10:30:00.000+0100",
// with or without the colon in the offset, with or without fractions, or
// "Z"). Anything else, and a year outside 1970..9999, is the zero time.
// The result is UTC.
func parseTime(s flexString) time.Time {
	v := strings.TrimSpace(string(s))
	if v == "" || len(v) > 64 {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		// A number is an epoch: POST /changelog/bulkfetch gives a history's
		// created as milliseconds; anything small enough is seconds.
		if n < 0 {
			return time.Time{}
		}
		var t time.Time
		if n > 1e11 {
			t = time.UnixMilli(n)
		} else {
			t = time.Unix(n, 0)
		}
		if t.Year() > 9999 {
			return time.Time{}
		}
		return t.UTC()
	}
	for _, layout := range []string{"2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04Z0700", "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, v); err == nil {
			if t.Year() < 1970 || t.Year() > 9999 {
				return time.Time{}
			}
			return t.UTC()
		}
	}
	return time.Time{}
}

// absoluteURL resolves ref against base and returns it when it is an
// http(s) URL without user info, at most maxURLBytes; "" otherwise.
func absoluteURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxURLBytes || strings.ContainsFunc(ref, unicode.IsControl) {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment, u.RawFragment = "", ""
	s := u.String()
	if len(s) > maxURLBytes {
		return ""
	}
	return s
}

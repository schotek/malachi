// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGraph is an in-memory mailbox behind the subset of the Graph API the
// engine uses. Every change bumps seq; delta tokens carry the seq they
// were issued at, so a delta query returns what changed since.
type fakeGraph struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	token    string
	me       string
	seq      int
	pageSize int
	folders  []*fakeFolder
	messages map[string]*fakeMsg
	removed  []removal
	requests []string
	sent     [][]byte

	throttle       int  // answer 429 to this many requests
	unauthorized   int  // answer 401 to this many requests
	staleTokens    bool // reject every delta token with 410
	noPermanentDel bool // permanentDelete is unsupported (400)
	failValue      map[string]int
}

type fakeFolder struct {
	id, name, parent, wellKnown string
}

type fakeMsg struct {
	id, folder, subject, from, messageID, conversation string
	received                                           time.Time
	isRead, flagged, isDraft                           bool
	mime                                               string
	version                                            int
}

type removal struct {
	folder, id string
	seq        int
}

func newFakeGraph(t *testing.T) *fakeGraph {
	f := &fakeGraph{t: t, token: "tok-1", me: "me@contoso.invalid", pageSize: 2, messages: map[string]*fakeMsg{}, failValue: map[string]int{}}
	f.folders = []*fakeFolder{
		{id: "F-INBOX", name: "Inbox", parent: "ROOT", wellKnown: "inbox"},
		{id: "F-SENT", name: "Sent Items", parent: "ROOT", wellKnown: "sentitems"},
		{id: "F-DRAFTS", name: "Drafts", parent: "ROOT", wellKnown: "drafts"},
		{id: "F-TRASH", name: "Deleted Items", parent: "ROOT", wellKnown: "deleteditems"},
		{id: "F-JUNK", name: "Junk Email", parent: "ROOT", wellKnown: "junkemail"},
		{id: "F-PROJ", name: "Projects", parent: "ROOT"},
		{id: "F-PROJ-A", name: "Alpha/Beta", parent: "F-PROJ"},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGraph) bump() int { f.seq++; return f.seq }

// add stores a message and returns its id.
func (f *fakeGraph) add(folder, subject, from string, received time.Time, body string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("AAk%03d", len(f.messages)+1)
	mid := strings.ReplaceAll(subject, " ", "") + "@example.test"
	m := &fakeMsg{
		id: id, folder: folder, subject: subject, from: from, messageID: "<" + mid + ">", conversation: "conv-" + subject,
		received: received, version: f.bump(),
		mime: "From: " + from + "\r\nTo: me@contoso.invalid\r\nSubject: " + subject + "\r\nMessage-ID: <" + mid + ">\r\n" +
			"Date: " + received.Format(time.RFC1123Z) + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n",
	}
	f.messages[id] = m
	return id
}

func (f *fakeGraph) setRead(id string, read bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.messages[id]
	m.isRead = read
	m.version = f.bump()
}

func (f *fakeGraph) move(id, folder string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.messages[id]
	f.removed = append(f.removed, removal{folder: m.folder, id: id, seq: f.bump()})
	m.folder = folder
	m.version = f.bump()
}

func (f *fakeGraph) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.messages[id]
	f.removed = append(f.removed, removal{folder: m.folder, id: id, seq: f.bump()})
	delete(f.messages, id)
}

func (f *fakeGraph) get(id string) (fakeMsg, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.messages[id]
	if !ok {
		return fakeMsg{}, false
	}
	return *m, true
}

func (f *fakeGraph) count(method, pathPrefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, method+" "+pathPrefix) {
			n++
		}
	}
	return n
}

func (f *fakeGraph) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.token || f.unauthorized > 0 {
		if f.unauthorized > 0 {
			f.unauthorized--
		}
		f.fail(w, http.StatusUnauthorized, "InvalidAuthenticationToken", "token "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return
	}
	if f.throttle > 0 {
		f.throttle--
		w.Header().Set("Retry-After", "0")
		f.fail(w, http.StatusTooManyRequests, "TooManyRequests", "slow down")
		return
	}
	if !strings.Contains(r.Header.Get("Prefer"), `IdType="ImmutableId"`) {
		f.fail(w, http.StatusBadRequest, "BadPrefer", "immutable ids not requested")
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/me")
	switch {
	case r.Method == "GET" && p == "":
		f.reply(w, map[string]string{"mail": f.me, "userPrincipalName": f.me})
	case r.Method == "GET" && p == "/mailFolders":
		f.listFolders(w, r, "ROOT")
	case r.Method == "GET" && strings.HasPrefix(p, "/mailFolders/") && strings.HasSuffix(p, "/childFolders"):
		f.listFolders(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/mailFolders/"), "/childFolders"))
	case r.Method == "GET" && strings.HasPrefix(p, "/mailFolders/") && strings.HasSuffix(p, "/messages/delta"):
		f.delta(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/mailFolders/"), "/messages/delta"))
	case r.Method == "GET" && strings.HasPrefix(p, "/mailFolders/"):
		name := strings.TrimPrefix(p, "/mailFolders/")
		for _, fo := range f.folders {
			if fo.wellKnown == name || fo.id == name {
				f.reply(w, map[string]any{"id": fo.id, "displayName": fo.name})
				return
			}
		}
		f.fail(w, http.StatusNotFound, "ErrorFolderNotFound", "no such folder")
	case r.Method == "GET" && strings.HasPrefix(p, "/messages/") && strings.HasSuffix(p, "/$value"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/messages/"), "/$value")
		if n := f.failValue[id]; n > 0 {
			f.failValue[id]--
			f.fail(w, http.StatusServiceUnavailable, "ServiceUnavailable", "try later")
			return
		}
		m, ok := f.messages[id]
		if !ok {
			f.fail(w, http.StatusNotFound, "ErrorItemNotFound", "gone")
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		io.WriteString(w, m.mime)
	case r.Method == "PATCH" && strings.HasPrefix(p, "/messages/"):
		m, ok := f.messages[strings.TrimPrefix(p, "/messages/")]
		if !ok {
			f.fail(w, http.StatusNotFound, "ErrorItemNotFound", "gone")
			return
		}
		var body struct {
			IsRead *bool `json:"isRead"`
			Flag   *struct {
				FlagStatus string `json:"flagStatus"`
			} `json:"flag"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.IsRead != nil {
			m.isRead = *body.IsRead
		}
		if body.Flag != nil {
			m.flagged = body.Flag.FlagStatus == "flagged"
		}
		m.version = f.bump()
		f.reply(w, map[string]any{"id": m.id})
	case r.Method == "POST" && strings.HasSuffix(p, "/move"):
		m, ok := f.messages[strings.TrimSuffix(strings.TrimPrefix(p, "/messages/"), "/move")]
		if !ok {
			f.fail(w, http.StatusNotFound, "ErrorItemNotFound", "gone")
			return
		}
		var body struct {
			DestinationID string `json:"destinationId"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.removed = append(f.removed, removal{folder: m.folder, id: m.id, seq: f.bump()})
		m.folder = body.DestinationID
		m.version = f.bump()
		f.reply(w, map[string]any{"id": m.id, "parentFolderId": m.folder})
	case r.Method == "POST" && strings.HasSuffix(p, "/permanentDelete"):
		if f.noPermanentDel {
			f.fail(w, http.StatusBadRequest, "BadRequest", "unknown action")
			return
		}
		f.deleteMsg(w, strings.TrimSuffix(strings.TrimPrefix(p, "/messages/"), "/permanentDelete"))
	case r.Method == "DELETE" && strings.HasPrefix(p, "/messages/"):
		f.deleteMsg(w, strings.TrimPrefix(p, "/messages/"))
	case r.Method == "POST" && p == "/sendMail":
		if r.Header.Get("Content-Type") != "text/plain" {
			f.fail(w, http.StatusBadRequest, "ErrorMimeContentInvalid", "not MIME")
			return
		}
		raw, _ := io.ReadAll(r.Body)
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			f.fail(w, http.StatusBadRequest, "ErrorMimeContentInvalidBase64String", "Invalid base64 string for MIME content.")
			return
		}
		if strings.Contains(string(decoded), "Subject: too big") {
			f.fail(w, http.StatusRequestEntityTooLarge, "ErrorMessageSizeExceeded", "too big")
			return
		}
		f.sent = append(f.sent, decoded)
		w.WriteHeader(http.StatusAccepted)
	default:
		f.fail(w, http.StatusNotFound, "ResourceNotFound", "unhandled "+r.Method+" "+p)
	}
}

func (f *fakeGraph) deleteMsg(w http.ResponseWriter, id string) {
	m, ok := f.messages[id]
	if !ok {
		f.fail(w, http.StatusNotFound, "ErrorItemNotFound", "gone")
		return
	}
	f.removed = append(f.removed, removal{folder: m.folder, id: id, seq: f.bump()})
	delete(f.messages, id)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGraph) listFolders(w http.ResponseWriter, r *http.Request, parent string) {
	var out []map[string]any
	for _, fo := range f.folders {
		if fo.parent != parent {
			continue
		}
		children, total, unread := 0, 0, 0
		for _, c := range f.folders {
			if c.parent == fo.id {
				children++
			}
		}
		for _, m := range f.messages {
			if m.folder == fo.id {
				total++
				if !m.isRead {
					unread++
				}
			}
		}
		out = append(out, map[string]any{
			"id": fo.id, "displayName": fo.name, "parentFolderId": fo.parent,
			"childFolderCount": children, "totalItemCount": total, "unreadItemCount": unread,
		})
	}
	f.reply(w, map[string]any{"value": out})
}

func (f *fakeGraph) delta(w http.ResponseWriter, r *http.Request, folder string) {
	q := r.URL.Query()
	sinceSeq := -1
	if tok := q.Get("$deltatoken"); tok != "" {
		if f.staleTokens {
			f.fail(w, http.StatusGone, "SyncStateNotFound", "resync")
			return
		}
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "v"))
		if err != nil {
			f.fail(w, http.StatusBadRequest, "BadDeltaToken", tok)
			return
		}
		sinceSeq = n
	}
	skip := 0
	if s := q.Get("$skiptoken"); s != "" {
		skip, _ = strconv.Atoi(s)
	}
	var since time.Time
	if flt := q.Get("$filter"); flt != "" {
		const prefix = "receivedDateTime ge "
		if !strings.HasPrefix(flt, prefix) {
			f.fail(w, http.StatusBadRequest, "BadFilter", flt)
			return
		}
		var err error
		if since, err = time.Parse(time.RFC3339, strings.TrimPrefix(flt, prefix)); err != nil {
			f.fail(w, http.StatusBadRequest, "BadFilter", flt)
			return
		}
	}
	// Build the full change list, then page it.
	var items []map[string]any
	var ids []string
	for id := range f.messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := f.messages[id]
		if m.folder != folder || m.version <= sinceSeq || (!since.IsZero() && m.received.Before(since)) {
			continue
		}
		item := map[string]any{
			"id": m.id, "internetMessageId": m.messageID, "subject": m.subject,
			"from":             map[string]any{"emailAddress": map[string]string{"name": "Sender", "address": m.from}},
			"toRecipients":     []any{map[string]any{"emailAddress": map[string]string{"address": f.me}}},
			"receivedDateTime": m.received.UTC().Format(time.RFC3339), "sentDateTime": m.received.Add(-time.Minute).UTC().Format(time.RFC3339),
			"isRead": m.isRead, "isDraft": m.isDraft, "hasAttachments": false, "conversationId": m.conversation, "parentFolderId": m.folder,
			"flag": map[string]string{"flagStatus": map[bool]string{true: "flagged", false: "notFlagged"}[m.flagged]},
		}
		items = append(items, item)
	}
	if sinceSeq >= 0 {
		for _, rm := range f.removed {
			if rm.folder == folder && rm.seq > sinceSeq {
				items = append(items, map[string]any{"id": rm.id, "@removed": map[string]string{"reason": "deleted"}})
			}
		}
	}
	end := min(len(items), skip+f.pageSize)
	pg := map[string]any{"value": items[skip:end]}
	base := f.srv.URL + r.URL.Path
	if end < len(items) {
		nq := url.Values{}
		for k, v := range q {
			nq[k] = v
		}
		nq.Set("$skiptoken", strconv.Itoa(end))
		pg["@odata.nextLink"] = base + "?" + nq.Encode()
	} else {
		pg["@odata.deltaLink"] = base + "?$deltatoken=v" + strconv.Itoa(f.seq)
	}
	f.reply(w, pg)
}

func (f *fakeGraph) reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (f *fakeGraph) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

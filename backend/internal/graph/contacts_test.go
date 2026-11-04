// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeContacts is an in-memory Graph contacts service: folders, contacts
// as JSON maps, a delta per folder with removals.
type fakeContacts struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	seq      int
	pageSize int
	folders  []map[string]any            // top level
	children map[string][]map[string]any // folder id → child folders
	items    map[string]map[string]any   // id → contact
	folderOf map[string]string
	ver      map[string]int
	itemSeq  map[string]int
	removed  []struct {
		folder, id string
		seq        int
	}
	tokens   map[string]int // delta token → seq
	reqs     []fakeContactReq
	fail     map[string]fakeFailure // method+" "+path, answered once
	honour   bool                   // answer If-Match with 412
	editOnce func()                 // runs between the version GET and the write
}

type fakeContactReq struct {
	Method, Path, Query, IfMatch, Prefer string
	Body                                 map[string]any
}

func newFakeContacts(t *testing.T) *fakeContacts {
	f := &fakeContacts{t: t, pageSize: 2, children: map[string][]map[string]any{}, items: map[string]map[string]any{},
		folderOf: map[string]string{}, ver: map[string]int{}, itemSeq: map[string]int{}, tokens: map[string]int{},
		fail: map[string]fakeFailure{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeContacts) client() *Client {
	return NewClient(Options{BaseURL: f.srv.URL, HTTP: f.srv.Client(),
		Token: func(context.Context) (string, error) { return "con-token", nil },
		Sleep: func(context.Context, time.Duration) error { return nil }})
}

func (f *fakeContacts) stampLocked(id string) {
	ck := fmt.Sprintf("ck-%s-%d", id, f.ver[id])
	f.items[id]["changeKey"] = ck
	f.items[id]["@odata.etag"] = `W/"` + ck + `"`
}

func (f *fakeContacts) add(folder string, c map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := c["id"].(string)
	f.items[id], f.folderOf[id], f.ver[id] = c, folder, 1
	f.seq++
	f.itemSeq[id] = f.seq
	f.stampLocked(id)
	return id
}

func (f *fakeContacts) edit(id string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editLocked(id, fields)
}

func (f *fakeContacts) editLocked(id string, fields map[string]any) {
	for k, v := range fields {
		f.items[id][k] = v
	}
	f.ver[id]++
	f.seq++
	f.itemSeq[id] = f.seq
	f.stampLocked(id)
}

func (f *fakeContacts) removeLocked(id string) {
	f.seq++
	f.removed = append(f.removed, struct {
		folder, id string
		seq        int
	}{f.folderOf[id], id, f.seq})
	delete(f.items, id)
}

func (f *fakeContacts) requests(method, path string) []fakeContactReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeContactReq
	for _, r := range f.reqs {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeContacts) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (f *fakeContacts) failWith(w http.ResponseWriter, status int, code string) {
	f.reply(w, status, map[string]any{"error": map[string]any{"code": code, "message": "fake " + code}})
}

func (f *fakeContacts) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer con-token" {
		f.failWith(w, 401, "InvalidAuthenticationToken")
		return
	}
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			f.t.Errorf("bad body %q", b)
		}
	}
	f.reqs = append(f.reqs, fakeContactReq{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		IfMatch: r.Header.Get("If-Match"), Prefer: r.Header.Get("Prefer"), Body: body})
	if fl, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		delete(f.fail, r.Method+" "+r.URL.Path)
		f.failWith(w, fl.status, fl.code)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	seg := strings.Split(p, "/")
	switch {
	case p == "me/contactFolders":
		f.reply(w, 200, map[string]any{"value": f.folders})
	case len(seg) == 4 && seg[0] == "me" && seg[1] == "contactFolders" && seg[3] == "childFolders":
		f.reply(w, 200, map[string]any{"value": f.children[seg[2]]})
	case p == "me/contacts/delta":
		f.delta(w, r, "default")
	case len(seg) == 5 && seg[1] == "contactFolders" && seg[3] == "contacts" && seg[4] == "delta":
		f.delta(w, r, seg[2])
	case r.Method == http.MethodPost && p == "me/contacts":
		f.create(w, "default", body)
	case r.Method == http.MethodPost && len(seg) == 4 && seg[1] == "contactFolders" && seg[3] == "contacts":
		f.create(w, seg[2], body)
	case len(seg) == 3 && seg[1] == "contacts":
		f.contact(w, r, seg[2], body)
	default:
		f.failWith(w, 404, "ResourceNotFound")
	}
}

func (f *fakeContacts) delta(w http.ResponseWriter, r *http.Request, folder string) {
	q := r.URL.Query()
	since := 0
	if tok := q.Get("token"); tok != "" {
		s, ok := f.tokens[tok]
		if !ok {
			f.failWith(w, 410, "SyncStateNotFound")
			return
		}
		since = s
	}
	var all []map[string]any
	for id, c := range f.items {
		if f.folderOf[id] == folder && f.itemSeq[id] > since {
			all = append(all, c)
		}
	}
	if since > 0 {
		for _, rm := range f.removed {
			if rm.folder == folder && rm.seq > since {
				all = append(all, map[string]any{"id": rm.id, "@removed": map[string]any{"reason": "deleted"}})
			}
		}
	}
	off := 0
	fmt.Sscan(q.Get("skip"), &off)
	end := min(off+f.pageSize, len(all))
	sortByID(all)
	out := map[string]any{"value": all[off:end]}
	base := f.srv.URL + r.URL.Path
	if end < len(all) {
		out["@odata.nextLink"] = fmt.Sprintf("%s?token=%s&skip=%d", base, q.Get("token"), end)
	} else {
		tok := fmt.Sprintf("t%d", f.seq)
		f.tokens[tok] = f.seq
		out["@odata.deltaLink"] = base + "?token=" + tok
	}
	f.reply(w, 200, out)
}

func sortByID(l []map[string]any) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && l[j]["id"].(string) < l[j-1]["id"].(string); j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

func (f *fakeContacts) create(w http.ResponseWriter, folder string, body map[string]any) {
	f.seq++
	id := fmt.Sprintf("NEW-%d", f.seq)
	body["id"] = id
	body["parentFolderId"] = folder
	f.items[id], f.folderOf[id], f.ver[id], f.itemSeq[id] = body, folder, 1, f.seq
	f.stampLocked(id)
	f.reply(w, 201, body)
}

func (f *fakeContacts) contact(w http.ResponseWriter, r *http.Request, id string, body map[string]any) {
	c, ok := f.items[id]
	if !ok {
		f.failWith(w, 404, "ErrorItemNotFound")
		return
	}
	if r.Method == http.MethodGet {
		f.reply(w, 200, c)
		if f.editOnce != nil {
			fn := f.editOnce
			f.editOnce = nil
			f.mu.Unlock()
			fn()
			f.mu.Lock()
		}
		return
	}
	if f.honour && r.Header.Get("If-Match") != c["@odata.etag"] {
		f.failWith(w, 412, "ErrorIrresolvableConflict")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		f.editLocked(id, body)
		f.reply(w, 200, f.items[id])
	case http.MethodDelete:
		f.removeLocked(id)
		w.WriteHeader(204)
	}
}

func ada() map[string]any {
	return map[string]any{
		"id": "C1", "displayName": "Ada‮ Lovelace", "givenName": "Ada", "surname": "Lovelace",
		"emailAddresses": []any{map[string]any{"name": "Ada", "address": "ada@example.org"}, map[string]any{"address": ""}},
		"businessPhones": []any{"+420 1\x00", " "}, "mobilePhone": "+420 777",
		"homeAddress": map[string]any{"street": "Line 1\r\nLine 2", "city": "Praha", "postalCode": "110 00", "countryOrRegion": "CZ"},
		"birthday":    "1815-12-10T11:59:00Z", "personalNotes": "a\n\n\n\nb",
		"businessHomePage": "https://example.org", "unknownField": "kept in raw",
	}
}

func TestContactFolders(t *testing.T) {
	f := newFakeContacts(t)
	f.folders = []map[string]any{{"id": "F1", "displayName": "Work\x07"}, {"id": "bad id"}, {"id": "F2", "displayName": "Family"}}
	f.children["F1"] = []map[string]any{{"id": "F1a", "displayName": "Clients"}, {"id": "F1"}}
	got, err := f.client().ContactFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ContactFolder{
		{ID: DefaultContactFolderID, Name: "Contacts", Default: true},
		{ID: "F1", Name: "Work"}, {ID: "F1a", Name: "Clients", ParentID: "F1"}, {ID: "F2", Name: "Family"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("folders %+v\nwant %+v", got, want)
	}
	if rs := f.requests("GET", "/me/contactFolders"); len(rs) != 1 || !strings.Contains(rs[0].Prefer, `IdType="ImmutableId"`) ||
		strings.Contains(rs[0].Prefer, "outlook.timezone") {
		t.Errorf("prefer %+v", rs)
	}
}

func TestContactFoldersAccessDenied(t *testing.T) {
	f := newFakeContacts(t)
	f.fail["GET /me/contactFolders"] = fakeFailure{403, "ErrorAccessDenied"}
	if _, err := f.client().ContactFolders(context.Background()); !errors.Is(err, ErrContactsAccessDenied) {
		t.Fatalf("err %v", err)
	}
}

func TestDeltaContactsPagingRemovedAndResync(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	f.add("default", ada())
	f.add("default", map[string]any{"id": "C2", "displayName": "Bob"})
	f.add("default", map[string]any{"id": "C3", "displayName": "Cyd"})
	f.add("default", map[string]any{"id": "bad\x01id", "displayName": "skipped"})
	f.add("F1", map[string]any{"id": "D1", "displayName": "Other folder"})
	c := f.client()

	var pages []ContactPage
	collect := func(p ContactPage) error { pages = append(pages, p); return nil }
	d, err := c.DeltaContacts(ctx, DefaultContactFolderID, "", collect)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Full || d.DeltaLink == "" || len(pages) != 2 {
		t.Fatalf("delta %+v pages %d", d, len(pages))
	}
	var ids []string
	var first Contact
	for _, p := range pages {
		if !p.Full || len(p.Removed) != 0 {
			t.Errorf("page %+v", p)
		}
		for _, ct := range p.Changed {
			ids = append(ids, ct.ID)
			if ct.ID == "C1" {
				first = ct
			}
		}
	}
	if strings.Join(ids, ",") != "C1,C2,C3" {
		t.Fatalf("ids %v", ids)
	}
	rs := f.requests("GET", "/me/contacts/delta")
	if !strings.Contains(rs[0].Query, "%24select=") && !strings.Contains(rs[0].Query, "$select=") || !strings.Contains(rs[0].Prefer, "odata.maxpagesize") {
		t.Errorf("first request %+v", rs[0])
	}
	// Cleaned and capped.
	if first.DisplayName != "Ada Lovelace" || first.ChangeKey != "ck-C1-1" || len(first.Emails) != 1 ||
		first.Emails[0] != (ContactEmail{Name: "Ada", Address: "ada@example.org"}) ||
		fmt.Sprint(first.BusinessPhones) != "[+420 1]" || first.HomeAddress.Street != "Line 1\nLine 2" ||
		first.HomeAddress.City != "Praha" || first.Birthday != "1815-12-10" || first.PersonalNotes != "a\n\nb" {
		t.Errorf("contact %+v", first)
	}
	if !strings.Contains(string(first.Raw), "kept in raw") {
		t.Errorf("raw %s", first.Raw)
	}

	// Increment: an edit, a removal.
	f.edit("C2", map[string]any{"displayName": "Bobby"})
	f.mu.Lock()
	f.removeLocked("C3")
	f.mu.Unlock()
	pages = nil
	d2, err := c.DeltaContacts(ctx, DefaultContactFolderID, d.DeltaLink, collect)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Full || len(pages) != 1 || len(pages[0].Changed) != 1 || pages[0].Changed[0].DisplayName != "Bobby" ||
		fmt.Sprint(pages[0].Removed) != "[C3]" || pages[0].Full {
		t.Fatalf("increment %+v %+v", d2, pages)
	}

	// A stale link (410 SyncStateNotFound): a full pass from scratch.
	pages = nil
	d3, err := c.DeltaContacts(ctx, DefaultContactFolderID, f.srv.URL+"/me/contacts/delta?token=gone", collect)
	if err != nil {
		t.Fatal(err)
	}
	if !d3.Full || len(pages) == 0 || !pages[len(pages)-1].Full {
		t.Fatalf("resync %+v %+v", d3, pages)
	}

	// Another folder; a link to another host is refused.
	pages = nil
	if _, err := c.DeltaContacts(ctx, "F1", "", collect); err != nil || len(pages) != 1 || pages[0].Changed[0].ID != "D1" {
		t.Fatalf("folder F1 %v %+v", err, pages)
	}
	if _, err := c.DeltaContacts(ctx, "F1", "https://evil.example/me/contacts/delta?token=x", collect); err == nil {
		t.Fatal("followed a link to another host")
	}
	// The sink's error stops the pass.
	stop := errors.New("stop")
	if _, err := c.DeltaContacts(ctx, DefaultContactFolderID, "", func(ContactPage) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("sink error %v", err)
	}
}

func TestContactBirthdayAndCaps(t *testing.T) {
	for in, want := range map[string]string{
		"1980-05-04T00:00:00Z": "1980-05-04", "1980-05-04": "1980-05-04", "": "", "garbage": "",
		"1980-13-04T00:00:00Z": "", strings.Repeat("1", 100): "",
	} {
		if got := birthdayOf(in); got != want {
			t.Errorf("birthdayOf(%q) = %q", in, got)
		}
	}
	raw, _ := json.Marshal(map[string]any{"id": "C9", "displayName": strings.Repeat("x", 5000), "personalNotes": strings.Repeat("n", MaxContactRawBytes)})
	ct, ok, err := contactOf(raw)
	if err != nil || !ok || len(ct.DisplayName) != MaxContactLineBytes || len(ct.PersonalNotes) > MaxContactNotesBytes || ct.Raw != nil {
		t.Fatalf("caps %v %v name %d notes %d raw %d", ok, err, len(ct.DisplayName), len(ct.PersonalNotes), len(ct.Raw))
	}
}

func TestCreateContact(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	c := f.client()
	ct, err := c.CreateContact(ctx, DefaultContactFolderID, ContactFields{GivenName: "Ada", Surname: "Lovelace",
		Emails: []ContactEmail{{Address: "ada@example.org"}}, Birthday: "1815-12-10"})
	if err != nil {
		t.Fatal(err)
	}
	posts := f.requests("POST", "/me/contacts")
	if len(posts) != 1 {
		t.Fatalf("posts %d", len(posts))
	}
	b := posts[0].Body
	if b["givenName"] != "Ada" || b["birthday"] != "1815-12-10T11:59:00Z" || b["displayName"] != nil || b["homeAddress"] != nil ||
		b["businessPhones"] != nil || len(b["emailAddresses"].([]any)) != 1 {
		t.Errorf("body %v", b)
	}
	if ct.ID == "" || ct.ChangeKey == "" || ct.Birthday != "1815-12-10" {
		t.Errorf("created %+v", ct)
	}
	if _, err := c.CreateContact(ctx, "F7", ContactFields{DisplayName: "In a folder"}); err != nil || len(f.requests("POST", "/me/contactFolders/F7/contacts")) != 1 {
		t.Fatalf("folder create %v", err)
	}
	for _, bad := range []ContactFields{
		{Emails: []ContactEmail{{Address: "a@x"}, {Address: "b@x"}, {Address: "c@x"}, {Address: "d@x"}}},
		{DisplayName: strings.Repeat("x", MaxContactLineBytes+1)},
		{Birthday: "10. 12. 1815"},
		{GivenName: "\xff"},
	} {
		if _, err := c.CreateContact(ctx, DefaultContactFolderID, bad); apiCodeOf(err) != api.CodeInvalidArgument {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestContactPatchOnlyChanged(t *testing.T) {
	before := ContactFields{GivenName: "Ada", Surname: "Lovelace", Emails: []ContactEmail{{Address: "ada@example.org"}},
		HomeAddress: ContactAddress{City: "London"}, Birthday: "1815-12-10"}
	after := before
	after.Surname = "King"
	after.HomeAddress.Street = "St James's Square"
	after.Emails = []ContactEmail{{Address: "ada@example.org"}}
	p, err := ContactPatch(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 || p["surname"] != "King" || p["homeAddress"].(ContactAddress).City != "London" {
		t.Fatalf("patch %v", p)
	}
	after = before
	after.Birthday = ""
	if p, _ := ContactPatch(before, after); len(p) != 1 || p["birthday"] != nil {
		t.Fatalf("clear birthday %v", p)
	} else if _, ok := p["birthday"]; !ok {
		t.Fatal("birthday not cleared")
	}
	if p, _ := ContactPatch(before, before); len(p) != 0 {
		t.Fatalf("no change %v", p)
	}
}

func TestUpdateContact(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	f.add("default", ada())
	c := f.client()
	cur, err := c.GetContact(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	after := cur.ContactFields
	after.JobTitle = "Analyst"
	patch, err := ContactPatch(cur.ContactFields, after)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.UpdateContact(ctx, "C1", cur.ChangeKey, patch)
	if err != nil {
		t.Fatal(err)
	}
	ps := f.requests("PATCH", "/me/contacts/C1")
	if len(ps) != 1 || len(ps[0].Body) != 1 || ps[0].Body["jobTitle"] != "Analyst" || ps[0].IfMatch != `W/"ck-C1-1"` {
		t.Fatalf("patch request %+v", ps)
	}
	if got.JobTitle != "Analyst" || got.ChangeKey != "ck-C1-2" {
		t.Fatalf("updated %+v", got)
	}
	if f.items["C1"]["unknownField"] != "kept in raw" {
		t.Fatal("PATCH lost a field it did not name")
	}
	if _, err := c.UpdateContact(ctx, "C1", got.ChangeKey, map[string]any{"photo": "x"}); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Fatalf("unknown key %v", err)
	}
}

func TestUpdateContactConflictByChangeKey(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	f.add("default", ada())
	c := f.client()
	f.edit("C1", map[string]any{"surname": "King"})
	_, err := c.UpdateContact(ctx, "C1", "ck-C1-1", map[string]any{"jobTitle": "Analyst"})
	var ce *ContactConflictError
	if !errors.Is(err, ErrContactConflict) || !errors.As(err, &ce) || ce.Server.Surname != "King" || ce.Server.ChangeKey != "ck-C1-2" {
		t.Fatalf("conflict %v", err)
	}
	if len(f.requests("PATCH", "/me/contacts/C1")) != 0 {
		t.Fatal("patched over a newer version")
	}
	if err := c.DeleteContact(ctx, "C1", "ck-C1-1"); !errors.Is(err, ErrContactConflict) {
		t.Fatalf("delete conflict %v", err)
	}
	if len(f.requests("DELETE", "/me/contacts/C1")) != 0 {
		t.Fatal("deleted a newer version")
	}
}

func TestUpdateContactConflictBy412(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	f.add("default", ada())
	f.honour = true
	// Another client edits between the version check and the PATCH.
	f.editOnce = func() { f.edit("C1", map[string]any{"surname": "King"}) }
	c := f.client()
	_, err := c.UpdateContact(ctx, "C1", "ck-C1-1", map[string]any{"jobTitle": "Analyst"})
	var ce *ContactConflictError
	if !errors.As(err, &ce) || ce.Server.Surname != "King" {
		t.Fatalf("412 conflict %v", err)
	}
	if ps := f.requests("PATCH", "/me/contacts/C1"); len(ps) != 1 || ps[0].IfMatch != `W/"ck-C1-1"` {
		t.Fatalf("patch %+v", ps)
	}
	f.editOnce = func() { f.edit("C1", map[string]any{"surname": "Byron"}) }
	if err := c.DeleteContact(ctx, "C1", "ck-C1-2"); !errors.As(err, &ce) || ce.Server.Surname != "Byron" {
		t.Fatalf("delete 412 %v", err)
	}
}

func TestDeleteContactAndGone(t *testing.T) {
	ctx := context.Background()
	f := newFakeContacts(t)
	f.add("default", ada())
	c := f.client()
	if err := c.DeleteContact(ctx, "C1", "ck-C1-1"); err != nil {
		t.Fatal(err)
	}
	if ds := f.requests("DELETE", "/me/contacts/C1"); len(ds) != 1 || ds[0].IfMatch != `W/"ck-C1-1"` {
		t.Fatalf("delete %+v", ds)
	}
	if _, err := c.GetContact(ctx, "C1"); !errors.Is(err, ErrContactGone) {
		t.Fatalf("get gone %v", err)
	}
	if err := c.DeleteContact(ctx, "C1", "ck-C1-1"); !errors.Is(err, ErrContactGone) {
		t.Fatalf("delete gone %v", err)
	}
	if _, err := c.UpdateContact(ctx, "C1", "ck-C1-1", map[string]any{"jobTitle": "x"}); !errors.Is(err, ErrContactGone) {
		t.Fatalf("update gone %v", err)
	}
	// Gone between the check and the write.
	f.add("default", map[string]any{"id": "C5", "displayName": "Eve"})
	f.fail["PATCH /me/contacts/C5"] = fakeFailure{404, "ErrorItemNotFound"}
	if _, err := c.UpdateContact(ctx, "C5", "ck-C5-1", map[string]any{"jobTitle": "x"}); !errors.Is(err, ErrContactGone) {
		t.Fatalf("patch 404 %v", err)
	}
	if _, err := c.GetContact(ctx, "bad id"); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Fatalf("bad id %v", err)
	}
}

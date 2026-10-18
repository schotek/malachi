// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func addCrmAccounts(t *testing.T, s *Store) (mail, crm Account) {
	t.Helper()
	ctx := context.Background()
	mail = Account{ID: "acc", Name: "Mail", Enabled: true, Config: api.AccountConfig{Name: "Mail", Email: "me@example.org"}}
	if err := s.AddAccount(ctx, &mail); err != nil {
		t.Fatal(err)
	}
	crm = Account{ID: "crm", Name: "CRM", Enabled: true, Config: api.AccountConfig{Name: "CRM", Email: "me@example.org",
		Kind: api.AccountEway, Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme/", Username: "me"}}}
	if err := s.AddAccount(ctx, &crm); err != nil {
		t.Fatalf("an eway account with a mailbox's address: %v", err)
	}
	return mail, crm
}

// The realm of an eway account is its service URL's host and path, so it
// may share an address with a mailbox; it needs a service URL.
func TestCrmRealm(t *testing.T) {
	cfg := api.AccountConfig{Kind: api.AccountEway, Eway: &api.EwayConfig{ServiceURL: "https://Hosting.eWay-CRM.com:443/Acme/"}}
	if got := RealmOf(cfg); got != "hosting.eway-crm.com/acme" {
		t.Errorf("realm = %q", got)
	}
	if got := RealmOf(api.AccountConfig{Kind: api.AccountEway}); got != "" {
		t.Errorf("realm without a config = %q", got)
	}
	s := openTestStore(t)
	addCrmAccounts(t, s)
	bad := Account{Name: "x", Config: api.AccountConfig{Name: "x", Email: "x@example.org", Kind: api.AccountEway}}
	if err := s.AddAccount(context.Background(), &bad); err == nil {
		t.Error("an eway account without a service URL was added")
	}
}

// Every copy of a message (same Message-ID) shows as saved, a message
// without one by its id; a paused CRM account's links do not count, and
// the links go with either account.
func TestCrmLinks(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)

	// Empty table: listings carry nothing and cost one EXISTS.
	a := seedMessage(t, s, inbox, 1, "one", time.Now())
	items, _, _, err := s.ListMessages(ctx, "acc", inbox.ID, "", 50, "", "")
	if err != nil || len(items) != 1 || items[0].CrmSaved {
		t.Fatalf("empty table: %+v %v", items, err)
	}
	addCrmAccounts(t, s)
	aCopy := seedMessage(t, s, archive, 2, "one", time.Now()) // same Message-ID
	b := seedMessage(t, s, inbox, 3, "two", time.Now())
	noID := &Message{AccountID: "acc", FolderID: inbox.ID, UID: 4, Subject: "no id", Date: time.Now()}
	if err := s.UpsertMessages(ctx, []*Message{noID}); err != nil {
		t.Fatal(err)
	}
	if k := CrmMessageKey(*noID); k != "id:"+noID.ID {
		t.Fatalf("key of a message without a Message-ID: %q", k)
	}

	now := time.Unix(1_800_000_000, 0).UTC()
	for _, key := range []string{CrmMessageKey(*a), CrmMessageKey(*noID)} {
		if err := s.UpsertCrmLink(ctx, CrmLink{CrmAccountID: "crm", MailAccountID: "acc", MsgKey: key,
			ItemGUID: "11111111-2222-3333-4444-555555555555", SavedAt: now, CheckedAt: now,
			Relations: []CrmRelation{{Kind: api.CrmContact, GUID: "g", Name: "Jan"}}}); err != nil {
			t.Fatal(err)
		}
	}
	saved := func(f Folder) map[string]bool {
		t.Helper()
		items, _, _, err := s.ListMessages(ctx, "acc", f.ID, "", 50, "", "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range items {
			out[m.ID] = m.CrmSaved
		}
		return out
	}
	// A link naming an account of another kind does not count.
	if err := s.UpsertCrmLink(ctx, CrmLink{CrmAccountID: "acc", MailAccountID: "acc", MsgKey: CrmMessageKey(*b),
		ItemGUID: "g", SavedAt: now, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got := saved(inbox); !got[a.ID] || got[b.ID] || !got[noID.ID] {
		t.Errorf("inbox saved = %v", got)
	}
	if err := s.DeleteCrmLink(ctx, "acc", "acc", CrmMessageKey(*b)); err != nil {
		t.Fatal(err)
	}
	if got := saved(archive); !got[aCopy.ID] {
		t.Errorf("the archived copy is not saved: %v", got)
	}
	if m, err := s.GetMessage(ctx, "acc", aCopy.ID); err != nil || !m.CrmSaved {
		t.Errorf("GetMessage: %+v %v", m.CrmSaved, err)
	}
	links, err := s.CrmLinksFor(ctx, "acc", []string{CrmMessageKey(*a), CrmMessageKey(*b)})
	if err != nil || len(links) != 1 || len(links[CrmMessageKey(*a)]) != 1 {
		t.Fatalf("links = %+v %v", links, err)
	}
	if l := links[CrmMessageKey(*a)][0]; !l.SavedAt.Equal(now) || len(l.Relations) != 1 || l.Relations[0].Name != "Jan" {
		t.Errorf("link = %+v", l)
	}
	folders, err := s.CrmFoldersOf(ctx, "acc", []string{CrmMessageKey(*a)})
	if err != nil || len(folders) != 2 {
		t.Errorf("folders = %v %v", folders, err)
	}

	later := now.Add(time.Hour)
	if err := s.MarkCrmChecked(ctx, "crm", "acc", CrmMessageKey(*a), later); err != nil {
		t.Fatal(err)
	}
	links, _ = s.CrmLinksFor(ctx, "acc", []string{CrmMessageKey(*a)})
	if !links[CrmMessageKey(*a)][0].CheckedAt.Equal(later) {
		t.Errorf("checked = %v", links[CrmMessageKey(*a)][0].CheckedAt)
	}

	if err := s.SetAccountEnabled(ctx, "crm", false); err != nil {
		t.Fatal(err)
	}
	if got := saved(inbox); got[a.ID] {
		t.Error("a paused CRM account's link shows")
	}
	if err := s.SetAccountEnabled(ctx, "crm", true); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteCrmLink(ctx, "crm", "acc", CrmMessageKey(*noID)); err != nil {
		t.Fatal(err)
	}
	if got := saved(inbox); got[noID.ID] || !got[a.ID] {
		t.Errorf("after delete: %v", got)
	}
	if err := s.DeleteAccount(ctx, "crm", false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM crm_links`); n != 0 {
		t.Errorf("%d links left after the CRM account went", n)
	}
}

// The mail account's removal takes its links too.
func TestCrmLinksGoWithTheMailAccount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	addCrmAccounts(t, s)
	if err := s.UpsertCrmLink(ctx, CrmLink{CrmAccountID: "crm", MailAccountID: "acc", MsgKey: "x@y", ItemGUID: "g",
		SavedAt: time.Now(), CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, "acc", true); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM crm_links`); n != 0 {
		t.Errorf("%d links left after the mail account went", n)
	}
}

// A followed conversation (D22): the template is the most recently saved
// member, the members to save are those stored after the first save in
// the folders that count, one per Message-ID, not saved yet; the sweep
// finds the thread.
func TestCrmFollow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	addCrmAccounts(t, s)
	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	sent := seedFolder(t, s, "acc", "Sent", api.RoleSent)
	archive := seedFolder(t, s, "acc", "Archive", api.RoleArchive)
	junk := seedFolder(t, s, "acc", "Junk", api.RoleJunk)
	trash := seedFolder(t, s, "acc", "Trash", api.RoleTrash)
	drafts := seedFolder(t, s, "acc", "Drafts", api.RoleDrafts)

	first := linked(t, s, inbox, 1, "a", "")
	older := linked(t, s, inbox, 2, "older", "a", "a") // stored before the save
	tid := threadOf(t, s, first)
	if _, ok, err := s.CrmFollowedThreadTemplate(ctx, "acc", tid, "crm"); ok || err != nil {
		t.Fatalf("not followed yet: %v %v", ok, err)
	}
	saved := time.Now().Add(time.Second).Truncate(time.Second)
	for i, key := range []string{"a"} {
		if err := s.UpsertCrmLink(ctx, CrmLink{CrmAccountID: "crm", MailAccountID: "acc", MsgKey: key, ItemGUID: "g" + string(rune('0'+i)),
			SavedAt: saved, CheckedAt: saved, Relations: []CrmRelation{{Kind: api.CrmDeal, GUID: "d", Superior: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(1100 * time.Millisecond) // the members below are stored after the save
	reply := linked(t, s, sent, 3, "r", "a", "a")
	replyCopy := linked(t, s, archive, 4, "r", "a", "a")
	answer := linked(t, s, inbox, 5, "b", "r", "a", "r")
	for i, f := range []Folder{junk, trash, drafts} {
		linked(t, s, f, uint32(10+i), "x"+string(rune('0'+i)), "a", "a")
	}
	late := seedThread(t, s, inbox, Message{UID: 20, RFCMessageID: "late", InReplyTo: "a", References: []string{"a"},
		InternalDate: saved.Add(-48 * time.Hour)}) // a late copy of an old message
	news := linked(t, s, inbox, 21, "news", "a", "a")       // a newsletter that claims the thread
	pending := linked(t, s, inbox, 22, "pending", "a", "a") // not classified yet
	verdicts := []BulkVerdict{{ID: news.ID, Bulk: "newsletter"}}
	for _, m := range []*Message{first, older, reply, replyCopy, answer, late} {
		verdicts = append(verdicts, BulkVerdict{ID: m.ID, Bulk: "none"})
	}
	if err := s.SetBulkBatch(ctx, verdicts); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Message{reply, replyCopy, answer, late, older, news, pending} {
		if threadOf(t, s, m) != tid {
			t.Fatalf("%s not in the thread", m.RFCMessageID)
		}
	}
	tpl, ok, err := s.CrmFollowedThreadTemplate(ctx, "acc", tid, "crm")
	if err != nil || !ok || tpl.Link.MsgKey != "a" || !tpl.FirstSaved.Equal(saved) || len(tpl.Link.Relations) != 1 {
		t.Fatalf("template = %+v %v %v", tpl, ok, err)
	}
	ids, err := s.CrmUnsavedMembers(ctx, "acc", tid, "crm", tpl.FirstSaved, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != reply.ID && ids[0] != replyCopy.ID || ids[1] != answer.ID {
		t.Fatalf("members = %v (reply %s/%s, answer %s)", ids, reply.ID, replyCopy.ID, answer.ID)
	}
	if ok, err := s.CrmFollowable(ctx, "acc", answer.ID, "crm", tpl.FirstSaved); !ok || err != nil {
		t.Errorf("answer followable: %v %v", ok, err)
	}
	for _, m := range []*Message{older, late, first, news, pending} {
		if ok, _ := s.CrmFollowable(ctx, "acc", m.ID, "crm", tpl.FirstSaved); ok {
			t.Errorf("%s followable", m.RFCMessageID)
		}
	}
	// Classified as personal mail, the pending one counts.
	if err := s.SetBulkBatch(ctx, []BulkVerdict{{ID: pending.ID, Bulk: "none"}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.CrmFollowable(ctx, "acc", pending.ID, "crm", tpl.FirstSaved); !ok {
		t.Error("pending member not followable once classified")
	}
	if ids, _ := s.CrmUnsavedMembers(ctx, "acc", tid, "crm", tpl.FirstSaved, 50); len(ids) != 3 {
		t.Errorf("members after the classification = %v", ids)
	}
	// Another CRM account does not follow it; an auto save is recorded.
	if _, ok, _ := s.CrmFollowedThreadTemplate(ctx, "acc", tid, "other"); ok {
		t.Error("followed by another CRM account")
	}
	if err := s.UpsertCrmLink(ctx, CrmLink{CrmAccountID: "crm", MailAccountID: "acc", MsgKey: "b", ItemGUID: "g9",
		SavedAt: saved.Add(time.Hour), CheckedAt: saved, Auto: true}); err != nil {
		t.Fatal(err)
	}
	links, _ := s.CrmLinksFor(ctx, "acc", []string{"b"})
	if !links["b"][0].Auto {
		t.Error("auto not stored")
	}
	if tpl, _, _ := s.CrmFollowedThreadTemplate(ctx, "acc", tid, "crm"); tpl.Link.MsgKey != "b" || !tpl.FirstSaved.Equal(saved) {
		t.Errorf("template after a later save = %+v", tpl)
	}
	if by, err := s.CrmFollowedBy(ctx, "acc", answer.ID, []string{"crm", "other"}); err != nil || len(by) != 1 || by[0] != "crm" {
		t.Errorf("followed by = %v %v", by, err)
	}
	if by, _ := s.CrmFollowedBy(ctx, "acc", answer.ID, nil); len(by) != 0 {
		t.Errorf("followed by none = %v", by)
	}
	threads, err := s.CrmFollowedThreads(ctx, "crm", saved.Add(-time.Hour), 10)
	if err != nil || len(threads) != 1 || threads[0].ThreadID != tid || threads[0].MailAccountID != "acc" {
		t.Errorf("followed threads = %+v %v", threads, err)
	}
	if threads, _ := s.CrmFollowedThreads(ctx, "crm", saved.Add(2*time.Hour), 10); len(threads) != 0 {
		t.Errorf("old threads listed: %+v", threads)
	}
}

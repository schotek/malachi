// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type followHarness struct {
	m       *mailbox
	f       *ewaytest.Server
	crm     api.AccountID
	contact string
	deal    string
	folders map[string]store.Folder
	saved   *api.CrmRecord
	bulk    string // the classification member gives new rows ("" = pending)
}

// newFollow saves m0 with a contact, a superior deal and privacy into an
// eway account following conversations, and waits a second so that what
// is stored next counts as later.
func newFollow(t *testing.T) *followHarness {
	t.Helper()
	m := seedMailbox(t)
	f := crmFake(t)
	m.b.EwayHTTP = f.Client()
	cfg := ewayConfig(f)
	cfg.Eway.FollowThreads = true
	res, err := m.b.Accounts().Add(context.Background(), api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: f.Password}})
	if err != nil {
		t.Fatal(err)
	}
	h := &followHarness{m: m, f: f, crm: res.AccountID, bulk: "none"}
	h.contact = f.Add("Contacts", ewaytest.Item{FileAs: "Alice", Email1: "alice@example.invalid"})
	h.deal = f.Add("Leads", ewaytest.Item{FileAs: "Deal"})
	h.folders = seedFolders(t, m.b, string(m.acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Container", Name: "Container", Path: "Container", Subscribed: true, Selectable: false},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Subscribed: true, Selectable: true},
	})
	sr, err := m.b.Crm().Save(context.Background(), api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1],
		Link: []api.CrmItemRef{{Kind: api.CrmContact, GUID: h.contact}}, Superior: &api.CrmItemRef{Kind: api.CrmDeal, GUID: h.deal}, Private: true})
	if err != nil || sr.Results[0].Record == nil {
		t.Fatalf("first save: %+v %v", sr, err)
	}
	h.saved = sr.Results[0].Record
	time.Sleep(1100 * time.Millisecond)
	return h
}

// member stores a reply to m0 in the folder (whole unless noRaw), from the
// sender at the date.
func (h *followHarness) member(t *testing.T, folder string, uid uint32, rfc, sender string, date time.Time, noRaw bool) store.Message {
	t.Helper()
	b := h.m.b
	row := &store.Message{AccountID: string(h.m.acc), FolderID: h.folders[folder].ID, UID: uid, Subject: "Re: first", Date: date,
		InternalDate: time.Now(), From: []api.Address{{Address: sender}}, To: []api.Address{{Address: "me@example.invalid"}},
		RFCMessageID: rfc, InReplyTo: "<one@example.invalid>", References: []string{"<one@example.invalid>"}, Size: 50, Bulk: h.bulk}
	if err := b.store.UpsertMessages(context.Background(), []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if !noRaw {
		h.m.rawOf(t, api.MessageID(row.ID), "From: "+sender+"\r\nTo: me@example.invalid\r\nSubject: Re: first\r\n\r\nreply\r\n")
	}
	got, err := b.store.GetMessage(context.Background(), string(h.m.acc), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// stored runs the Stored hook's part for the message and waits for the
// worker.
func (h *followHarness) stored(t *testing.T, m store.Message) {
	t.Helper()
	b := h.m.b
	ch := make(chan struct{}, 8)
	b.crm.follow.mu.Lock()
	b.crm.follow.idle = ch
	b.crm.follow.mu.Unlock()
	b.noteCrmFollow(context.Background(), m.AccountID, m.ID)
	b.crm.follow.mu.Lock()
	q := b.crm.follow.queues[string(h.crm)]
	running := q != nil && q.running
	b.crm.follow.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not finish")
	}
	for i := 0; i < 100; i++ { // the worker's last signal is its end
		b.crm.follow.mu.Lock()
		running = q.running
		b.crm.follow.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *followHarness) link(t *testing.T, m store.Message) *store.CrmLink {
	t.Helper()
	key := store.CrmMessageKey(m)
	links, err := h.m.b.store.CrmLinksFor(context.Background(), m.AccountID, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	return localLink(links[key], string(h.crm))
}

func TestCrmFollowSavesLaterMembers(t *testing.T) {
	h := newFollow(t)
	rec := &noteRecorder{}
	h.m.b.SetNotifier(rec)
	now := time.Now().UTC().Truncate(time.Minute)
	reply := h.member(t, "Sent", 30, "<reply@example.invalid>", "me@example.invalid", now, false)
	h.stored(t, reply)
	l := h.link(t, reply)
	if l == nil || !l.Auto || !l.Private || len(l.Relations) != 2 {
		t.Fatalf("reply link = %+v", l)
	}
	if len(h.f.Emails()) != 2 {
		t.Fatalf("items = %d", len(h.f.Emails()))
	}
	types := map[string]string{}
	for _, r := range h.f.Relations(l.ItemGUID) {
		other := r.GUID2
		if strings.EqualFold(other, l.ItemGUID) {
			other = r.GUID1
		}
		types[strings.ToLower(other)] = r.Type
	}
	if types[strings.ToLower(h.contact)] != "GENERAL" || types[strings.ToLower(h.deal)] != "OUTLOOKPROJECT" {
		t.Errorf("relations = %v", types)
	}
	if got := waitChanged(t, rec, 1); len(got) == 0 {
		t.Error("no messagesChanged")
	}
	g, err := h.m.b.Messages().Get(context.Background(), api.MessageGetParams{AccountID: h.m.acc, MessageID: api.MessageID(reply.ID)})
	if err != nil || len(g.Message.Crm) != 1 || !g.Message.Crm[0].Auto || !g.Message.CrmSaved {
		t.Errorf("message.get = %+v %v", g.Message.Crm, err)
	}

	// Junk and Trash members, and one not whole, are not saved.
	for i, folder := range []string{"Junk", "Trash"} {
		m := h.member(t, folder, uint32(40+i), "<x"+folder+"@example.invalid>", "bob@example.invalid", now, false)
		h.stored(t, m)
		if h.link(t, m) != nil {
			t.Errorf("%s member saved", folder)
		}
	}
	partial := h.member(t, "INBOX", 50, "<partial@example.invalid>", "bob@example.invalid", now, true)
	h.stored(t, partial)
	if h.link(t, partial) != nil || len(h.f.Emails()) != 2 {
		t.Error("a member not whole was saved")
	}
	// A message of another conversation is not followed.
	h.stored(t, h.m.row(t, string(h.m.msgs[1])))
	if len(h.f.Emails()) != 2 {
		t.Error("another conversation was followed")
	}
}

// The other side saved its reply from Outlook: it is linked, not
// duplicated.
func TestCrmFollowLinksWhatOutlookSaved(t *testing.T) {
	h := newFollow(t)
	date := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
	theirs := h.f.AddEmail(ewaytest.Email{MessageID: "<answer@example.invalid>", Sender: "alice@example.invalid",
		SentOn: date.Format(time.RFC3339), FileAs: "Re: first"})
	answer := h.member(t, "INBOX", 31, "<answer@example.invalid>", "alice@example.invalid", date, false)
	h.stored(t, answer)
	l := h.link(t, answer)
	if l == nil || l.ItemGUID != strings.ToLower(theirs) || len(h.f.Emails()) != 2 {
		t.Fatalf("link = %+v, %d items", l, len(h.f.Emails()))
	}
	if len(h.f.Relations(theirs)) != 2 {
		t.Errorf("relations of the Outlook item = %+v", h.f.Relations(theirs))
	}
}

// Switched off, nothing happens, by the hook or the sweep; switched on,
// the sweep catches up what was missed.
func TestCrmFollowSwitchAndSweep(t *testing.T) {
	h := newFollow(t)
	ctx := context.Background()
	b := h.m.b
	acc, err := b.store.GetAccount(ctx, string(h.crm))
	if err != nil {
		t.Fatal(err)
	}
	off := acc.Config
	off.Eway.FollowThreads = false
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: h.crm, Config: off}); err != nil {
		t.Fatal(err)
	}
	missed := h.member(t, "INBOX", 32, "<missed@example.invalid>", "bob@example.invalid", time.Now(), false)
	h.stored(t, missed)
	b.crmFollowSweep(ctx)
	if h.link(t, missed) != nil || len(h.f.Emails()) != 1 {
		t.Fatal("saved while switched off")
	}
	on := off
	on.Eway = &api.EwayConfig{ServiceURL: off.Eway.ServiceURL, Username: off.Eway.Username, FollowThreads: true}
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: h.crm, Config: on}); err != nil {
		t.Fatal(err)
	}
	b.crmFollowSweep(ctx)
	if l := h.link(t, missed); l == nil || !l.Auto {
		t.Fatalf("the sweep did not catch up: %+v", l)
	}
	n := len(h.f.Emails())
	b.crmFollowSweep(ctx)
	if len(h.f.Emails()) != n {
		t.Error("the sweep saved twice")
	}
}

// Another mail account's message in a thread of the same Message-IDs is
// not followed: the conversation is the mail account's.
func TestCrmFollowStaysInTheMailAccount(t *testing.T) {
	h := newFollow(t)
	ctx := context.Background()
	b := h.m.b
	other := seedAccount(t, b, "other@example.invalid")
	folders := seedFolders(t, b, other, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
	})
	row := &store.Message{AccountID: other, FolderID: folders["INBOX"].ID, UID: 1, Subject: "Re: first", Date: time.Now(),
		From: []api.Address{{Address: "bob@example.invalid"}}, RFCMessageID: "<o@example.invalid>",
		InReplyTo: "<one@example.invalid>", References: []string{"<one@example.invalid>"}, Size: 10}
	if err := b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.WriteMessageRaw(ctx, other, row.ID, strings.NewReader("From: bob@example.invalid\r\n\r\nx\r\n"), 1<<20); err != nil {
		t.Fatal(err)
	}
	b.noteCrmFollow(ctx, other, row.ID)
	b.crmFollowSweep(ctx)
	time.Sleep(100 * time.Millisecond)
	if len(h.f.Emails()) != 1 {
		t.Errorf("followed into another mail account: %d items", len(h.f.Emails()))
	}
}

// A newsletter that claims to be part of a saved conversation is never
// saved by itself; a member still waiting for the classification is left
// to the sweep, which saves it once it is classified as personal mail.
func TestCrmFollowSkipsBulk(t *testing.T) {
	h := newFollow(t)
	ctx := context.Background()
	b := h.m.b
	h.bulk = "newsletter"
	news := h.member(t, "INBOX", 60, "<news@example.invalid>", "news@shop.example", time.Now(), false)
	h.bulk = ""
	pending := h.member(t, "INBOX", 61, "<pending@example.invalid>", "bob@example.invalid", time.Now(), false)
	h.stored(t, news)
	h.stored(t, pending)
	b.crmFollowSweep(ctx)
	if h.link(t, news) != nil || h.link(t, pending) != nil || len(h.f.Emails()) != 1 {
		t.Fatalf("bulk or pending saved: %d items", len(h.f.Emails()))
	}
	if err := b.store.SetBulkBatch(ctx, []store.BulkVerdict{{ID: pending.ID, Bulk: "none"}}); err != nil {
		t.Fatal(err)
	}
	b.crmFollowSweep(ctx)
	if l := h.link(t, pending); l == nil || !l.Auto {
		t.Fatalf("pending not saved after classification: %+v", l)
	}
	if h.link(t, news) != nil {
		t.Error("the newsletter was saved")
	}
}

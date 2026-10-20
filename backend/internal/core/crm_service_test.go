// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func ewayConfig(f *ewaytest.Server) api.AccountConfig {
	return api.AccountConfig{Name: "acme", Kind: api.AccountEway,
		Eway: &api.EwayConfig{ServiceURL: f.ServiceURL() + "/API.svc", Username: f.Username}}
}

// addEway adds an eway account over the fake to m's backend.
func addEway(t *testing.T, b *Backend, f *ewaytest.Server) api.AccountID {
	t.Helper()
	b.EwayHTTP = f.Client()
	res, err := b.Accounts().Add(context.Background(), api.AccountAddParams{Config: ewayConfig(f), Credentials: api.Credentials{Password: f.Password}})
	if err != nil {
		t.Fatal(err)
	}
	return res.AccountID
}

func crmFake(t *testing.T) *ewaytest.Server {
	t.Helper()
	f := ewaytest.New()
	t.Cleanup(f.Close)
	return f
}

func TestEwayAccount(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	b.EwayHTTP = f.Client()
	acc := b.Accounts()

	d, err := acc.DetectEway(ctx, api.AccountDetectEwayParams{ServiceURL: f.ServiceURL() + "/API.svc/"})
	if err != nil || d.ServiceURL != f.ServiceURL() || d.MicrosoftSignIn {
		t.Fatalf("detect = %+v %v", d, err)
	}
	if _, err := acc.DetectEway(ctx, api.AccountDetectEwayParams{ServiceURL: "http://crm.example.invalid"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("http: %v", err)
	}
	f.AzureAuth = true
	if d, err := acc.DetectEway(ctx, api.AccountDetectEwayParams{ServiceURL: f.ServiceURL()}); err != nil || !d.MicrosoftSignIn {
		t.Errorf("azure detect = %+v %v", d, err)
	}
	_, err = acc.Add(ctx, api.AccountAddParams{Config: ewayConfig(f), Credentials: api.Credentials{Password: f.Password}})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeCrmSignInUnsupported || ae.Data.(api.CrmSignInUnsupportedData).Reason != api.CrmSignInMicrosoft {
		t.Fatalf("azure add: %v", err)
	}
	f.AzureAuth = false
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: ewayConfig(f), Credentials: api.Credentials{Password: "wrong"}}); errCode(t, err) != api.CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: ewayConfig(f)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no password: %v", err)
	}
	bad := ewayConfig(f)
	bad.IMAP = validConfig().IMAP
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: bad, Credentials: api.Credentials{Password: f.Password}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("imap block: %v", err)
	}

	// The fake's user has the mail account's address? No: its own, which
	// may equal a mailbox's (another realm).
	cfg := ewayConfig(f)
	cfg.Email = "me@example.invalid"
	res, err := acc.Add(ctx, api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: f.Password}})
	if err != nil {
		t.Fatalf("add with a mailbox's address: %v", err)
	}
	id := res.AccountID
	list, _ := acc.List(ctx, api.AccountListParams{})
	var got api.Account
	for _, a := range list.Accounts {
		if a.ID == id {
			got = a
		}
	}
	if !slices.Equal(got.Capabilities, []api.AccountCapability{api.CapabilityCrm}) || got.Config.Eway.ServiceURL != f.ServiceURL() ||
		!strings.Contains(got.Config.Eway.UserGUID, "-") {
		t.Fatalf("account = %+v", got)
	}
	// No mail: no folders, message methods invalidArgument.
	if fl, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id}); err != nil || len(fl.Folders) != 0 {
		t.Errorf("folders = %+v %v", fl, err)
	}
	if _, err := b.Messages().List(ctx, api.MessageListParams{AccountID: id}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("message.list: %v", err)
	}
	// account.test with the stored password; a new instance needs its own.
	tr, err := acc.Test(ctx, api.AccountTestParams{AccountID: id, Config: got.Config})
	if err != nil || tr.Eway == nil || !tr.Eway.OK {
		t.Fatalf("test = %+v %v", tr, err)
	}
	moved := got.Config
	moved.Eway = &api.EwayConfig{ServiceURL: "https://other.example.invalid/x", Username: f.Username}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: moved}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("update to another instance without a password: %v", err)
	}
	if _, err := acc.Test(ctx, api.AccountTestParams{AccountID: id, Config: moved}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("test of another instance with the stored password: %v", err)
	}
	renamed := got.Config
	renamed.Name = "Renamed"
	renamed.Eway = &api.EwayConfig{ServiceURL: got.Config.Eway.ServiceURL, Username: f.Username}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: id, Config: renamed}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if a, _ := b.store.GetAccount(ctx, string(id)); a.Config.Eway.UserGUID != got.Config.Eway.UserGUID {
		t.Errorf("the user GUID was lost: %+v", a.Config.Eway)
	}
}

// The whole flow: save with a contact and a superior deal, the record in
// listings and message.get, save again (found), lookup, search, remove.
func TestCrmSaveLookupRemove(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	crm := addEway(t, b, f)
	rec := &noteRecorder{}
	b.SetNotifier(rec)
	svc := b.Crm()

	alice := f.Add("Contacts", ewaytest.Item{FileAs: "Alice Example", Email1: "alice@example.invalid"})
	deal := f.Add("Leads", ewaytest.Item{FileAs: "Deal", Changed: time.Now()})
	f.Relate(alice, "Contacts", deal, "Leads", "GENERAL")
	f.Add("Contacts", ewaytest.Item{FileAs: "Me", Email1: "me@example.invalid"}) // the user's own: never suggested

	look, err := svc.Lookup(ctx, api.CrmLookupParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if look.AccountID != crm || len(look.Saved) != 1 || look.Saved[0].Record != nil || len(look.Suggestions) != 2 {
		t.Fatalf("lookup = %+v", look)
	}
	if s := look.Suggestions[0]; s.GUID != alice || s.Reason != api.CrmReasonAddress || !strings.HasPrefix(s.URL, "https://open.eway-crm.com/?ws=") {
		t.Errorf("contact suggestion %+v", s)
	}
	if s := look.Suggestions[1]; s.GUID != deal || !s.Superior {
		t.Errorf("deal suggestion %+v", s)
	}

	res, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1],
		Link: []api.CrmItemRef{{Kind: api.CrmContact, GUID: alice}}, Superior: &api.CrmItemRef{Kind: api.CrmDeal, GUID: deal}})
	if err != nil {
		t.Fatal(err)
	}
	out := res.Results[0]
	if out.Error != nil || !out.Created || out.Record == nil || len(out.Record.Items) != 2 || out.Record.AccountID != crm {
		t.Fatalf("save = %+v", out)
	}
	for _, it := range out.Record.Items {
		if (it.GUID == deal) != it.Superior || it.Name == "" {
			t.Errorf("item %+v", it)
		}
	}
	es := f.Emails()
	if len(es) != 1 || es[0].MessageID != "<one@example.invalid>" || es[0].Sender != "alice@example.invalid" || es[0].Note == "" {
		t.Fatalf("saved = %+v", es)
	}
	if got := waitChanged(t, rec, 1); len(got) == 0 || got[0] != string(m.acc)+":"+string(m.inbox) {
		t.Errorf("messagesChanged = %v", got)
	}
	l, err := b.Messages().List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range l.Messages {
		if s.CrmSaved != (s.ID == m.msgs[0]) {
			t.Errorf("crmSaved of %s = %v", s.ID, s.CrmSaved)
		}
	}
	g, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]})
	if err != nil || len(g.Message.Crm) != 1 || g.Message.Crm[0].ItemGUID != out.Record.ItemGUID || !g.Message.CrmSaved {
		t.Fatalf("message.get crm = %+v %v", g.Message.Crm, err)
	}

	// Saved again: found by Message-ID, nothing new; the record names it.
	again, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]})
	if err != nil || again.Results[0].Created || again.Results[0].Record.ItemGUID != out.Record.ItemGUID || len(f.Emails()) != 1 {
		t.Fatalf("again = %+v %v", again, err)
	}
	look, err = svc.Lookup(ctx, api.CrmLookupParams{AccountID: crm, MessageAccountID: m.acc, MessageIDs: m.msgs[:1]})
	if err != nil || look.Saved[0].Record == nil || len(look.Saved[0].Record.Items) != 2 {
		t.Fatalf("lookup of the saved = %+v %v", look, err)
	}
	// Edit links: unlink the contact.
	if _, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1],
		Unlink: []api.CrmItemRef{{Kind: api.CrmContact, GUID: alice}}}); err != nil {
		t.Fatal(err)
	}
	if rels := f.Relations(out.Record.ItemGUID); len(rels) != 1 || rels[0].Type != "OUTLOOKPROJECT" {
		t.Errorf("relations after unlink = %+v", rels)
	}

	sr, err := svc.Search(ctx, api.CrmSearchParams{Query: "alice ex"})
	if err != nil || len(sr.Items) != 1 || sr.Items[0].GUID != alice || sr.Items[0].Reason != api.CrmReasonSearch {
		t.Fatalf("search = %+v %v", sr, err)
	}
	if _, err := svc.Search(ctx, api.CrmSearchParams{Query: "x"}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("short query: %v", err)
	}

	rec.takeChanged()
	if _, err := svc.Remove(ctx, api.CrmRemoveParams{MessageAccountID: m.acc, MessageID: m.msgs[0]}); err != nil {
		t.Fatal(err)
	}
	if len(f.Emails()) != 0 {
		t.Error("the item is still in the CRM")
	}
	if got := waitChanged(t, rec, 1); len(got) == 0 {
		t.Error("no messagesChanged after remove")
	}
	if g, _ := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]}); g.Message.CrmSaved || len(g.Message.Crm) != 0 {
		t.Errorf("still saved: %+v", g.Message.Crm)
	}
	if _, err := svc.Remove(ctx, api.CrmRemoveParams{MessageAccountID: m.acc, MessageID: m.msgs[0]}); errCode(t, err) != api.CodeCrmItemNotFound {
		t.Errorf("remove again: %v", err)
	}
}

// A message not whole on this device is partNotDownloaded with its id;
// after the download (here: its file written) the save goes through.
func TestCrmSaveNeedsTheWholeMessage(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	f := crmFake(t)
	addEway(t, m.b, f)
	_, err := m.b.Crm().Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], m.msgs[2]}})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodePartNotDownloaded {
		t.Fatalf("not whole: %v", err)
	}
	if d, ok := ae.Data.(api.MessagesNotDownloadedData); !ok || !slices.Equal(d.MessageIDs, []api.MessageID{m.msgs[2]}) {
		t.Fatalf("data = %#v", ae.Data)
	}
	if len(f.Emails()) != 0 || f.Binaries() != 0 {
		t.Fatal("something went out before the check")
	}
	m.rawOf(t, m.msgs[2], secondRaw)
	res, err := m.b.Crm().Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], m.msgs[2]}})
	if err != nil || res.Results[0].Error != nil || res.Results[1].Error != nil || len(f.Emails()) != 2 {
		t.Fatalf("after the download: %+v %v", res, err)
	}
}

func TestCrmRefusals(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	svc := b.Crm()
	if _, err := svc.Lookup(ctx, api.CrmLookupParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("no eway account: %v", err)
	}
	f := crmFake(t)
	crm := addEway(t, b, f)
	drafts := m.draftsFolder(t)
	draft := m.seedIn(t, drafts, 50, "alternative.eml", true)
	m.rawOf(t, api.MessageID(draft.ID), secondRaw)
	many := make([]api.MessageID, api.MaxCrmMessages+1)
	for i := range many {
		many[i] = api.MessageID("m_" + strings.Repeat("x", i+1))
	}
	cases := []struct {
		name string
		p    api.CrmSaveParams
		code api.ErrorCode
	}{
		{"drafts folder", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: []api.MessageID{api.MessageID(draft.ID)}}, api.CodeInvalidArgument},
		{"a crm account's messages", api.CrmSaveParams{MessageAccountID: crm, MessageIDs: m.msgs[:1]}, api.CodeInvalidArgument},
		{"no messages", api.CrmSaveParams{MessageAccountID: m.acc}, api.CodeInvalidArgument},
		{"too many", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: many}, api.CodeInvalidArgument},
		{"twice", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], m.msgs[0]}}, api.CodeInvalidArgument},
		{"unknown", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: []api.MessageID{"m_nope"}}, api.CodeMessageNotFound},
		{"bad guid", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1], Link: []api.CrmItemRef{{Kind: api.CrmContact, GUID: "x"}}}, api.CodeInvalidArgument},
		{"contact as superior", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1], Superior: &api.CrmItemRef{Kind: api.CrmContact, GUID: ewaytest.NewGUID()}}, api.CodeInvalidArgument},
		{"unlink of several", api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:2], Unlink: []api.CrmItemRef{{Kind: api.CrmContact, GUID: ewaytest.NewGUID()}}}, api.CodeInvalidArgument},
		{"a mail account as the crm", api.CrmSaveParams{AccountID: m.acc, MessageAccountID: m.acc, MessageIDs: m.msgs[:1]}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		if _, err := svc.Save(ctx, c.p); errCode(t, err) != c.code {
			t.Errorf("%s: %v, want %v", c.name, err, c.code)
		}
	}
	// An unknown item to link is that message's error.
	// An unknown item to link fails the call before anything goes out.
	if _, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1],
		Link: []api.CrmItemRef{{Kind: api.CrmCompany, GUID: ewaytest.NewGUID()}}}); errCode(t, err) != api.CodeCrmItemNotFound {
		t.Errorf("unknown item: %v", err)
	}
	if _, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1],
		Superior: &api.CrmItemRef{Kind: api.CrmProject, GUID: ewaytest.NewGUID()}}); errCode(t, err) != api.CodeCrmItemNotFound {
		t.Errorf("unknown superior: %v", err)
	}
	if len(f.Emails()) != 0 || f.Binaries() != 0 {
		t.Errorf("an unknown item left %d items, %d uploads", len(f.Emails()), f.Binaries())
	}
	// A save of the same message running: conflict.
	key := crmBusyKey(string(crm), string(m.acc), store.CrmMessageKey(m.row(t, string(m.msgs[0]))))
	b.crm.claim([]string{key})
	if _, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]}); errCode(t, err) != api.CodeConflict {
		t.Errorf("running: %v", err)
	}
	b.crm.release([]string{key})
	// A paused account answers unavailable; a refused sign-in is the call's.
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: crm, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, api.CrmSearchParams{AccountID: crm, Query: "ab"}); errCode(t, err) != api.CodeUnavailable {
		t.Errorf("paused: %v", err)
	}
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: crm, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	f.Password = "changed"
	f.ExpireSessions()
	if _, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]}); errCode(t, err) != api.CodeAuthFailed {
		t.Errorf("refused sign-in: %v", err)
	}
}

// An old record is checked again when message.get returns it; the item
// gone from the CRM takes the record and tells the clients.
func TestCrmStaleRecheck(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	crm := addEway(t, b, f)
	res, err := b.Crm().Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]})
	if err != nil || res.Results[0].Record == nil {
		t.Fatalf("save: %+v %v", res, err)
	}
	key := store.CrmMessageKey(m.row(t, string(m.msgs[0])))
	links, _ := b.store.CrmLinksFor(ctx, string(m.acc), []string{key})
	l := links[key][0]
	l.CheckedAt = time.Now().Add(-48 * time.Hour)
	if err := b.store.UpsertCrmLink(ctx, l); err != nil {
		t.Fatal(err)
	}
	f.DropEmail(l.ItemGUID)
	rec := &noteRecorder{}
	b.SetNotifier(rec)
	g, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]})
	if err != nil || len(g.Message.Crm) != 1 || g.Message.Crm[0].AccountID != crm {
		t.Fatalf("cached record: %+v %v", g.Message.Crm, err)
	}
	if got := waitChanged(t, rec, 1); len(got) == 0 {
		t.Fatal("no messagesChanged after the stale check")
	}
	if links, _ := b.store.CrmLinksFor(ctx, string(m.acc), []string{key}); len(links[key]) != 0 {
		t.Errorf("record kept: %+v", links)
	}
}

// A mail that claims the Message-ID of someone else's CRM item is not
// "saved", cannot remove that item, and saving it creates a new item
// instead of linking the user's items to the other one.
func TestCrmForgedMessageID(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	addEway(t, b, f)
	svc := b.Crm()
	// m0 is "<one@example.invalid>" from alice; the CRM has an item with
	// that Message-ID from someone else, at another time.
	theirs := f.AddEmail(ewaytest.Email{MessageID: "<one@example.invalid>", Sender: "ceo@victim.example",
		SentOn: "2026-01-02T03:04:00Z", FileAs: "Board minutes"})
	look, err := svc.Lookup(ctx, api.CrmLookupParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]})
	if err != nil || look.Saved[0].Record != nil {
		t.Fatalf("forged lookup = %+v %v", look, err)
	}
	if g, _ := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]}); g.Message.CrmSaved {
		t.Error("marked saved")
	}
	if _, err := svc.Remove(ctx, api.CrmRemoveParams{MessageAccountID: m.acc, MessageID: m.msgs[0]}); errCode(t, err) != api.CodeCrmItemNotFound {
		t.Errorf("forged remove: %v", err)
	}
	contact := f.Add("Contacts", ewaytest.Item{FileAs: "C"})
	res, err := svc.Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1], Link: []api.CrmItemRef{{Kind: api.CrmContact, GUID: contact}}})
	if err != nil || res.Results[0].Error != nil || !res.Results[0].Created || res.Results[0].Record.ItemGUID == strings.ToLower(theirs) {
		t.Fatalf("forged save = %+v %v", res, err)
	}
	if len(f.Emails()) != 2 || len(f.Relations(theirs)) != 0 {
		t.Errorf("the other item was touched: %d items, relations %+v", len(f.Emails()), f.Relations(theirs))
	}
	// Removing now deletes the user's own item only.
	if _, err := svc.Remove(ctx, api.CrmRemoveParams{MessageAccountID: m.acc, MessageID: m.msgs[0]}); err != nil {
		t.Fatal(err)
	}
	if es := f.Emails(); len(es) != 1 || es[0].GUID != strings.ToLower(theirs) {
		t.Errorf("left = %+v", es)
	}
}

// A failed background check backs off; one beside a running save does
// not run.
func TestCrmRecheckBackoffAndBusy(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	crm := addEway(t, b, f)
	if _, err := b.Crm().Save(ctx, api.CrmSaveParams{MessageAccountID: m.acc, MessageIDs: m.msgs[:1]}); err != nil {
		t.Fatal(err)
	}
	key := store.CrmMessageKey(m.row(t, string(m.msgs[0])))
	stale := func() {
		links, _ := b.store.CrmLinksFor(ctx, string(m.acc), []string{key})
		l := links[key][0]
		l.CheckedAt = time.Now().Add(-48 * time.Hour)
		if err := b.store.UpsertCrmLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	checked := func() time.Time {
		links, _ := b.store.CrmLinksFor(ctx, string(m.acc), []string{key})
		return links[key][0].CheckedAt
	}
	stale()
	busy := []string{crmBusyKey(string(crm), string(m.acc), key)}
	b.crm.claim(busy)
	n := len(f.Calls())
	if _, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if len(f.Calls()) != n {
		t.Errorf("a check ran beside a save: %v", f.Calls()[n:])
	}
	b.crm.release(busy)

	f.Override("GetEmailsByItemGuids", []byte("broken"))
	if _, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc, MessageID: m.msgs[0]}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Since(checked()) > crmStaleAfter && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if age := time.Since(checked()); age > crmStaleAfter || age < crmStaleAfter-crmRecheckBackoff-time.Minute {
		t.Errorf("no backoff: checked %v ago", age)
	}
}

// An eWay-CRM user without an e-mail whose user name is not an address
// (Gate B, 2026-10-17: the owner's "vjanecek" failed with "empty
// address"): the account is added with an empty address, the only kind
// that may have one; a given address must still be one.
func TestEwayAccountWithoutAddress(t *testing.T) {
	m := seedMailbox(t)
	b := m.b
	ctx := context.Background()
	f := crmFake(t)
	f.Username = "vjanecek"
	f.SetUserEmail("")
	b.EwayHTTP = f.Client()
	acc := b.Accounts()

	bad := ewayConfig(f)
	bad.Email = "not an address"
	if _, err := acc.Add(ctx, api.AccountAddParams{Config: bad, Credentials: api.Credentials{Password: f.Password}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("invalid address: %v", err)
	}
	res, err := acc.Add(ctx, api.AccountAddParams{Config: ewayConfig(f), Credentials: api.Credentials{Password: f.Password}})
	if err != nil {
		t.Fatalf("add without an address: %v", err)
	}
	list, _ := acc.List(ctx, api.AccountListParams{})
	for _, a := range list.Accounts {
		if a.ID == res.AccountID {
			if a.Config.Email != "" || a.Config.Eway.Username != "vjanecek" || a.Config.Eway.UserGUID == "" {
				t.Fatalf("account = %+v", a.Config)
			}
			return
		}
	}
	t.Fatal("the account is not listed")
}

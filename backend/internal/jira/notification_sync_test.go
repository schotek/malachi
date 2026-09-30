// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// mailNaming stores, in a mail account of the harness's store, a
// notification mail of the site and links it to the issue with the key,
// as core does when the matcher finds the key (here whatever the matcher
// would say: the syncer decides about its own scope).
func (h *harness) mailNaming(key string) store.Message {
	h.t.Helper()
	ctx := context.Background()
	accounts, err := h.st.ListAccounts(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	var mail store.Account
	for _, a := range accounts {
		if a.Config.Protocol() == api.AccountIMAP {
			mail = a
		}
	}
	if mail.ID == "" {
		mail = store.Account{Name: "Mail", Enabled: true, Config: api.AccountConfig{
			Name: "Mail", Email: "jana.dvorakova@example.invalid", Kind: api.AccountIMAP,
			IMAP: &api.ServerConfig{Host: "imap.example.invalid", Port: 993, Security: api.SecurityTLS, Username: "jana", AuthMethod: api.AuthPassword},
			SMTP: &api.ServerConfig{Host: "smtp.example.invalid", Port: 465, Security: api.SecurityTLS, Username: "jana", AuthMethod: api.AuthPassword},
		}}
		if err := h.st.AddAccount(ctx, &mail); err != nil {
			h.t.Fatal(err)
		}
	}
	folders, _, err := h.st.UpsertFolders(ctx, mail.ID, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	m := &store.Message{AccountID: mail.ID, FolderID: folders[0].ID, RemoteID: "note:" + key,
		From:    []api.Address{{Name: "Petr Svoboda (Jira)", Address: "jira@acme.atlassian.net"}},
		Subject: "[JIRA] (" + key + ") changed", Date: h.clock.now(), InternalDate: h.clock.now()}
	if err := h.st.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.LinkIssueMail(ctx, m.ID, h.acc.ID, key, ""); err != nil {
		h.t.Fatal(err)
	}
	return *m
}

// With OnlyMine, an issue that is nobody's of the user's is synchronised
// once a notification mail names it, and kept from then on.
func TestOnlyMineKeepsIssuesNamedByMail(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		h := newHarness(t, mode, func(c *api.JiraConfig) { c.OnlyMine = true })
		f := h.f
		var named, unnamed, elsewhere, watched *jiratest.Issue
		h.at(h.ago(3*day), func() {
			named = f.AddIssue("ITSD", "Zmínka v komentáři", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			unnamed = f.AddIssue("ITSD", "Cizí požadavek", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			elsewhere = f.AddIssue("MOB", "Jiný prostor", func(is *jiratest.Issue) { is.Reporter = f.Petr })
			watched = f.AddIssue("WEB", "Sledovaný", func(is *jiratest.Issue) { is.Reporter, is.Watching = f.Petr, true })
		})
		h.mustPass()
		for _, is := range []*jiratest.Issue{named, unnamed, elsewhere} {
			if _, ok := h.issue(is.ID); ok {
				t.Fatalf("%s was synchronised", is.Key)
			}
		}

		// A refresh asked for by its key alone brings nothing that is not
		// the user's; the mail's does, within the selected spaces only.
		h.mailNaming(named.Key)
		h.mailNaming(elsewhere.Key)
		h.clock.advance(time.Minute)
		h.mustPass(named.Key, unnamed.Key, elsewhere.Key)
		is, ok := h.issue(named.ID)
		if !ok || !is.ViaMail || is.Key != named.Key {
			t.Fatalf("the issue a mail named: %+v %v", is, ok)
		}
		if _, ok := h.issue(unnamed.ID); ok {
			t.Fatal("an issue nobody named was synchronised")
		}
		if _, ok := h.issue(elsewhere.ID); ok {
			t.Fatal("an issue of a space that is not selected was synchronised")
		}
		if rows := h.rows(spaceBox("10000")); len(rows) != 1 {
			t.Fatalf("rows of the space: %v", keysOf(rows))
		}

		// A stored issue is marked when a mail names it, and stays when
		// it is no longer the user's.
		if is, _ := h.issue(watched.ID); is.ViaMail {
			t.Fatal("marked before any mail")
		}
		h.mailNaming(watched.Key)
		if is, _ := h.issue(watched.ID); !is.ViaMail {
			t.Fatal("the link did not mark the stored issue")
		}
		f.SetWatching(watched.ID, false)
		f.AddComment(named.ID, f.Petr, "<p>Ještě jedna zmínka.</p>")
		h.clock.advance(2 * time.Hour)
		h.mustPass() // with the reconciliation
		for _, want := range []*jiratest.Issue{named, watched} {
			is, ok := h.issue(want.ID)
			if !ok || !is.ViaMail {
				t.Fatalf("%s after the reconciliation: %+v %v", want.Key, is, ok)
			}
		}
		if is, _ := h.issue(watched.ID); is.Watching {
			t.Fatal("the reconciliation did not refresh the issue")
		}
		if rows := h.rows(spaceBox("10000")); len(rows) != 2 {
			t.Fatalf("the new comment did not arrive: %v", keysOf(rows))
		}
		// The mark outlives a refresh and the mail itself.
		if err := h.st.DeleteMessagesByRemoteID(context.Background(), h.mailNaming(named.Key).FolderID, []string{"note:" + named.Key}); err != nil {
			t.Fatal(err)
		}
		if linked, _ := h.st.IssueMailLinked(context.Background(), h.acc.ID, named.Key); linked {
			t.Fatal("the link outlived its message")
		}
		h.clock.advance(time.Minute)
		h.mustPass(named.Key)
		if is, ok := h.issue(named.ID); !ok || !is.ViaMail {
			t.Fatalf("after its mail went: %+v %v", is, ok)
		}
	})
}

// Without OnlyMine the mark is kept all the same, for the day the option
// is switched on.
func TestIssuesNamedByMailAreMarked(t *testing.T) {
	h := newHarness(t, jiratest.Cloud)
	f := h.f
	var named, other *jiratest.Issue
	h.at(h.ago(day), func() {
		named = f.AddIssue("ITSD", "Tiskárna", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		other = f.AddIssue("ITSD", "Skener", func(is *jiratest.Issue) { is.Reporter = f.Petr })
	})
	h.mailNaming(named.Key) // before the issue is stored
	h.mustPass()
	if is, ok := h.issue(named.ID); !ok || !is.ViaMail {
		t.Fatalf("named: %+v %v", is, ok)
	}
	if is, ok := h.issue(other.ID); !ok || is.ViaMail {
		t.Fatalf("other: %+v %v", is, ok)
	}
	h.reconfigure(func(c *api.JiraConfig) { c.OnlyMine = true })
	h.clock.advance(2 * time.Hour)
	h.mustPass()
	if _, ok := h.issue(named.ID); !ok {
		t.Fatal("OnlyMine dropped an issue a mail named")
	}
	if _, ok := h.issue(other.ID); ok {
		t.Fatal("OnlyMine kept someone else's issue")
	}
}

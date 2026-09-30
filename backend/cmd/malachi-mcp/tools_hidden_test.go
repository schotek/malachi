// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	daemonconfig "github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/core"
	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// A notification mail that an issue-tracker account hides in a mail
// account (docs/api.md §4.1 notificationMail) is in none of the bridge's
// listings. The daemon's own backend over its store answers here, not the
// fake: the filter is the store's.
func TestHiddenMailIsNotListed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mail := store.Account{Name: "Mail", Enabled: true, Config: api.AccountConfig{
		Name: "Mail", Email: "jana.dvorakova@example.invalid", Kind: api.AccountIMAP,
		IMAP: &api.ServerConfig{Host: "imap.example.invalid", Port: 993, Security: api.SecurityTLS, Username: "jana", AuthMethod: api.AuthPassword},
		SMTP: &api.ServerConfig{Host: "smtp.example.invalid", Port: 465, Security: api.SecurityTLS, Username: "jana", AuthMethod: api.AuthPassword},
	}}
	tracker := store.Account{Name: "Acme Jira", Enabled: true, Config: api.AccountConfig{
		Name: "Acme Jira", Email: "jana.dvorakova@example.invalid", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "jana.dvorakova@example.invalid",
			Spaces: []api.SpaceRef{{ID: "10000", Key: "ITSD"}}, NotificationMail: api.NotificationMailHide},
	}}
	for _, a := range []*store.Account{&mail, &tracker} {
		if err := st.AddAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	folders, _, err := st.UpsertFolders(ctx, mail.ID, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox := folders[0]
	now := time.Now()
	note := &store.Message{AccountID: mail.ID, FolderID: inbox.ID, UID: 1, Subject: "[JIRA] (ITSD-42) Printer on the 2nd floor",
		From: []api.Address{{Name: "Petr Svoboda (Jira)", Address: "jira@acme.atlassian.net"}}, Date: now.Add(-time.Hour), InternalDate: now.Add(-time.Hour)}
	lunch := &store.Message{AccountID: mail.ID, FolderID: inbox.ID, UID: 2, Subject: "Lunch by the printer",
		From: []api.Address{{Name: "Petr Svoboda", Address: "petr.svoboda@example.invalid"}}, Date: now, InternalDate: now}
	if err := st.UpsertMessages(ctx, []*store.Message{note, lunch}); err != nil {
		t.Fatal(err)
	}
	for m, text := range map[*store.Message]string{note: "Petr Svoboda commented: the printer has paper again.", lunch: "Meet me at the printer at noon."} {
		if err := st.SetMessageBody(ctx, m.ID, store.BodyUpdate{Text: text, Snippet: text, State: store.BodyFetched}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutIssue(ctx, store.Issue{AccountID: tracker.ID, IssueID: "10042", Key: "ITSD-42", SpaceID: "10000"}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkIssueMail(ctx, note.ID, tracker.ID, "ITSD-42", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecountFolder(ctx, inbox.ID); err != nil {
		t.Fatal(err)
	}

	b := core.New("test", st, daemonconfig.Default(), nil)
	t.Cleanup(b.Close)
	sock := tempSocket(t)
	srv := rpc.NewServer(b, slog.New(slog.DiscardHandler))
	if err := srv.Listen(sock); err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = srv.Serve(serveCtx); close(done) }()
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() {
			cancel()
			srv.Close()
			<-done
		})
	})
	cs, _, _ := connectBridge(t, sock, false, false)
	call := func(name string, args map[string]any) string {
		t.Helper()
		out, isErr := callTool(t, cs, name, args)
		if isErr {
			t.Fatalf("%s: %s", name, out)
		}
		return out
	}
	list := map[string]any{"accountId": mail.ID, "folderId": inbox.ID}
	search := map[string]any{"query": "printer"}

	// Shown: both messages everywhere.
	mustContain(t, call("list_messages", list), "2 messages on this page, 2 in the folder", note.Subject, lunch.Subject)
	mustContain(t, call("search_messages", search), note.Subject, lunch.Subject)
	mustContain(t, call("list_folders", map[string]any{"accountId": mail.ID}), `"total": 2`, `"unread": 2`)

	touched, err := st.SetIssueMailHidden(ctx, tracker.ID, true, nil)
	if err != nil || len(touched[mail.ID]) != 1 {
		t.Fatalf("hide: %v %v", touched, err)
	}
	out := call("list_messages", list)
	mustContain(t, out, "1 messages on this page, 1 in the folder", lunch.Subject)
	mustNotContain(t, out, note.Subject, "ITSD-42", note.ID)
	for _, args := range []map[string]any{
		search, {"query": "ITSD-42"}, {"query": "from:jira@acme.atlassian.net"}, {"query": "commented"},
		{"query": "printer", "accountId": mail.ID}, {"query": "printer", "accountId": mail.ID, "folderId": inbox.ID},
	} {
		mustNotContain(t, call("search_messages", args), note.Subject, note.ID)
	}
	mustContain(t, call("search_messages", search), lunch.Subject)
	mustContain(t, call("list_folders", map[string]any{"accountId": mail.ID}), `"total": 1`, `"unread": 1`)
	// By its id the message is still read: hiding is a filter of the
	// listings, the message is the user's.
	mustContain(t, call("read_message", map[string]any{"accountId": mail.ID, "messageId": note.ID}), note.Subject)

	// Shown again, it is back.
	if _, err := st.SetIssueMailHidden(ctx, tracker.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, call("list_messages", list), "2 messages on this page, 2 in the folder", note.Subject)
	mustContain(t, call("search_messages", search), note.Subject)
}

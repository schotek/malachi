// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// startMailServer runs an in-memory IMAP server with the user "me" and an
// empty INBOX, Sent and Trash.
func startMailServer(t *testing.T) (addr string, port int) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	for _, mb := range []string{"INBOX", "Sent", "Trash"} {
		if err := user.Create(mb, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}, imap.CapSpecialUse: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), ln.Addr().(*net.TCPAddr).Port
}

// notificationMail is a message as the site of jiratest sends it.
func notificationMail(from, subject string, n int) string {
	return "From: " + from + "\r\nTo: jana.dvorakova@example.invalid\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + fmt.Sprintf("\r\nMessage-ID: <note-%d@acme.atlassian.net>\r\n", n) +
		"Auto-Submitted: auto-generated\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPetr Svoboda commented.\r\n"
}

// TestEndToEndNotificationMail runs the production stack (store, the
// supervisors from New, the services) with a mail account on an
// in-memory IMAP server and a jira account on a fake site that hides its
// notification mail and keeps only the user's issues: mail stored before
// the account existed is hidden after its first pass; a notification that
// arrives brings its issue, although it is not the user's, and is hidden
// before anybody hears of it; a forgery is ordinary mail; the mail server
// hears nothing of any of it; and everything is back when the account
// stops hiding.
func TestEndToEndNotificationMail(t *testing.T) {
	fastIssueMail(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	addr, port := startMailServer(t)
	f := jiratest.New(t, jiratest.Cloud)
	mine := f.AddIssue("WEB", "Landing page typo")
	named := f.AddIssue("ITSD", "Printer on the 2nd floor", func(is *jiratest.Issue) { is.Reporter = f.Petr })
	const site = `"Petr Svoboda (Jira)" <jira@acme.atlassian.net>`
	earlier := "[JIRA] (" + mine.Key + ") Landing page typo"
	arriving := "[JIRA] (" + named.Key + ") Printer on the 2nd floor"
	forged := "[JIRA] Updates: (" + named.Key + ") Printer on the 2nd floor"
	appendRawTestMessage(t, addr, notificationMail(site, earlier, 1))
	appendTestMessage(t, addr, "Lunch", "At noon?")

	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	b.JiraHTTP = f.HTTPClient()
	rec := &noteRecorder{}
	b.SetNotifier(rec)
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the engines did not stop")
		}
		b.Close()
	})

	mailCfg := validConfig()
	mailCfg.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	mailCfg.SMTP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: mailCfg, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	mail := added.AccountID
	inbox := func() api.Folder {
		res, err := b.Folders().List(ctx, api.FolderListParams{AccountID: mail})
		if err != nil {
			return api.Folder{}
		}
		for _, fo := range res.Folders {
			if fo.Role == api.RoleInbox {
				return fo
			}
		}
		return api.Folder{}
	}
	// stored reports a message of the mail account by its subject: whether
	// its body is stored, and whether it is hidden.
	stored := func(subject string) (fetched, hidden bool) {
		var state string
		var h int
		err := b.store.DB().QueryRowContext(ctx, `SELECT body_state, hidden FROM messages WHERE account_id = ? AND subject = ?`, mail, subject).Scan(&state, &h)
		return err == nil && state == "fetched", h != 0
	}
	waitUntil(t, ctx, "the mailbox", func() bool {
		a, _ := stored(earlier)
		l, _ := stored("Lunch")
		return a && l && inbox().Total == 2
	})

	jc := f.Config()
	jc.OnlyMine, jc.NotificationMail = true, api.NotificationMailHide
	start := time.Now()
	added, err = b.Accounts().Add(ctx, api.AccountAddParams{
		Config:      api.AccountConfig{Name: "Acme Jira", Email: jiratest.Login, Kind: api.AccountJira, Jira: &jc},
		Credentials: api.Credentials{Password: jiratest.Token},
	})
	if err != nil {
		t.Fatal(err)
	}
	jira := added.AccountID
	var st api.SyncState
	waitUntil(t, ctx, "the first pass of the jira account", func() bool {
		st, _ = b.Supervisor.State(string(jira))
		return st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(start)
	}, func() string { return fmt.Sprintf("state %+v, error %v", st, st.Error) })
	issue := func(key string) (viaMail, ok bool) {
		is, err := b.store.IssueByKey(ctx, string(jira), key)
		return is.ViaMail, err == nil
	}
	if _, ok := issue(named.Key); ok {
		t.Fatal("an issue that is not the user's was synchronised")
	}

	// The mail that was there before is hidden once the first pass ended.
	waitUntil(t, ctx, "the earlier notification to be hidden", func() bool {
		_, hidden := stored(earlier)
		return hidden && inbox().Total == 1 && inbox().Unread == 1
	})
	wantChanged := string(mail) + ":" + string(inbox().ID)
	waitUntil(t, ctx, "notify.messagesChanged", func() bool { return slices.Contains(rec.takeChanged(), wantChanged) })

	// A notification arrives about an issue that is not the user's.
	f.AddComment(named.ID, f.Petr, "<p>Jana, could you have a look?</p>")
	appendRawTestMessage(t, addr, notificationMail(site, arriving, 2))
	waitUntil(t, ctx, "the arriving notification to be hidden", func() bool {
		fetched, hidden := stored(arriving)
		return fetched && hidden
	})
	if viaMail, ok := issue(named.Key); !ok || !viaMail {
		t.Fatalf("the issue the mail named: stored %v, marked %v", ok, viaMail)
	}
	waitUntil(t, ctx, "notify.messagesChanged for the arriving mail", func() bool { return slices.Contains(rec.takeChanged(), wantChanged) })
	if fo := inbox(); fo.Total != 1 || fo.Unread != 1 {
		t.Fatalf("inbox counts %d/%d", fo.Unread, fo.Total)
	}
	// The issue's news is the jira account's; the mail account has none.
	waitUntil(t, ctx, "the issue's news", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, n := range rec.news {
			if n.AccountID == jira && n.Message.Issue != nil && n.Message.Issue.Key == named.Key && n.Message.Issue.Item == api.IssueItemComment {
				return true
			}
		}
		return false
	})

	// A forgery under the site's name is ordinary mail: shown, announced.
	appendRawTestMessage(t, addr, notificationMail(`"jira@acme.atlassian.net" <mallory@example.org>`, forged, 3))
	waitUntil(t, ctx, "the forgery to be announced", func() bool { return slices.Contains(rec.announced(), forged) })
	if _, hidden := stored(forged); hidden {
		t.Fatal("the forgery is hidden")
	}
	for _, s := range rec.announced() {
		if s == arriving || s == earlier {
			t.Fatalf("a hidden notification was announced: %q", s)
		}
	}
	list, err := b.Messages().List(ctx, api.MessageListParams{AccountID: mail, FolderID: inbox().ID})
	if err != nil || len(list.Messages) != 2 || list.Page.Total != 2 {
		t.Fatalf("message.list = %+v, %v", list, err)
	}
	for _, m := range list.Messages {
		if m.Subject != forged && m.Subject != "Lunch" {
			t.Fatalf("message.list shows %q", m.Subject)
		}
	}
	res, err := b.Search().Query(ctx, api.SearchQueryParams{Query: "commented"})
	if err != nil || len(res.Results) != 1 || res.Results[0].Message.Subject != forged {
		t.Fatalf("search = %+v, %v", res, err)
	}

	// The mail server heard nothing: every message is there, unread.
	if n := serverMailboxCount(t, addr, "INBOX"); n != 4 {
		t.Fatalf("%d messages on the server", n)
	}
	for _, s := range []string{earlier, arriving} {
		for _, flag := range []imap.Flag{imap.FlagSeen, imap.FlagDeleted, imap.FlagFlagged} {
			if serverHasFlag(t, addr, s, flag) {
				t.Fatalf("%q has %s on the server", s, flag)
			}
		}
	}

	// A full pass with its reconciliation keeps the issue the mail named.
	start = time.Now()
	if _, err := b.Sync().Trigger(ctx, api.SyncTriggerParams{AccountID: jira, Full: true}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "the full pass", func() bool {
		st, _ = b.Supervisor.State(string(jira))
		return st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(start)
	})
	if viaMail, ok := issue(named.Key); !ok || !viaMail {
		t.Fatalf("after the reconciliation: stored %v, marked %v", ok, viaMail)
	}
	if _, hidden := stored(arriving); !hidden {
		t.Fatal("the reconciliation showed the mail")
	}

	// The account stops hiding: everything is back.
	a, err := b.store.GetAccount(ctx, string(jira))
	if err != nil {
		t.Fatal(err)
	}
	next := *cloneConfig(a.Config)
	next.Jira.NotificationMail = api.NotificationMailSync
	rec.takeChanged()
	if _, err := b.Accounts().Update(ctx, api.AccountUpdateParams{AccountID: jira, Config: next}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "the mail to be shown again", func() bool {
		_, h1 := stored(earlier)
		_, h2 := stored(arriving)
		return !h1 && !h2 && inbox().Total == 4 && inbox().Unread == 4
	})
	waitUntil(t, ctx, "notify.messagesChanged when shown", func() bool { return slices.Contains(rec.takeChanged(), wantChanged) })
	if !strings.HasPrefix(string(jira), "acc_") {
		t.Fatalf("account id %q", jira)
	}
}

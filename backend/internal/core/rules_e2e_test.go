// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

// newMailRecorder records notify.newMessage.
type newMailRecorder struct {
	recNotifier
	mu   sync.Mutex
	news []string
}

func (r *newMailRecorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.news = append(r.news, n.Message.Subject)
	r.mu.Unlock()
}

func (r *newMailRecorder) subjects() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.news...)
}

// TestRulesEndToEnd wires the production sync against an in-memory IMAP
// server: new mail a rule moves out of the inbox or marks read is not
// announced as new mail, the move reaches the server, and other new mail
// is announced as before.
func TestRulesEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	for _, mb := range []string{"INBOX", "Archive"} {
		if err := user.Create(mb, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}, imap.CapIdle: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	addr := ln.Addr().String()
	appendTestMessage(t, addr, "Before", "old mail")

	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	rec := &newMailRecorder{}
	b.SetNotifier(rec)
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	acc := validConfig()
	port := ln.Addr().(*net.TCPAddr).Port
	acc.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	acc.SMTP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID
	folders := map[string]api.Folder{}
	waitUntil(t, ctx, "the first sync", func() bool {
		res, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
		if err != nil {
			return false
		}
		for _, f := range res.Folders {
			folders[f.Path] = f
		}
		return folders["INBOX"].Total == 1 && folders["Archive"].ID != ""
	})
	for _, in := range []api.RuleInput{
		subjectRule("promo", "Promo", id, api.RuleAction{Kind: api.RuleActionMove, FolderID: folders["Archive"].ID}),
		subjectRule("quiet", "Quiet", id, api.RuleAction{Kind: api.RuleActionMarkRead}),
		// Old mail is left alone even when a rule matches it.
		subjectRule("before", "Before", id, api.RuleAction{Kind: api.RuleActionFlag}),
	} {
		if _, err := b.Rules().Create(ctx, api.RulesCreateParams{Rule: in}); err != nil {
			t.Fatal(err)
		}
	}
	appendTestMessage(t, addr, "Promo of the week", "buy")
	appendTestMessage(t, addr, "Quiet note", "fyi")
	appendTestMessage(t, addr, "Plain hello", "hi")
	waitUntil(t, ctx, "the promo on the server's Archive", func() bool { return serverMailboxCount(t, addr, "Archive") == 1 },
		func() string { return "announced " + joinSubjects(rec.subjects()) })
	waitUntil(t, ctx, "the quiet note read on the server", func() bool { return serverHasFlag(t, addr, "Quiet note", imap.FlagSeen) })
	waitUntil(t, ctx, "the plain message announced", func() bool { return len(rec.subjects()) > 0 })
	// Give a late announcement the chance to show.
	time.Sleep(300 * time.Millisecond)
	if got := rec.subjects(); len(got) != 1 || got[0] != "Plain hello" {
		t.Fatalf("announced %v", got)
	}
	if serverHasFlag(t, addr, "Before", imap.FlagFlagged) {
		t.Error("a rule ran on mail of the first sync")
	}
}

func joinSubjects(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

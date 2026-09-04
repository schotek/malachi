// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

// TestEndToEndSync wires the production stack (store, real supervisor from
// New, keyring, services) against an in-memory IMAP server: an account is
// added through the API, its folders and messages arrive in the store, the
// body is readable as text, and a local flag change is pushed to the server.
func TestEndToEndSync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// IMAP server with one user and a few messages.
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
	port := ln.Addr().(*net.TCPAddr).Port
	for i := 1; i <= 3; i++ {
		appendTestMessage(t, ln.Addr().String(), fmt.Sprintf("Hello %d", i), fmt.Sprintf("Body %d with čeština.", i))
	}

	// Production backend with an in-memory keyring and a fast poll interval.
	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0 // manual: IDLE plus explicit triggers only
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
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
	acc.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	acc.SMTP = &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID

	// Folders and messages arrive.
	var inbox api.Folder
	waitUntil(t, ctx, "inbox with 3 messages", func() bool {
		res, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
		if err != nil {
			return false
		}
		for _, f := range res.Folders {
			if f.Role == api.RoleInbox && f.Total == 3 {
				inbox = f
				return true
			}
		}
		return false
	})
	list, err := b.Messages().List(ctx, api.MessageListParams{AccountID: id, FolderID: inbox.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Messages) != 3 || list.Page.Total != 3 {
		t.Fatalf("list = %+v", list)
	}
	first := list.Messages[0]
	if first.From[0].Address != "alice@example.org" || !strings.HasPrefix(first.Subject, "Hello") {
		t.Fatalf("summary = %+v", first)
	}

	// Body text is there (or arrives with the next pass).
	waitUntil(t, ctx, "body fetched", func() bool {
		body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: first.ID})
		return err == nil && body.BodyState == api.BodyFetched && strings.Contains(body.Text, "čeština")
	})
	body, _ := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: first.ID})
	if body.HTML != "" || body.SanitizerVersion != "0-stub" || body.Links == nil {
		t.Fatalf("body shape = %+v", body)
	}
	// The pass may still be storing the other bodies; it ends idle with a
	// lastSync.
	waitUntil(t, ctx, "syncer idle after the pass", func() bool {
		state, _ := b.Supervisor.State(string(id))
		return state.Status == api.SyncIdle && state.LastSync != nil
	})

	// Local flag change reaches the server.
	if _, err := b.Messages().Flag(ctx, api.MessageFlagParams{AccountID: id, MessageIDs: []api.MessageID{first.ID}, Set: []api.Flag{api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: first.ID})
	if !hasFlagE2E(got.Message.Flags, api.FlagFlagged) {
		t.Fatalf("local flags = %v", got.Message.Flags)
	}
	waitUntil(t, ctx, "flag on server", func() bool {
		return serverHasFlag(t, ln.Addr().String(), first.Subject, imap.FlagFlagged)
	})

	// Trash moves the message; the inbox count drops locally.
	if _, err := b.Messages().Delete(ctx, api.MessageDeleteParams{AccountID: id, MessageIDs: []api.MessageID{first.ID}}); err != nil {
		t.Fatal(err)
	}
	res, _ := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
	for _, f := range res.Folders {
		if f.Role == api.RoleInbox && f.Total != 2 {
			t.Fatalf("inbox total after trash = %d", f.Total)
		}
	}
	waitUntil(t, ctx, "trash on server", func() bool {
		return serverMailboxCount(t, ln.Addr().String(), "Trash") == 1
	})
}

// TestEndToEndSend runs the production stack against the in-memory IMAP
// server and a recording SMTP server: a text draft is sent through the
// API, the SMTP server receives it (Bcc in the envelope only), the syncer
// files the copy in the server's Sent folder, and the outbox drains.
func TestEndToEndSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	for _, mb := range []string{"INBOX", "Sent", "Trash"} {
		if err := user.Create(mb, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}, imap.CapSpecialUse: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go imapSrv.Serve(imapLn)
	t.Cleanup(func() { imapSrv.Close() })

	smtpRec := startSMTPRecorder(t)

	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisors did not stop")
		}
	})

	acc := validConfig()
	acc.DisplayName = "Me"
	acc.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: imapLn.Addr().(*net.TCPAddr).Port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	acc.SMTP = &api.ServerConfig{Host: "127.0.0.1", Port: smtpRec.port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID

	// The folder list (with a Sent folder) arrives from the first pass.
	var sent api.Folder
	waitUntil(t, ctx, "sent folder", func() bool {
		res, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
		if err != nil {
			return false
		}
		for _, f := range res.Folders {
			if f.Role == api.RoleSent {
				sent = f
				return true
			}
		}
		return false
	})

	saved, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{
		AccountID: id,
		To:        []api.Address{{Name: "Bob", Address: "bob@example.org"}},
		BCC:       []api.Address{{Address: "hidden@example.org"}},
		Subject:   "Sent from the test",
		TextBody:  "Hello Bob, this is čeština.\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: saved.DraftID, Version: saved.Version})
	if err != nil {
		t.Fatal(err)
	}

	// The SMTP server receives it: Bcc in the envelope, not in the data.
	waitUntil(t, ctx, "smtp delivery", func() bool { _, _, data := smtpRec.envelope(); return len(data) > 0 })
	from, rcpts, data := smtpRec.envelope()
	if from != "me@example.invalid" || strings.Join(rcpts, " ") != "bob@example.org hidden@example.org" {
		t.Fatalf("envelope = %s %v", from, rcpts)
	}
	text := string(data)
	if strings.Contains(text, "hidden@example.org") || !strings.Contains(text, "To: \"Bob\" <bob@example.org>") ||
		!strings.Contains(text, "From: \"Me\" <me@example.invalid>") || !strings.Contains(text, "Subject: Sent from the test") {
		t.Fatalf("data:\n%s", text)
	}
	if !smtpRec.authenticated() {
		t.Fatal("delivered without authentication")
	}

	// The syncer appends the copy to the server's Sent folder (WP-I), then
	// the local Sent folder lists it as seen and the outbox is empty.
	waitUntil(t, ctx, "copy in server Sent folder", func() bool {
		return serverMailboxCount(t, imapLn.Addr().String(), "Sent") == 1
	})
	waitUntil(t, ctx, "local sent copy", func() bool {
		list, err := b.Messages().List(ctx, api.MessageListParams{AccountID: id, FolderID: sent.ID})
		return err == nil && len(list.Messages) == 1 && list.Messages[0].Subject == "Sent from the test" && hasFlagE2E(list.Messages[0].Flags, api.FlagSeen)
	})
	waitUntil(t, ctx, "outbox drained", func() bool {
		_, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: res.OutboxID})
		return err != nil
	})
	st, err := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: id})
	if err != nil || st.Accounts[0].PendingOutbox != 0 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	senders, _ := b.Senders().List(ctx, api.SenderListParams{})
	known := 0
	for _, s := range senders.Senders {
		if (s.Address == "bob@example.org" || s.Address == "hidden@example.org") && s.Source == api.KnownSenderSourceSent {
			known++
		}
	}
	if known != 2 {
		t.Fatalf("known senders = %+v", senders.Senders)
	}
}

// smtpRecorder is a go-smtp server that accepts AUTH PLAIN for me/pw and
// records the last envelope and message.
type smtpRecorder struct {
	port int

	mu    sync.Mutex
	from  string
	rcpts []string
	data  []byte
	auth  bool
}

func startSMTPRecorder(t *testing.T) *smtpRecorder {
	t.Helper()
	rec := &smtpRecorder{}
	srv := gosmtp.NewServer(rec)
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	rec.port = ln.Addr().(*net.TCPAddr).Port
	return rec
}

func (r *smtpRecorder) envelope() (string, []string, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.from, append([]string(nil), r.rcpts...), append([]byte(nil), r.data...)
}

func (r *smtpRecorder) authenticated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.auth
}

func (r *smtpRecorder) NewSession(*gosmtp.Conn) (gosmtp.Session, error) {
	return &smtpSession{rec: r}, nil
}

type smtpSession struct{ rec *smtpRecorder }

func (*smtpSession) Reset()        {}
func (*smtpSession) Logout() error { return nil }

func (s *smtpSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *smtpSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, username, pw string) error {
		if username != "me" || pw != "pw" {
			return gosmtp.ErrAuthFailed
		}
		s.rec.mu.Lock()
		s.rec.auth = true
		s.rec.mu.Unlock()
		return nil
	}), nil
}

func (s *smtpSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.from, s.rec.rcpts = from, nil
	return nil
}

func (s *smtpSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.rcpts = append(s.rec.rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.data = data
	return nil
}

func waitUntil(t *testing.T, ctx context.Context, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func hasFlagE2E(flags []api.Flag, f api.Flag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

func appendTestMessage(t *testing.T, addr, subject, body string) {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("me", "pw").Wait(); err != nil {
		t.Fatal(err)
	}
	raw := "From: Alice <alice@example.org>\r\nTo: me@example.org\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMessage-ID: <" + strings.ReplaceAll(subject, " ", "") + "@example.org>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"
	cmd := c.Append("INBOX", int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	c.Logout().Wait()
}

func serverHasFlag(t *testing.T, addr, subject string, flag imap.Flag) bool {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		return false
	}
	defer c.Close()
	if err := c.Login("me", "pw").Wait(); err != nil {
		return false
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		return false
	}
	var set imap.SeqSet
	set.AddRange(1, 0) // 1:*
	msgs, err := c.Fetch(set, &imap.FetchOptions{Envelope: true, Flags: true}).Collect()
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.Envelope != nil && m.Envelope.Subject == subject {
			for _, f := range m.Flags {
				if f == flag {
					return true
				}
			}
		}
	}
	c.Logout().Wait()
	return false
}

func serverMailboxCount(t *testing.T, addr, mailbox string) int {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		return -1
	}
	defer c.Close()
	if err := c.Login("me", "pw").Wait(); err != nil {
		return -1
	}
	st, err := c.Status(mailbox, &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil || st.NumMessages == nil {
		return -1
	}
	c.Logout().Wait()
	return int(*st.NumMessages)
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

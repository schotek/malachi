// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const waitTimeout = 15 * time.Second

// fullCaps is a modern server: MOVE, UIDPLUS and LIST-STATUS on top of rev1.
var fullCaps = imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}, imap.CapListStatus: {}}

// rev1Caps is a bare IMAP4rev1 server (IDLE is always offered).
var rev1Caps = imap.CapSet{imap.CapIMAP4rev1: {}}

// proxy is a controllable TCP relay between the syncer and the server so
// tests can simulate an outage: while blocked, new connections are closed
// at once and existing ones are cut.
type proxy struct {
	ln     net.Listener
	target string

	mu      sync.Mutex
	blocked bool
	conns   map[net.Conn]struct{}
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln, target: target, conns: map[net.Conn]struct{}{}}
	go p.serve()
	t.Cleanup(func() { ln.Close(); p.block(true) })
	return p
}

func (p *proxy) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		blocked := p.blocked
		p.mu.Unlock()
		if blocked {
			c.Close()
			continue
		}
		go p.relay(c)
	}
}

func (p *proxy) relay(c net.Conn) {
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		c.Close()
		return
	}
	p.mu.Lock()
	if p.blocked {
		p.mu.Unlock()
		c.Close()
		up.Close()
		return
	}
	p.conns[c] = struct{}{}
	p.conns[up] = struct{}{}
	p.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
	c.Close()
	up.Close()
	<-done
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, up)
	p.mu.Unlock()
}

// block cuts every connection and refuses new ones until unblocked.
func (p *proxy) block(blocked bool) {
	p.mu.Lock()
	p.blocked = blocked
	if blocked {
		for c := range p.conns {
			c.Close()
		}
	}
	p.mu.Unlock()
}

// recorder is an api.Notifier that keeps every notification and hands
// new-message and auth notifications out on channels.
type recorder struct {
	mu     sync.Mutex
	states []api.SyncState
	news   []api.NewMessageNotification
	auths  []api.AuthRequiredNotification
	newCh  chan api.NewMessageNotification
	authCh chan api.AuthRequiredNotification
}

func newRecorder() *recorder {
	return &recorder{newCh: make(chan api.NewMessageNotification, 64), authCh: make(chan api.AuthRequiredNotification, 64)}
}

func (r *recorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	r.news = append(r.news, n)
	r.mu.Unlock()
	select {
	case r.newCh <- n:
	default:
	}
}

func (r *recorder) SyncState(n api.SyncStateNotification) {
	r.mu.Lock()
	r.states = append(r.states, n.State)
	r.mu.Unlock()
}

func (r *recorder) AuthRequired(n api.AuthRequiredNotification) {
	r.mu.Lock()
	r.auths = append(r.auths, n)
	r.mu.Unlock()
	select {
	case r.authCh <- n:
	default:
	}
}

func (r *recorder) AccountsChanged(api.AccountsChangedNotification) {}

func (r *recorder) newMessages() []api.NewMessageNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]api.NewMessageNotification(nil), r.news...)
}

func (r *recorder) sawStatus(status api.SyncStatus) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, st := range r.states {
		if st.Status == status {
			return true
		}
	}
	return false
}

func (r *recorder) authCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.auths)
}

// harnessOptions tunes one test setup.
type harnessOptions struct {
	caps    imap.CapSet   // server capabilities; nil = fullCaps
	noIdle  bool          // hide IDLE from the syncer
	prefs   SyncPrefs     // initial preferences; zero = 0 s interval, 30 days
	backoff time.Duration // reconnect delay; zero = 50 ms
	now     func() time.Time
}

// harness is a memserver behind a proxy, a temporary store with one
// account, a recording notifier and one syncer.
type harness struct {
	t      *testing.T
	mem    *imapmemserver.Server
	user   *imapmemserver.User
	srvURL string // direct server address for helper clients
	proxy  *proxy
	st     *store.Store
	acc    store.Account
	notes  *recorder

	mu       sync.Mutex
	password string
	pwErr    error
	prefs    SyncPrefs

	syncer *Syncer
	cancel context.CancelFunc
	done   chan error
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	caps := o.caps
	if caps == nil {
		caps = fullCaps
	}
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", password)
	mem.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         caps,
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	px := newProxy(t, ln.Addr().String())

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := store.Account{Enabled: true, Config: api.AccountConfig{Name: "Test", Email: "me@example.test", IMAP: cfg(px.port(), api.SecurityNone)}}
	if err := st.AddAccount(context.Background(), &acc); err != nil {
		t.Fatal(err)
	}

	prefs := o.prefs
	if prefs.OfflineDays == 0 && prefs.IntervalSeconds == 0 {
		prefs.OfflineDays = 30
	}
	h := &harness{t: t, mem: mem, user: user, srvURL: ln.Addr().String(), proxy: px, st: st, acc: acc, notes: newRecorder(), password: password, prefs: prefs}
	backoff := o.backoff
	if backoff == 0 {
		backoff = 50 * time.Millisecond
	}
	deps := Deps{
		Store: st,
		Password: func(context.Context) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.password, h.pwErr
		},
		Notifier: h.notes,
		Prefs: func() SyncPrefs {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.prefs
		},
		Log:     slog.New(slog.DiscardHandler),
		Now:     o.now,
		Backoff: func(int) time.Duration { return backoff },
	}
	if o.noIdle {
		deps.CapFilter = func(c imap.CapSet) imap.CapSet {
			out := c.Copy()
			delete(out, imap.CapIdle)
			return out
		}
	}
	h.syncer = NewSyncer(acc, deps)
	return h
}

// start runs the syncer until the test ends.
func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- h.syncer.Run(ctx) }()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.cancel = nil
	select {
	case <-h.done:
	case <-time.After(waitTimeout):
		h.t.Error("syncer did not stop")
	}
}

func (h *harness) setPassword(pw string, err error) {
	h.mu.Lock()
	h.password, h.pwErr = pw, err
	h.mu.Unlock()
}

func (h *harness) setPrefs(p SyncPrefs) {
	h.mu.Lock()
	h.prefs = p
	h.mu.Unlock()
}

// client is a second, logged-in session straight to the server.
func (h *harness) client() *imapclient.Client {
	h.t.Helper()
	c, err := imapclient.DialInsecure(h.srvURL, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := c.Login("me", password).Wait(); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c
}

// rawMessage builds a simple RFC 5322 message.
func rawMessage(id, subject, body string) string {
	return fmt.Sprintf("From: Alice <alice@example.test>\r\nTo: me@example.test\r\nSubject: %s\r\nDate: Mon, 01 Sep 2026 10:00:00 +0000\r\nMessage-ID: <%s@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", subject, id, body)
}

// append stores a message on the server with the given internal date.
func (h *harness) append(mailbox, raw string, at time.Time, flags ...imap.Flag) uint32 {
	h.t.Helper()
	c := h.client()
	cmd := c.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Time: at, Flags: flags})
	if _, err := io.WriteString(cmd, raw); err != nil {
		h.t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		h.t.Fatal(err)
	}
	data, err := cmd.Wait()
	if err != nil {
		h.t.Fatal(err)
	}
	c.Logout().Wait()
	return uint32(data.UID)
}

// serverUIDs lists the UIDs of a mailbox on the server.
func (h *harness) serverUIDs(mailbox string) []uint32 {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		h.t.Fatal(err)
	}
	data, err := c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []uint32
	for _, u := range data.AllUIDs() {
		out = append(out, uint32(u))
	}
	return out
}

// serverFlags reads the flags of one message.
func (h *harness) serverFlags(mailbox string, uid uint32) []imap.Flag {
	h.t.Helper()
	c := h.client()
	defer func() { c.Logout().Wait() }()
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		h.t.Fatal(err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		h.t.Fatal(err)
	}
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0].Flags
}

// folder finds the stored folder with the given role or mailbox.
func (h *harness) folder(roleOrMailbox string) store.Folder {
	h.t.Helper()
	folders, err := h.st.ListFolders(context.Background(), h.acc.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, f := range folders {
		if string(f.Role) == roleOrMailbox || f.Mailbox == roleOrMailbox {
			return f
		}
	}
	h.t.Fatalf("no folder %q in %v", roleOrMailbox, folders)
	return store.Folder{}
}

// messages lists a folder's local messages, newest first.
func (h *harness) messages(folderID string) []store.Message {
	h.t.Helper()
	items, _, _, err := h.st.ListMessages(context.Background(), h.acc.ID, folderID, "", 500, api.SortDateDesc, false)
	if err != nil {
		h.t.Fatal(err)
	}
	return items
}

// waitIdle blocks until the syncer reports idle with a LastSync after
// since.
func (h *harness) waitIdle(since time.Time) api.SyncState {
	h.t.Helper()
	var st api.SyncState
	waitFor(h.t, "idle state", func() bool {
		st = h.syncer.State()
		return st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(since)
	})
	return st
}

// waitStatus blocks until the syncer reports the status.
func (h *harness) waitStatus(status api.SyncStatus) {
	h.t.Helper()
	waitFor(h.t, string(status)+" state", func() bool { return h.syncer.State().Status == status })
}

// waitNewMessage blocks until a notify.newMessage arrives.
func (h *harness) waitNewMessage() api.NewMessageNotification {
	h.t.Helper()
	select {
	case n := <-h.notes.newCh:
		return n
	case <-time.After(waitTimeout):
		h.t.Fatalf("no newMessage notification (state %+v)", h.syncer.State())
		return api.NewMessageNotification{}
	}
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func hasFlag(flags []api.Flag, f api.Flag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

func hasIMAPFlag(flags []imap.Flag, f imap.Flag) bool {
	for _, x := range flags {
		if strings.EqualFold(string(x), string(f)) {
			return true
		}
	}
	return false
}

func daysAgo(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour) }

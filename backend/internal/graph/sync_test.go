// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const waitTimeout = 10 * time.Second

// recorder collects notifications and hands new-message and auth
// notifications out on channels.
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

// harness is a fake Graph, a temporary store with one account, a
// recording notifier and one syncer.
type harness struct {
	t     *testing.T
	fake  *fakeGraph
	st    *store.Store
	acc   store.Account
	notes *recorder

	mu       sync.Mutex
	token    string
	tokenErr error
	prefs    SyncPrefs
	tokens   int // Token calls

	syncer *Syncer
	cancel context.CancelFunc
	done   chan error
}

func newHarness(t *testing.T, prefs SyncPrefs) *harness {
	t.Helper()
	fake := newFakeGraph(t)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := store.Account{Name: "Contoso", Enabled: true, Config: api.AccountConfig{
		Name: "Contoso", Email: "me@contoso.invalid", Kind: api.AccountGraph,
		Graph: &api.GraphConfig{Source: api.GraphSourceGOA, GOAAccountID: "account_1_0"},
	}}
	if err := st.AddAccount(context.Background(), &acc); err != nil {
		t.Fatal(err)
	}
	if prefs.OfflineDays == 0 && prefs.IntervalSeconds == 0 {
		prefs.OfflineDays = 30
	}
	h := &harness{t: t, fake: fake, st: st, acc: acc, notes: newRecorder(), token: fake.token, prefs: prefs}
	h.syncer = NewSyncer(acc, Deps{
		Store: st,
		Token: func(context.Context) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.tokens++
			return h.token, h.tokenErr
		},
		Notifier: h.notes,
		Prefs: func() SyncPrefs {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.prefs
		},
		Log:     slog.New(slog.DiscardHandler),
		BaseURL: fake.srv.URL,
		Backoff: func(int) time.Duration { return 50 * time.Millisecond },
		Sleep:   func(context.Context, time.Duration) error { return nil },
	})
	return h
}

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

func (h *harness) setToken(tok string, err error) {
	h.mu.Lock()
	h.token, h.tokenErr = tok, err
	h.mu.Unlock()
}

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

func (h *harness) messages(folderID string) []store.Message {
	h.t.Helper()
	items, _, _, err := h.st.ListMessages(context.Background(), h.acc.ID, folderID, "", 500, api.SortDateDesc, api.FilterAll)
	if err != nil {
		h.t.Fatal(err)
	}
	return items
}

func (h *harness) byRemote(remoteID string) (store.Message, bool) {
	h.t.Helper()
	folders, _ := h.st.ListFolders(context.Background(), h.acc.ID)
	for _, f := range folders {
		for _, m := range h.messages(f.ID) {
			if m.RemoteID == remoteID {
				return m, true
			}
		}
	}
	return store.Message{}, false
}

func (h *harness) waitIdle(since time.Time) api.SyncState {
	h.t.Helper()
	var st api.SyncState
	waitFor(h.t, "idle state", func() bool {
		st = h.syncer.State()
		return st.Status == api.SyncIdle && st.LastSync != nil && st.LastSync.After(since)
	})
	return st
}

func (h *harness) waitStatus(status api.SyncStatus) {
	h.t.Helper()
	waitFor(h.t, string(status)+" state", func() bool { return h.syncer.State().Status == status })
}

// pass triggers a pass (optionally of one folder) and waits for it.
func (h *harness) pass(folder api.FolderID, full bool) {
	h.t.Helper()
	start := time.Now()
	h.syncer.Trigger(folder, full)
	h.waitIdle(start)
}

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

func daysAgo(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour) }

func TestInitialSyncFoldersRolesAndBodies(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	old := h.fake.add("F-INBOX", "Old news", "alice@example.test", daysAgo(60), "ancient body")
	fresh := h.fake.add("F-INBOX", "Fresh news", "alice@example.test", daysAgo(2), "hello world")
	h.fake.setRead(fresh, true)
	h.fake.add("F-SENT", "I wrote this", "me@contoso.invalid", daysAgo(1), "my text")
	h.fake.add("F-PROJ-A", "Deep", "bob@example.test", daysAgo(3), "nested")
	for i := 0; i < 3; i++ { // more than one delta page
		h.fake.add("F-INBOX", fmt.Sprintf("Page %d", i), "carol@example.test", daysAgo(4+i), "p")
	}

	start := time.Now()
	h.start()
	h.waitIdle(start)

	inbox := h.folder("inbox")
	if inbox.Mailbox != "F-INBOX" || inbox.Path != "Inbox" || inbox.DeltaLink == "" {
		t.Fatalf("inbox = %+v", inbox)
	}
	for mailbox, role := range map[string]api.FolderRole{"F-SENT": api.RoleSent, "F-DRAFTS": api.RoleDrafts, "F-TRASH": api.RoleTrash, "F-JUNK": api.RoleJunk, "F-PROJ": api.RoleNone} {
		if f := h.folder(mailbox); f.Role != role {
			t.Errorf("%s role = %q, want %q", mailbox, f.Role, role)
		}
	}
	deep := h.folder("F-PROJ-A")
	if deep.Path != "Projects/AlphaBeta" || deep.ParentID != h.folder("F-PROJ").ID || deep.Name != "AlphaBeta" {
		t.Fatalf("nested folder = %+v", deep)
	}
	msgs := h.messages(inbox.ID)
	if len(msgs) != 4 {
		t.Fatalf("inbox messages = %d (window must drop the old one): %+v", len(msgs), msgs)
	}
	if _, ok := h.byRemote(old); ok {
		t.Fatal("message outside the window stored")
	}
	m, ok := h.byRemote(fresh)
	if !ok {
		t.Fatal("fresh message missing")
	}
	if m.Subject != "Fresh news" || m.BodyState != store.BodyFetched || m.Snippet != "hello world" ||
		m.RFCMessageID != "Freshnews@example.test" || m.ThreadID != "conv-Fresh news" || !hasFlag(m.Flags, api.FlagSeen) ||
		len(m.From) != 1 || m.From[0].Address != "alice@example.test" || m.InternalDate.IsZero() || m.Size == 0 {
		t.Fatalf("message = %+v", m)
	}
	if _, err := os.Stat(h.st.MessageRawPath(h.acc.ID, m.ID)); err != nil {
		t.Fatalf("raw file: %v", err)
	}
	if inbox.Unread != 3 || inbox.Total != 4 {
		t.Fatalf("counts = %d/%d", inbox.Unread, inbox.Total)
	}
	if sent := h.messages(h.folder("sent").ID); len(sent) != 1 || sent[0].BodyState != store.BodyFetched {
		t.Fatalf("sent messages = %+v", sent)
	}
	if n := h.notes.newMessages(); len(n) != 0 {
		t.Fatalf("initial sync must not notify, got %+v", n)
	}
	if !h.notes.sawStatus(api.SyncSyncing) {
		t.Fatal("no syncing state emitted")
	}
	if n := h.fake.count("GET", "/me/mailFolders/F-INBOX/messages/delta"); n < 2 {
		t.Fatalf("delta pages fetched = %d, want paging", n)
	}
}

func TestIncrementalNewMessageNotifiesAndSeenFlag(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	first := h.fake.add("F-INBOX", "Ping", "alice@example.test", daysAgo(1), "first")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	inbox := h.folder("inbox")
	before := h.fake.count("GET", "/me/messages/")

	second := h.fake.add("F-INBOX", "Pong", "alice@example.test", time.Now(), "second body line")
	h.fake.setRead(first, true)
	h.pass(api.FolderID(inbox.ID), false)

	news := h.notes.newMessages()
	if len(news) != 1 || news[0].Message.Subject != "Pong" || news[0].FolderID != api.FolderID(inbox.ID) || news[0].Message.Snippet != "second body line" {
		t.Fatalf("notifications = %+v", news)
	}
	m, _ := h.byRemote(first)
	if !hasFlag(m.Flags, api.FlagSeen) {
		t.Fatalf("server read state not applied: %+v", m.Flags)
	}
	if m2, _ := h.byRemote(second); m2.BodyState != store.BodyFetched {
		t.Fatalf("second body: %+v", m2)
	}
	if n := h.fake.count("GET", "/me/messages/") - before; n != 1 {
		t.Fatalf("bodies downloaded on the incremental pass = %d, want 1", n)
	}
	// A pending local flag change wins over the server's state.
	if err := h.st.FlagMessages(context.Background(), h.acc.ID, []string{m.ID}, nil, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	h.fake.setRead(first, true)
	h.pass(api.FolderID(inbox.ID), false)
	if got, _ := h.fake.get(first); got.isRead {
		t.Fatal("local unread not pushed to the server")
	}
}

func TestServerMoveKeepsLocalIDAndServerDeleteRemoves(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	id := h.fake.add("F-INBOX", "Travelling", "alice@example.test", daysAgo(1), "body")
	other := h.fake.add("F-INBOX", "Doomed", "alice@example.test", daysAgo(1), "body")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	local, _ := h.byRemote(id)
	raw := h.st.MessageRawPath(h.acc.ID, local.ID)

	h.fake.move(id, "F-PROJ")
	h.fake.remove(other)
	h.pass("", false)

	moved, ok := h.byRemote(id)
	if !ok || moved.ID != local.ID || moved.FolderID != h.folder("F-PROJ").ID || moved.BodyState != store.BodyFetched {
		t.Fatalf("after server move: %+v (ok=%v)", moved, ok)
	}
	if _, err := os.Stat(raw); err != nil {
		t.Fatalf("raw file after move: %v", err)
	}
	if _, ok := h.byRemote(other); ok {
		t.Fatal("server-deleted message still stored")
	}
	if inbox := h.folder("inbox"); inbox.Total != 0 {
		t.Fatalf("inbox total after move and delete = %d", inbox.Total)
	}
	if proj := h.folder("F-PROJ"); proj.Total != 1 {
		t.Fatalf("project total = %d", proj.Total)
	}
}

func TestLocalOperationsPushed(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	a := h.fake.add("F-INBOX", "A", "alice@example.test", daysAgo(1), "a")
	b := h.fake.add("F-INBOX", "B", "alice@example.test", daysAgo(1), "b")
	c := h.fake.add("F-INBOX", "C", "alice@example.test", daysAgo(1), "c")
	d := h.fake.add("F-TRASH", "D", "alice@example.test", daysAgo(1), "d")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	ctx := context.Background()
	la, _ := h.byRemote(a)
	lb, _ := h.byRemote(b)
	lc, _ := h.byRemote(c)
	ld, _ := h.byRemote(d)
	proj := h.folder("F-PROJ")
	trash := h.folder("trash")

	if err := h.st.FlagMessages(ctx, h.acc.ID, []string{la.ID}, []api.Flag{api.FlagSeen, api.FlagFlagged}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.st.MoveMessages(ctx, h.acc.ID, []string{lb.ID}, proj.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.st.TrashMessages(ctx, h.acc.ID, []string{lc.ID, ld.ID}, trash.ID); err != nil {
		t.Fatal(err)
	}
	h.pass("", false)

	if m, _ := h.fake.get(a); !m.isRead || !m.flagged {
		t.Fatalf("flags not pushed: %+v", m)
	}
	if m, _ := h.fake.get(b); m.folder != "F-PROJ" {
		t.Fatalf("move not pushed: %+v", m)
	}
	if m, _ := h.fake.get(c); m.folder != "F-TRASH" {
		t.Fatalf("trash move not pushed: %+v", m)
	}
	if _, ok := h.fake.get(d); ok {
		t.Fatal("permanent delete not pushed")
	}
	if n := h.fake.count("POST", "/me/messages/"+d+"/permanentDelete"); n != 1 {
		t.Fatalf("permanentDelete calls = %d", n)
	}
	if ops, _ := h.st.NextOps(ctx, h.acc.ID, time.Now().Add(time.Hour), 10); len(ops) != 0 {
		t.Fatalf("ops left: %+v", ops)
	}
	// The moved row kept its local id and gained nothing new in the target.
	if got, _ := h.byRemote(b); got.ID != lb.ID || got.FolderID != proj.ID {
		t.Fatalf("moved row after sync: %+v", got)
	}
	if msgs := h.messages(proj.ID); len(msgs) != 1 {
		t.Fatalf("project folder has %d rows", len(msgs))
	}

	// permanentDelete unsupported → DELETE; a vanished message → done.
	h.fake.noPermanentDel = true
	e := h.fake.add("F-TRASH", "E", "alice@example.test", daysAgo(1), "e")
	h.pass("", false)
	le, _ := h.byRemote(e)
	if err := h.st.DeleteMessages(ctx, h.acc.ID, []string{le.ID}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.FlagMessages(ctx, h.acc.ID, []string{la.ID}, nil, []api.Flag{api.FlagSeen}); err != nil {
		t.Fatal(err)
	}
	h.fake.remove(a)
	h.pass("", false)
	if _, ok := h.fake.get(e); ok {
		t.Fatal("DELETE fallback not used")
	}
	if ops, _ := h.st.NextOps(ctx, h.acc.ID, time.Now().Add(time.Hour), 10); len(ops) != 0 {
		t.Fatalf("ops on a vanished message not settled: %+v", ops)
	}
}

func TestStaleDeltaCursorResynchronises(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	id := h.fake.add("F-INBOX", "Keep", "alice@example.test", daysAgo(1), "k")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	local, _ := h.byRemote(id)

	h.fake.staleTokens = true
	gone := h.fake.add("F-INBOX", "Later", "alice@example.test", daysAgo(1), "l")
	h.fake.remove(gone)
	h.pass("", false)
	if h.syncer.State().Error != nil {
		t.Fatalf("resync failed: %+v", h.syncer.State())
	}
	if again, ok := h.byRemote(id); !ok || again.ID != local.ID {
		t.Fatalf("row not kept across resync: %+v", again)
	}
	if n := h.fake.count("GET", "/me/mailFolders/F-INBOX/messages/delta"); n < 3 {
		t.Fatalf("delta calls = %d, want the rejected one plus a fresh enumeration", n)
	}
}

func TestRetentionShrinkPrunes(t *testing.T) {
	h := newHarness(t, SyncPrefs{OfflineDays: 30})
	oldish := h.fake.add("F-INBOX", "Oldish", "alice@example.test", daysAgo(20), "o")
	h.fake.add("F-INBOX", "New", "alice@example.test", daysAgo(1), "n")
	start := time.Now()
	h.start()
	h.waitIdle(start)
	if _, ok := h.byRemote(oldish); !ok {
		t.Fatal("message inside the window missing")
	}
	h.mu.Lock()
	h.prefs = SyncPrefs{OfflineDays: 7}
	h.mu.Unlock()
	h.pass("", false)
	if _, ok := h.byRemote(oldish); ok {
		t.Fatal("message outside the shrunk window kept")
	}
	if inbox := h.folder("inbox"); inbox.Total != 1 {
		t.Fatalf("total after shrink = %d", inbox.Total)
	}
}

func TestTokenProblemsAndRecovery(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-INBOX", "Hi", "alice@example.test", daysAgo(1), "hi")
	h.setToken("", api.NewError(api.CodeAuthRequired, "sign in again"))
	h.start()
	h.waitStatus(api.SyncAuthRequired)
	select {
	case n := <-h.notes.authCh:
		if n.Reason != api.CodeAuthRequired || n.AccountID != api.AccountID(h.acc.ID) {
			t.Fatalf("auth notification = %+v", n)
		}
	case <-time.After(waitTimeout):
		t.Fatal("no authRequired notification")
	}
	// Retries keep the state; the notification is not repeated.
	time.Sleep(150 * time.Millisecond)
	if h.notes.authCount() != 1 {
		t.Fatalf("authRequired notified %d times", h.notes.authCount())
	}
	// The user signs in again: the next attempt succeeds.
	start := time.Now()
	h.setToken(h.fake.token, nil)
	h.syncer.Wake()
	h.waitIdle(start)
	if h.syncer.State().Error != nil || len(h.messages(h.folder("inbox").ID)) != 1 {
		t.Fatalf("after recovery: %+v", h.syncer.State())
	}

	// A token the service rejects is fetched again once, then the syncer
	// reports authRequired.
	h.fake.mu.Lock()
	h.fake.unauthorized = 1
	h.fake.mu.Unlock()
	calls := func() int { h.mu.Lock(); defer h.mu.Unlock(); return h.tokens }
	before := calls()
	h.pass("", false)
	if calls() <= before {
		t.Fatal("token not re-fetched after a 401")
	}
	h.setToken("tok-wrong", nil)
	h.syncer.Trigger("", false)
	h.waitStatus(api.SyncAuthRequired)

	// Unavailable (no session bus) is an error, not a sign-in problem.
	h.setToken("", api.NewError(api.CodeUnavailable, "no bus"))
	h.syncer.Wake()
	h.waitStatus(api.SyncError)
}

func TestThrottlingAndTransientBodyFailure(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	id := h.fake.add("F-INBOX", "Slow", "alice@example.test", daysAgo(1), "slow")
	h.fake.mu.Lock()
	h.fake.throttle = 2
	h.fake.failValue[id] = throttleRetries + 1 // more than the client retries itself
	h.fake.mu.Unlock()
	start := time.Now()
	h.start()
	h.waitIdle(start)
	m, ok := h.byRemote(id)
	if !ok || m.BodyState != store.BodyFetched {
		t.Fatalf("after throttling and repeated 503s: %+v (ok=%v)", m, ok)
	}
	if !h.notes.sawStatus(api.SyncOffline) {
		t.Fatal("the exhausted 503 retries should have shown as offline before the next pass")
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	h := newHarness(t, SyncPrefs{})
	h.fake.add("F-INBOX", "One", "alice@example.test", daysAgo(1), "1")
	sv := NewSupervisor(SupervisorDeps{
		Store:    h.st,
		Token:    func(context.Context, string) (string, error) { return h.fake.token, nil },
		Notifier: h.notes,
		Prefs:    func() SyncPrefs { return SyncPrefs{OfflineDays: 30} },
		BaseURL:  h.fake.srv.URL,
	})
	if sv.Trigger(h.acc.ID, "", false) {
		t.Fatal("trigger before Run")
	}
	sv.Start(h.acc) // queued until Run
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sv.Run(ctx) }()
	waitFor(t, "queued account synced", func() bool {
		st, ok := sv.State(h.acc.ID)
		return ok && st.Status == api.SyncIdle && st.LastSync != nil
	})
	if !sv.Trigger(h.acc.ID, "", false) || sv.Trigger("acc_nope", "", false) {
		t.Fatal("trigger routing")
	}
	if len(sv.States()) != 1 {
		t.Fatalf("states = %+v", sv.States())
	}
	sv.Stop(h.acc.ID)
	if _, ok := sv.State(h.acc.ID); ok {
		t.Fatal("state after stop")
	}
	sv.Restart(h.acc)
	waitFor(t, "restarted", func() bool { _, ok := sv.State(h.acc.ID); return ok })
	sv.Reload()
	cancel()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("supervisor did not stop")
	}
}

func TestDeliverAddsBccAndClassifies(t *testing.T) {
	fake := newFakeGraph(t)
	c := NewClient(Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) { return fake.token, nil }})
	raw := "From: me@contoso.invalid\r\nTo: Alice <alice@example.test>, bob@example.test\r\nCc: carol@example.test\r\nSubject: hi\r\n\r\nbody\r\n"
	rcpts := []string{"alice@example.test", "BOB@example.test", "carol@example.test", "dave@example.test", "erin@example.test", "dave@example.test"}
	if err := Deliver(context.Background(), c, "me@contoso.invalid", rcpts, strings.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	sent := string(fake.sent[0])
	fake.mu.Unlock()
	if !strings.Contains(sent, "Subject: hi\r\nBcc: <dave@example.test>,\r\n <erin@example.test>\r\n\r\nbody") {
		t.Fatalf("sent message:\n%s", sent)
	}
	if strings.Count(sent, "Bcc:") != 1 || strings.Contains(sent, "alice@example.test>,\r\n") {
		t.Fatalf("bcc header wrong:\n%s", sent)
	}
	// No blind recipients: the message goes as is.
	if err := Deliver(context.Background(), c, "me@contoso.invalid", rcpts[:3], strings.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	sent = string(fake.sent[1])
	fake.mu.Unlock()
	if sent != raw {
		t.Fatalf("message altered:\n%s", sent)
	}

	// Permanent versus transient outcomes.
	big := strings.Replace(raw, "Subject: hi", "Subject: too big", 1)
	err := Deliver(context.Background(), c, "me@contoso.invalid", nil, strings.NewReader(big), int64(len(big)))
	if se := sendError(t, err); !se.Permanent || se.Err.Code != api.CodeServerError {
		t.Fatalf("size: %+v", se)
	}
	fake.mu.Lock()
	fake.throttle = throttleRetries + 1
	fake.mu.Unlock()
	c2 := NewClient(Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) { return fake.token, nil },
		Sleep: func(context.Context, time.Duration) error { return nil }})
	err = Deliver(context.Background(), c2, "me@contoso.invalid", nil, strings.NewReader(raw), int64(len(raw)))
	if se := sendError(t, err); se.Permanent || se.Err.Code != api.CodeServerTimeout {
		t.Fatalf("throttled: %+v", se)
	}
	fake.mu.Lock()
	fake.unauthorized = 2
	fake.mu.Unlock()
	err = Deliver(context.Background(), c2, "me@contoso.invalid", nil, strings.NewReader(raw), int64(len(raw)))
	if se := sendError(t, err); se.Permanent || se.Err.Code != api.CodeAuthRequired || strings.Contains(se.Err.Message, fake.token) {
		t.Fatalf("unauthorized: %+v", se)
	}
	bad := "no header block at all"
	err = Deliver(context.Background(), c, "me@contoso.invalid", []string{"x@example.test"}, strings.NewReader(bad), int64(len(bad)))
	if se := sendError(t, err); !se.Permanent {
		t.Fatalf("malformed: %+v", se)
	}
}

func sendError(t *testing.T, err error) *smtpSendError {
	t.Helper()
	var se *smtpSendError
	if !errors.As(err, &se) {
		t.Fatalf("expected *smtp.SendError, got %T: %v", err, err)
	}
	return se
}

func TestProbeAndClientErrors(t *testing.T) {
	fake := newFakeGraph(t)
	ctx := context.Background()
	res, err := ProbeWith(ctx, Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) { return fake.token, nil }})
	if err != nil || res.Email != "me@contoso.invalid" || len(res.Capabilities) != 1 || res.Capabilities[0] != "graph" {
		t.Fatalf("probe = %+v, %v", res, err)
	}
	_, err = ProbeWith(ctx, Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) { return "nope", nil }})
	if code(t, err) != api.CodeAuthRequired {
		t.Fatalf("bad token: %v", err)
	}
	_, err = ProbeWith(ctx, Options{BaseURL: "http://127.0.0.1:1", Token: func(context.Context) (string, error) { return "x", nil }})
	if code(t, err) != api.CodeNetworkError {
		t.Fatalf("unreachable: %v", err)
	}
	_, err = ProbeWith(ctx, Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) {
		return "", api.NewError(api.CodeUnavailable, "no bus")
	}})
	if code(t, err) != api.CodeUnavailable {
		t.Fatalf("token source: %v", err)
	}
	// A 401 refreshes the token once, through Invalidate.
	invalidated := 0
	tokens := []string{"stale", fake.token}
	c := NewClient(Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) {
		tok := tokens[0]
		if len(tokens) > 1 {
			tokens = tokens[1:]
		}
		return tok, nil
	}, Invalidate: func() { invalidated++ }})
	var me map[string]any
	if err := c.Get(ctx, "me", &me); err != nil || invalidated != 1 {
		t.Fatalf("refresh after 401: %v (invalidated %d)", err, invalidated)
	}
	// Retry-After beyond the cap gives up at once.
	fake.mu.Lock()
	fake.throttle = 1
	fake.mu.Unlock()
	slow := NewClient(Options{BaseURL: fake.srv.URL, Token: func(context.Context) (string, error) { return fake.token, nil },
		Sleep: func(context.Context, time.Duration) error { t.Fatal("must not sleep"); return nil }})
	fake.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		if fake.throttle > 0 {
			fake.throttle--
			fake.mu.Unlock()
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fake.mu.Unlock()
		fake.handle(w, r)
	})
	err = slow.Get(ctx, "me", &me)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusTooManyRequests || se.RetryAfter != time.Hour {
		t.Fatalf("long retry-after: %v", err)
	}
	if ToAPIError(err).Code != api.CodeServerTimeout {
		t.Fatalf("429 mapping: %v", ToAPIError(err))
	}
}

func code(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	return e.Code
}

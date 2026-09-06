// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const testPassword = "hunter2-secret"

var fixedNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// delivery is one recorded Deliver call.
type delivery struct {
	password string
	from     string
	rcpts    []string
	size     int64
	body     string
}

// fakeDeliver records calls and answers with whatever fn says.
type fakeDeliver struct {
	mu    sync.Mutex
	calls []delivery
	fn    func(ctx context.Context, d delivery) error
}

func (f *fakeDeliver) deliver(ctx context.Context, _ api.ServerConfig, password, from string, rcpts []string, r io.Reader, size int64) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d := delivery{password: password, from: from, rcpts: append([]string(nil), rcpts...), size: size, body: string(body)}
	f.mu.Lock()
	f.calls = append(f.calls, d)
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, d)
}

func (f *fakeDeliver) set(fn func(ctx context.Context, d delivery) error) {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
}

func (f *fakeDeliver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDeliver) last() delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// recorder collects notifications, Changed and Trigger calls.
type recorder struct {
	mu       sync.Mutex
	auth     []api.AuthRequiredNotification
	changed  int
	triggers []string
}

func (r *recorder) NewMessage(api.NewMessageNotification)           {}
func (r *recorder) SyncState(api.SyncStateNotification)             {}
func (r *recorder) AccountsChanged(api.AccountsChangedNotification) {}
func (r *recorder) AuthRequired(n api.AuthRequiredNotification) {
	r.mu.Lock()
	r.auth = append(r.auth, n)
	r.mu.Unlock()
}
func (r *recorder) onChanged(string) {
	r.mu.Lock()
	r.changed++
	r.mu.Unlock()
}
func (r *recorder) onTrigger(id string, f api.FolderID, full bool) bool {
	r.mu.Lock()
	r.triggers = append(r.triggers, fmt.Sprintf("%s:%s:%v", id, f, full))
	r.mu.Unlock()
	return true
}
func (r *recorder) authCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.auth)
}
func (r *recorder) changedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}
func (r *recorder) triggered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.triggers...)
}

// harness is a temp store with one account and the worker's fakes.
type harness struct {
	t       *testing.T
	s       *store.Store
	account store.Account
	deliver *fakeDeliver
	rec     *recorder
	// password is what Password returns; passwordErr wins when set.
	mu          sync.Mutex
	passwordErr error

	authFailed atomic.Int32 // Deps.AuthFailed calls
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := store.Account{Name: "Me", Enabled: true, Config: api.AccountConfig{
		Name: "Me", Email: "me@example.invalid", DisplayName: "Me",
		IMAP: &api.ServerConfig{Host: "127.0.0.1", Port: 143, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword},
		SMTP: &api.ServerConfig{Host: "127.0.0.1", Port: 25, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword},
	}}
	if err := s.AddAccount(context.Background(), &a); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, s: s, account: a, deliver: &fakeDeliver{}, rec: &recorder{}}
}

func (h *harness) password(context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.passwordErr != nil {
		return "", h.passwordErr
	}
	return testPassword, nil
}

func (h *harness) deps() Deps {
	return Deps{
		Store:      h.s,
		Password:   h.password,
		AuthFailed: func() { h.authFailed.Add(1) },
		Notifier:   h.rec,
		Deliver:    h.deliver.deliver,
		Trigger:    h.rec.onTrigger,
		Changed:    h.rec.onChanged,
		Now:        func() time.Time { return fixedNow },
		Backoff:    func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute },
	}
}

// enqueue queues one message and returns its id.
func (h *harness) enqueue(subject string) string {
	h.t.Helper()
	ctx := context.Background()
	d := store.Draft{AccountID: h.account.ID, Subject: subject,
		To:  []api.Address{{Name: "Recipient One", Address: "to@example.invalid"}},
		BCC: []api.Address{{Address: "bcc@example.invalid"}}, TextBody: "hello"}
	if err := h.s.SaveDraft(ctx, &d, nil); err != nil {
		h.t.Fatal(err)
	}
	m, err := h.s.EnqueueOutbox(ctx, store.EnqueueInput{
		DraftID: d.ID, DraftVersion: d.Version,
		Message: store.Message{AccountID: h.account.ID, From: []api.Address{{Address: "me@example.invalid"}},
			To: d.To, BCC: d.BCC, Subject: subject, Date: fixedNow, RFCMessageID: "x@example.invalid"},
		Text:         "hello",
		EnvelopeFrom: "me@example.invalid",
		Recipients:   []string{"to@example.invalid", "bcc@example.invalid"},
		Build: func(w io.Writer) error {
			_, err := io.WriteString(w, "Subject: "+subject+"\r\n\r\nhello\r\n")
			return err
		},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return m.ID
}

// run starts a worker and stops it at the end of the test.
func (h *harness) run() (*Worker, context.CancelFunc) {
	h.t.Helper()
	w := NewWorker(h.account, h.deps())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			h.t.Error("worker did not stop")
		}
	}
	h.t.Cleanup(stop)
	return w, stop
}

func (h *harness) entry(id string) (store.OutboxEntry, error) {
	return h.s.GetOutbox(context.Background(), h.account.ID, id)
}

func (h *harness) gone(id string) bool {
	_, err := h.entry(id)
	return errors.Is(err, store.ErrNotFound)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func sendErr(code api.ErrorCode, permanent bool) error {
	return &smtp.SendError{Err: api.NewError(code, "boom %s", testPassword), Stage: smtp.StageData, Permanent: permanent}
}

func TestWorkerDeliversWithoutSentFolder(t *testing.T) {
	h := newHarness(t)
	id := h.enqueue("one")
	h.run()

	waitFor(t, "message delivered and dropped", func() bool { return h.gone(id) })
	d := h.deliver.last()
	if d.password != testPassword || d.from != "me@example.invalid" || len(d.rcpts) != 2 || d.size != int64(len(d.body)) || d.body == "" {
		t.Fatalf("delivery = %+v", d)
	}
	ctx := context.Background()
	if _, err := h.s.GetMessage(ctx, h.account.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("message row after delivery: %v", err)
	}
	if _, err := h.s.OpenMessageRaw(ctx, h.account.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("raw file after delivery: %v", err)
	}
	for _, addr := range []string{"to@example.invalid", "bcc@example.invalid"} {
		if known, err := h.s.IsKnownSender(ctx, addr); err != nil || !known {
			t.Errorf("known sender %s = %v, %v", addr, known, err)
		}
	}
	senders, _ := h.s.ListKnownSenders(ctx)
	for _, ks := range senders {
		if ks.Source != api.KnownSenderSourceSent {
			t.Errorf("source of %s = %q", ks.Address, ks.Source)
		}
	}
	if h.rec.changedCount() < 3 { // start, sending, done
		t.Errorf("Changed called %d times", h.rec.changedCount())
	}
	if got := h.rec.triggered(); len(got) != 0 {
		t.Errorf("trigger without a sent folder: %v", got)
	}
	if n := h.rec.authCount(); n != 0 {
		t.Errorf("authRequired = %d", n)
	}
}

// A delivery records every recipient, with the display name the draft
// carried, for recipient completion; the sender is not a recipient.
func TestWorkerCollectsRecipients(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueue("one")
	_, cancel := h.run()
	waitFor(t, "first message delivered", func() bool { return h.gone(id) })
	cancel()

	got, err := h.s.SearchCollectedAddresses(ctx, "example.invalid", 10)
	if err != nil {
		t.Fatal(err)
	}
	byAddr := map[string]store.CollectedAddress{}
	for _, c := range got {
		byAddr[c.Address] = c
	}
	if c := byAddr["to@example.invalid"]; c.Name != "Recipient One" || c.Uses != 1 || !c.LastUsed.Equal(fixedNow) {
		t.Errorf("to = %+v", c)
	}
	if c, ok := byAddr["bcc@example.invalid"]; !ok || c.Name != "" || c.Uses != 1 {
		t.Errorf("bcc = %+v (present %v)", c, ok)
	}
	if _, ok := byAddr["me@example.invalid"]; ok {
		t.Error("the sender was collected as a recipient")
	}

	id2 := h.enqueue("two")
	h.run()
	waitFor(t, "second message delivered", func() bool { return h.gone(id2) })
	if got, _ := h.s.SearchCollectedAddresses(ctx, "to@", 10); len(got) != 1 || got[0].Uses != 2 {
		t.Errorf("after the second delivery: %+v", got)
	}
}

func TestWorkerDeliversWithSentFolder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	folders, _, err := h.s.UpsertFolders(ctx, h.account.ID, []store.Folder{
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Selectable: true, Subscribed: true}})
	if err != nil {
		t.Fatal(err)
	}
	sentID := folders[0].ID
	id := h.enqueue("one")
	h.run()

	waitFor(t, "state sent", func() bool { e, err := h.entry(id); return err == nil && e.State == store.OutboxSent })
	e, _ := h.entry(id)
	if e.Attempts != 1 || e.LastErrorCode != 0 || e.LastError != "" || !e.NextAttemptAt.IsZero() {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := h.s.OpenMessageRaw(ctx, h.account.ID, id); err != nil {
		t.Fatalf("raw file must stay for the Sent append: %v", err)
	}
	want := fmt.Sprintf("%s:%s:false", h.account.ID, sentID)
	waitFor(t, "sent folder trigger", func() bool { got := h.rec.triggered(); return len(got) == 1 && got[0] == want })
}

func TestWorkerTransientFailureRetriesWithBackoff(t *testing.T) {
	h := newHarness(t)
	h.deliver.set(func(context.Context, delivery) error { return sendErr(api.CodeNetworkError, false) })
	id := h.enqueue("one")
	w, _ := h.run()

	waitFor(t, "first failure recorded", func() bool { e, err := h.entry(id); return err == nil && e.Attempts == 1 })
	e, _ := h.entry(id)
	if e.State != store.OutboxQueued || e.LastErrorCode != api.CodeNetworkError || !e.NextAttemptAt.Equal(fixedNow.Add(time.Minute)) {
		t.Fatalf("after first failure: %+v", e)
	}
	if e.LastError == "" || containsPassword(e.LastError) {
		t.Fatalf("last error = %q", e.LastError)
	}
	if h.deliver.count() != 1 {
		t.Fatalf("attempts before the retry time = %d", h.deliver.count())
	}

	// Waiting for the backoff is the worker's job; a Wake alone changes
	// nothing, an outbox.retry makes it due.
	w.Wake()
	time.Sleep(30 * time.Millisecond)
	if h.deliver.count() != 1 {
		t.Fatalf("wake retried before the schedule: %d", h.deliver.count())
	}
	if err := h.s.RetryOutbox(context.Background(), h.account.ID, id); err != nil {
		t.Fatal(err)
	}
	w.Wake()
	waitFor(t, "second failure recorded", func() bool { e, err := h.entry(id); return err == nil && e.Attempts == 2 })
	e, _ = h.entry(id)
	if e.State != store.OutboxQueued || !e.NextAttemptAt.Equal(fixedNow.Add(2*time.Minute)) {
		t.Fatalf("after second failure: %+v", e)
	}
}

func TestWorkerPermanentFailure(t *testing.T) {
	h := newHarness(t)
	h.deliver.set(func(context.Context, delivery) error { return sendErr(api.CodeServerError, true) })
	id := h.enqueue("one")
	h.run()

	waitFor(t, "failed", func() bool { e, err := h.entry(id); return err == nil && e.State == store.OutboxFailed })
	e, _ := h.entry(id)
	if e.Attempts != 1 || e.LastErrorCode != api.CodeServerError || !e.NextAttemptAt.IsZero() {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := h.s.OpenMessageRaw(context.Background(), h.account.ID, id); err != nil {
		t.Fatalf("raw file of a failed message must stay: %v", err)
	}
}

func TestWorkerDeadlineIsTransient(t *testing.T) {
	h := newHarness(t)
	// smtp.Deliver reports a deadline of the per-attempt context as
	// cancelled; with the worker still alive that is a timeout.
	h.deliver.set(func(ctx context.Context, _ delivery) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("no per-attempt deadline")
		}
		return &smtp.SendError{Err: api.NewError(api.CodeCancelled, "cancelled"), Stage: smtp.StageData}
	})
	id := h.enqueue("one")
	h.run()

	waitFor(t, "retry scheduled", func() bool { e, err := h.entry(id); return err == nil && e.Attempts == 1 })
	e, _ := h.entry(id)
	if e.State != store.OutboxQueued || e.LastErrorCode != api.CodeServerTimeout || !e.NextAttemptAt.Equal(fixedNow.Add(time.Minute)) {
		t.Fatalf("entry = %+v", e)
	}
}

func TestWorkerAuthFailureDefersQueue(t *testing.T) {
	h := newHarness(t)
	h.deliver.set(func(context.Context, delivery) error { return sendErr(api.CodeAuthFailed, false) })
	first := h.enqueue("one")
	second := h.enqueue("two")
	w, _ := h.run()

	until := fixedNow.Add(authDefer)
	deferred := func(id string) bool {
		e, err := h.entry(id)
		return err == nil && e.State == store.OutboxQueued && e.LastErrorCode == api.CodeAuthFailed && e.NextAttemptAt.Equal(until)
	}
	waitFor(t, "both deferred", func() bool { return deferred(first) && deferred(second) })
	e, _ := h.entry(first)
	if e.Attempts != 1 || containsPassword(e.LastError) {
		t.Fatalf("first = %+v", e)
	}
	if e, _ := h.entry(second); e.Attempts != 0 {
		t.Fatalf("second was attempted: %+v", e)
	}
	time.Sleep(30 * time.Millisecond)
	if h.deliver.count() != 1 {
		t.Fatalf("deliveries = %d, want 1", h.deliver.count())
	}
	if h.authFailed.Load() != 1 {
		t.Fatalf("AuthFailed called %d times", h.authFailed.Load())
	}
	if n := h.rec.authCount(); n != 1 {
		t.Fatalf("authRequired = %d, want 1", n)
	}
	h.rec.mu.Lock()
	n := h.rec.auth[0]
	h.rec.mu.Unlock()
	if n.AccountID != api.AccountID(h.account.ID) || n.Reason != api.CodeAuthFailed || n.Message == "" || containsPassword(n.Message) {
		t.Fatalf("notification = %+v", n)
	}

	// A retry with working credentials delivers both and resets the
	// notification dedupe.
	h.deliver.set(nil)
	for _, id := range []string{first, second} {
		if err := h.s.RetryOutbox(context.Background(), h.account.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	w.Wake()
	waitFor(t, "both delivered", func() bool { return h.gone(first) && h.gone(second) })

	h.deliver.set(func(context.Context, delivery) error { return sendErr(api.CodeAuthFailed, false) })
	third := h.enqueue("three")
	w.Wake()
	waitFor(t, "third deferred", func() bool { return deferred(third) })
	waitFor(t, "second authRequired", func() bool { return h.rec.authCount() == 2 })
}

func TestWorkerMissingPasswordDefers(t *testing.T) {
	h := newHarness(t)
	h.mu.Lock()
	h.passwordErr = api.NewError(api.CodeAuthRequired, "no stored password")
	h.mu.Unlock()
	id := h.enqueue("one")
	h.run()

	waitFor(t, "deferred", func() bool {
		e, err := h.entry(id)
		return err == nil && e.LastErrorCode == api.CodeAuthRequired && e.NextAttemptAt.Equal(fixedNow.Add(authDefer))
	})
	if h.deliver.count() != 0 {
		t.Fatalf("delivered without a password")
	}
	waitFor(t, "authRequired", func() bool { return h.rec.authCount() == 1 })
	h.rec.mu.Lock()
	reason := h.rec.auth[0].Reason
	h.rec.mu.Unlock()
	if reason != api.CodeAuthRequired {
		t.Fatalf("reason = %d", reason)
	}
}

func TestWorkerResetsInterruptedAttempt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueue("one")

	// A delivery cut short by shutdown leaves the row as it is.
	h.deliver.set(func(ctx context.Context, _ delivery) error {
		<-ctx.Done()
		return &smtp.SendError{Err: api.NewError(api.CodeCancelled, "cancelled"), Stage: smtp.StageData}
	})
	_, stop := h.run()
	waitFor(t, "delivery in progress", func() bool { return h.deliver.count() == 1 })
	if e, _ := h.entry(id); e.State != store.OutboxSending {
		t.Fatalf("during delivery: %+v", e)
	}
	stop()
	if e, _ := h.entry(id); e.State != store.OutboxSending || e.Attempts != 0 {
		t.Fatalf("after interrupted attempt: %+v", e)
	}

	// The next worker re-queues it (one attempt counted) and delivers.
	h.deliver.set(nil)
	h.run()
	waitFor(t, "delivered after restart", func() bool { return h.gone(id) })
	if h.deliver.count() != 2 {
		t.Fatalf("deliveries = %d", h.deliver.count())
	}
	if _, err := h.s.GetMessage(ctx, h.account.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("message after delivery: %v", err)
	}
}

func TestWorkerResetMakesDeferredDue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueue("one")
	if _, err := h.s.DeferOutbox(ctx, h.account.ID, fixedNow.Add(authDefer), api.CodeAuthFailed, "old"); err != nil {
		t.Fatal(err)
	}
	h.run()
	waitFor(t, "delivered despite the deferral", func() bool { return h.gone(id) })
}

func TestWorkerWakeIsNonBlocking(t *testing.T) {
	w := NewWorker(store.Account{ID: "acc"}, Deps{})
	for i := 0; i < 10; i++ {
		w.Wake()
	}
}

func TestDefaultBackoff(t *testing.T) {
	within := func(attempt int, want time.Duration) {
		t.Helper()
		got := DefaultBackoff(attempt)
		lo, hi := time.Duration(float64(want)*(1-backoffJitter)), time.Duration(float64(want)*(1+backoffJitter))
		if got < lo || got > hi {
			t.Errorf("DefaultBackoff(%d) = %v, want within %v..%v", attempt, got, lo, hi)
		}
	}
	within(0, time.Minute)
	within(1, time.Minute)
	within(2, 2*time.Minute)
	within(3, 4*time.Minute)
	within(9, backoffMax)
	within(100, backoffMax)
}

func TestSupervisorLifecycle(t *testing.T) {
	h := newHarness(t)
	sv := NewSupervisor(SupervisorDeps{
		Store:    h.s,
		Password: func(ctx context.Context, _ string) (string, error) { return h.password(ctx) },
		Notifier: h.rec,
		Deliver:  h.deliver.deliver,
		Trigger:  h.rec.onTrigger,
		Changed:  h.rec.onChanged,
	})
	if sv.Wake(h.account.ID) {
		t.Fatal("wake before Run")
	}
	first := h.enqueue("one")
	sv.Start(h.account) // queued until Run

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sv.Run(ctx)
	}()
	waitFor(t, "queued account delivered", func() bool { return h.gone(first) })
	if !sv.Wake(h.account.ID) {
		t.Fatal("wake with a running worker returned false")
	}
	if sv.Wake("acc_nope") {
		t.Fatal("wake for an unknown account returned true")
	}

	// Stop cuts a session short and waits; nothing runs afterwards.
	h.deliver.set(func(ctx context.Context, _ delivery) error {
		<-ctx.Done()
		return &smtp.SendError{Err: api.NewError(api.CodeCancelled, "cancelled")}
	})
	second := h.enqueue("two")
	sv.Wake(h.account.ID)
	waitFor(t, "second sending", func() bool { e, err := h.entry(second); return err == nil && e.State == store.OutboxSending })
	stopped := make(chan struct{})
	go func() {
		sv.Stop(h.account.ID)
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if sv.Wake(h.account.ID) {
		t.Fatal("wake after Stop returned true")
	}
	sv.Stop(h.account.ID) // no-op

	// Restart re-queues the interrupted attempt and delivers it.
	h.deliver.set(nil)
	sv.Restart(h.account)
	waitFor(t, "second delivered after restart", func() bool { return h.gone(second) })
	sv.Start(h.account) // no-op while running

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	sv.Start(h.account) // ignored after Run returned
	if sv.Wake(h.account.ID) {
		t.Fatal("wake after Run returned true")
	}
}

func containsPassword(s string) bool {
	for i := 0; i+len(testPassword) <= len(s); i++ {
		if s[i:i+len(testPassword)] == testPassword {
			return true
		}
	}
	return false
}

func TestWorkerFilesSentCopyDropsLocalCopyAndTriggersSent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	folders, _, err := h.s.UpsertFolders(ctx, h.account.ID, []store.Folder{
		{Mailbox: "AAMkSent", Name: "Sent Items", Path: "Sent Items", Role: api.RoleSent, Selectable: true, Subscribed: true}})
	if err != nil {
		t.Fatal(err)
	}
	sentID := folders[0].ID
	id := h.enqueue("one")
	deps := h.deps()
	deps.FilesSentCopy = true
	w := NewWorker(h.account, deps)
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); w.Run(wctx) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "message delivered and dropped", func() bool { return h.gone(id) })
	if _, err := h.s.OpenMessageRaw(ctx, h.account.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("raw file after a server-filed delivery: %v", err)
	}
	want := fmt.Sprintf("%s:%s:false", h.account.ID, sentID)
	waitFor(t, "sent folder trigger", func() bool { got := h.rec.triggered(); return len(got) == 1 && got[0] == want })
}

func TestSupervisorDeliverForPicksPerAccount(t *testing.T) {
	h := newHarness(t)
	var picked []string
	sv := NewSupervisor(SupervisorDeps{
		Store:    h.s,
		Password: func(context.Context, string) (string, error) { return "", nil },
		Notifier: h.rec,
		Deliver: func(context.Context, api.ServerConfig, string, string, []string, io.Reader, int64) error {
			t.Error("shared Deliver used although DeliverFor is set")
			return nil
		},
		DeliverFor: func(a store.Account) DeliverFunc {
			picked = append(picked, a.ID)
			return h.deliver.deliver
		},
		Trigger: h.rec.onTrigger,
		Changed: h.rec.onChanged,
	})
	first := h.enqueue("one")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	sv.Start(h.account)
	waitFor(t, "delivered through the per-account function", func() bool { return h.gone(first) })
	if len(picked) != 1 || picked[0] != h.account.ID {
		t.Fatalf("DeliverFor calls = %v", picked)
	}
}

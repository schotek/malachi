// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeSupervisor records every lifecycle call and serves canned states.
type fakeSupervisor struct {
	mu     sync.Mutex
	calls  []string
	states map[string]api.SyncState
	onStop func(id string) // runs inside Stop, before it is recorded
	ran    chan struct{}   // closed when Run is entered
}

func newFakeSupervisor() *fakeSupervisor {
	return &fakeSupervisor{states: map[string]api.SyncState{}, ran: make(chan struct{})}
}

func (f *fakeSupervisor) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeSupervisor) Run(ctx context.Context) {
	close(f.ran)
	<-ctx.Done()
}
func (f *fakeSupervisor) Start(a store.Account)   { f.record("start:" + a.ID) }
func (f *fakeSupervisor) Restart(a store.Account) { f.record("restart:" + a.ID) }
func (f *fakeSupervisor) Reload()                 { f.record("reload") }
func (f *fakeSupervisor) Stop(id string) {
	if f.onStop != nil {
		f.onStop(id)
	}
	f.record("stop:" + id)
}
func (f *fakeSupervisor) Trigger(id string, folder api.FolderID, full bool) bool {
	f.record(fmt.Sprintf("trigger:%s:%s:%v", id, folder, full))
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.states[id]
	return ok
}
func (f *fakeSupervisor) State(id string) (api.SyncState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[id]
	return st, ok
}
func (f *fakeSupervisor) States() []api.SyncState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.SyncState, 0, len(f.states))
	for _, st := range f.states {
		out = append(out, st)
	}
	return out
}

func (f *fakeSupervisor) setState(id string, st api.SyncState) {
	f.mu.Lock()
	f.states[id] = st
	f.mu.Unlock()
}

func (f *fakeSupervisor) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *fakeSupervisor) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// count returns how many recorded calls start with prefix.
func (f *fakeSupervisor) count(prefix string) int {
	n := 0
	for _, c := range f.recorded() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

var _ SyncSupervisor = (*fakeSupervisor)(nil)

// fakeOutbox records every lifecycle call of the outbox supervisor.
type fakeOutbox struct {
	mu    sync.Mutex
	calls []string
	ran   chan struct{} // closed when Run is entered
}

func newFakeOutbox() *fakeOutbox { return &fakeOutbox{ran: make(chan struct{})} }

func (f *fakeOutbox) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeOutbox) Run(ctx context.Context) {
	close(f.ran)
	<-ctx.Done()
}
func (f *fakeOutbox) Start(a store.Account)   { f.record("start:" + a.ID) }
func (f *fakeOutbox) Stop(id string)          { f.record("stop:" + id) }
func (f *fakeOutbox) Restart(a store.Account) { f.record("restart:" + a.ID) }
func (f *fakeOutbox) Wake(id string) bool {
	f.record("wake:" + id)
	return true
}

func (f *fakeOutbox) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *fakeOutbox) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

var _ OutboxSupervisor = (*fakeOutbox)(nil)

// outboxOf returns the fake installed by newSyncBackend.
func outboxOf(t *testing.T, b *Backend) *fakeOutbox {
	t.Helper()
	o, ok := b.Delivery.(*fakeOutbox)
	if !ok {
		t.Fatalf("Delivery is %T, not the fake", b.Delivery)
	}
	return o
}

// expectCalls checks that both supervisors recorded exactly want since the
// last reset, and resets them.
func expectCalls(t *testing.T, step string, f *fakeSupervisor, o *fakeOutbox, want ...string) {
	t.Helper()
	if got := f.recorded(); !equalStrings(got, want) {
		t.Fatalf("%s: sync supervisor calls = %v, want %v", step, got, want)
	}
	if got := o.recorded(); !equalStrings(got, want) {
		t.Fatalf("%s: outbox supervisor calls = %v, want %v", step, got, want)
	}
	f.reset()
	o.reset()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newSyncBackend is newTestBackend with a memory keyring and fake
// supervisors installed (the outbox one is reachable via outboxOf).
func newSyncBackend(t *testing.T) (*Backend, *fakeSupervisor) {
	t.Helper()
	b := newTestBackend(t, config.Default())
	b.Keyring = newMemKeyring()
	f := newFakeSupervisor()
	b.Supervisor = f
	b.Delivery = newFakeOutbox()
	return b, f
}

// seedAccount adds an enabled account with the given e-mail and returns
// its id.
func seedAccount(t *testing.T, b *Backend, email string) string {
	t.Helper()
	cfg := validConfig()
	cfg.Email = email
	res, err := b.Accounts().Add(context.Background(), api.AccountAddParams{Config: cfg, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(res.AccountID)
}

func TestStartSyncStartsEnabledOnly(t *testing.T) {
	b, f := newSyncBackend(t)
	ctx := context.Background()
	on := seedAccount(t, b, "on@example.invalid")
	off := seedAccount(t, b, "off@example.invalid")
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(off), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	o := outboxOf(t, b)
	f.reset()
	o.reset()

	runCtx, cancel := context.WithCancel(ctx)
	done := b.StartSync(runCtx)
	for _, ran := range []chan struct{}{f.ran, o.ran} {
		select {
		case <-ran:
		case <-time.After(2 * time.Second):
			t.Fatal("Run not called")
		}
	}
	expectCalls(t, "start", f, o, "start:"+on)
	select {
	case <-done:
		t.Fatal("done closed before Run returned")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done not closed after cancel")
	}
}

func TestAccountHooksDriveSupervisor(t *testing.T) {
	b, f := newSyncBackend(t)
	ctx := context.Background()
	acc := b.Accounts()

	o := outboxOf(t, b)

	id := seedAccount(t, b, "me@example.invalid")
	expectCalls(t, "after add", f, o, "start:"+id)

	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(id), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, "after disable", f, o, "stop:"+id)

	// Updating a paused account does not restart anything.
	cfg := validConfig()
	cfg.Name = "Renamed"
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, "update while paused", f, o)

	if _, err := acc.SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(id), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, "after enable", f, o, "start:"+id)

	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: cfg, Credentials: api.Credentials{Password: "new"}}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, "after update", f, o, "restart:"+id)

	// A keyring failure reverts the row and restarts nothing.
	b.Keyring = auth.UnavailableKeyring{}
	if _, err := acc.Update(ctx, api.AccountUpdateParams{AccountID: api.AccountID(id), Config: cfg, Credentials: api.Credentials{Password: "x"}}); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("keyring failure: %v", err)
	}
	expectCalls(t, "update with keyring failure", f, o)
	b.Keyring = newMemKeyring()

	// Remove stops the syncer and the worker while the account still exists.
	stopSawAccount := false
	f.onStop = func(stopID string) {
		_, err := b.store.GetAccount(ctx, stopID)
		stopSawAccount = err == nil
	}
	if _, err := acc.Remove(ctx, api.AccountRemoveParams{AccountID: api.AccountID(id), DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, "after remove", f, o, "stop:"+id)
	if !stopSawAccount {
		t.Fatal("Stop ran after the account row was deleted")
	}
}

func TestConfigSetReloadsSupervisor(t *testing.T) {
	b, f := newSyncBackend(t)
	p := api.Preferences{SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: 7}
	if _, err := b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: p}); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded(); len(got) != 1 || got[0] != "reload" {
		t.Fatalf("calls = %v", got)
	}
	if interval, days := b.SyncPrefs(); interval != 300 || days != 7 {
		t.Fatalf("SyncPrefs = %d, %d", interval, days)
	}
}

func TestSyncStatusMergesLiveAndStoredState(t *testing.T) {
	b, f := newSyncBackend(t)
	ctx := context.Background()
	live := seedAccount(t, b, "live@example.invalid")
	idle := seedAccount(t, b, "idle@example.invalid")
	paused := seedAccount(t, b, "paused@example.invalid")
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(paused), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	f.setState(live, api.SyncState{Status: api.SyncSyncing, FolderID: "f_1", Progress: 40})
	// A stale state for the paused account must not leak through.
	f.setState(paused, api.SyncState{Status: api.SyncSyncing, Progress: 10})

	res, err := b.Sync().Status(ctx, api.SyncStatusParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accounts) != 3 {
		t.Fatalf("accounts = %+v", res.Accounts)
	}
	want := []api.SyncState{
		{AccountID: api.AccountID(live), Status: api.SyncSyncing, FolderID: "f_1", Progress: 40},
		{AccountID: api.AccountID(idle), Status: api.SyncIdle, Progress: -1},
		{AccountID: api.AccountID(paused), Status: api.SyncDisabled, Progress: -1},
	}
	for i, w := range want {
		if got := res.Accounts[i]; got != w {
			t.Errorf("accounts[%d] = %+v, want %+v", i, got, w)
		}
	}

	one, err := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: api.AccountID(live)})
	if err != nil || len(one.Accounts) != 1 || one.Accounts[0] != want[0] {
		t.Fatalf("single = %+v, %v", one, err)
	}
	if _, err := b.Sync().Status(ctx, api.SyncStatusParams{AccountID: "acc_nope"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}

	// account.list reports the same state.
	list, err := b.Accounts().List(ctx, api.AccountListParams{})
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range want {
		if list.Accounts[i].State != w {
			t.Errorf("account.list[%d].state = %+v, want %+v", i, list.Accounts[i].State, w)
		}
	}
}

func TestSyncTriggerValidation(t *testing.T) {
	b, f := newSyncBackend(t)
	ctx := context.Background()
	on := seedAccount(t, b, "on@example.invalid")
	off := seedAccount(t, b, "off@example.invalid")
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(off), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	folders, _, err := b.store.UpsertFolders(ctx, on, []store.Folder{{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Selectable: true, Subscribed: true}})
	if err != nil {
		t.Fatal(err)
	}
	inbox := api.FolderID(folders[0].ID)
	f.reset()

	sync := b.Sync()
	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{FolderID: inbox}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("folder without account: %v", err)
	}
	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{AccountID: "acc_nope"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{AccountID: api.AccountID(on), FolderID: "f_nope"}); errCode(t, err) != api.CodeFolderNotFound {
		t.Fatalf("unknown folder: %v", err)
	}
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("rejected calls reached the supervisor: %v", got)
	}

	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{AccountID: api.AccountID(off), Full: true}); err != nil {
		t.Fatalf("paused account: %v", err)
	}
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("paused account triggered: %v", got)
	}

	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{AccountID: api.AccountID(on), FolderID: inbox}); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded(); len(got) != 1 || got[0] != fmt.Sprintf("trigger:%s:%s:false", on, inbox) {
		t.Fatalf("folder trigger: %v", got)
	}

	f.reset()
	if _, err := sync.Trigger(ctx, api.SyncTriggerParams{Full: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded(); len(got) != 1 || got[0] != "trigger:"+on+"::true" {
		t.Fatalf("trigger all: %v", got)
	}
}

func TestPasswordFor(t *testing.T) {
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	id := seedAccount(t, b, "me@example.invalid")

	if pw, err := b.PasswordFor(ctx, id); err != nil || pw != "pw" {
		t.Fatalf("PasswordFor = %q, %v", pw, err)
	}
	if _, err := b.PasswordFor(ctx, "acc_nope"); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := b.PasswordFor(ctx, ""); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("empty: %v", err)
	}
	b.Keyring = newMemKeyring()
	if _, err := b.PasswordFor(ctx, id); errCode(t, err) != api.CodeAuthRequired {
		t.Fatalf("no secret: %v", err)
	}
	b.Keyring = auth.UnavailableKeyring{}
	if _, err := b.PasswordFor(ctx, id); errCode(t, err) != api.CodeKeyringError {
		t.Fatalf("unavailable keyring: %v", err)
	}
}

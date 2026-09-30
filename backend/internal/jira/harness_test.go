// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// waitTimeout bounds every wait for something a test expects (a state, a
// notification); a passing run returns as soon as it happens.
const waitTimeout = 60 * time.Second

// syncT0 is the harness clock's start: tests put issues days before it.
var syncT0 = time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)

// testClock is the time of the fake site and of the syncer alike.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// recorder collects notifications.
type recorder struct {
	mu      sync.Mutex
	states  []api.SyncState
	news    []api.NewMessageNotification
	auths   []api.AuthRequiredNotification
	changed []api.MessagesChangedNotification
}

func (r *recorder) NewMessage(n api.NewMessageNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.news = append(r.news, n)
}

func (r *recorder) SyncState(n api.SyncStateNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, n.State)
}

func (r *recorder) AuthRequired(n api.AuthRequiredNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auths = append(r.auths, n)
}

func (r *recorder) AccountsChanged(api.AccountsChangedNotification) {}

func (r *recorder) MessagesChanged(n api.MessagesChangedNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changed = append(r.changed, n)
}

func (r *recorder) takeNews() []api.NewMessageNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.news
	r.news = nil
	return out
}

func (r *recorder) takeChanged() []api.MessagesChangedNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.changed
	r.changed = nil
	return out
}

func (r *recorder) authCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.auths)
}

// harness is a fake site, a store with one jira account on it and a
// syncer, all on one clock.
type harness struct {
	t     *testing.T
	f     *jiratest.Server
	st    *store.Store
	acc   store.Account
	clock *testClock
	notes *recorder

	mu       sync.Mutex
	token    string
	tokenErr error
	prefs    SyncPrefs
	stored   []storedCall

	syncer *Syncer
}

type storedCall struct {
	id  string
	pol ingest.Policy
}

func newHarness(t *testing.T, mode jiratest.Mode, edit ...func(*api.JiraConfig)) *harness {
	t.Helper()
	f := jiratest.New(t, mode)
	clock := &testClock{t: syncT0}
	f.Set(func(f *jiratest.Server) { f.Now = clock.now })
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := f.Config()
	cfg.Spaces = cfg.Spaces[:2] // ITSD and WEB; MOB is not selected
	for _, e := range edit {
		e(&cfg)
	}
	acc := store.Account{Name: "Acme Jira", Enabled: true, Config: api.AccountConfig{
		Name: "Acme Jira", Email: jiratest.Login, Kind: api.AccountJira, Jira: &cfg,
	}}
	if err := st.AddAccount(context.Background(), &acc); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, f: f, st: st, acc: acc, clock: clock, notes: &recorder{}, token: jiratest.Token,
		prefs: SyncPrefs{IntervalSeconds: 300}}
	h.newSyncer()
	return h
}

// reconfigure changes the account's configuration and replaces the syncer,
// as account.update does (Restart).
func (h *harness) reconfigure(edit func(*api.JiraConfig)) {
	h.t.Helper()
	cfg := *h.acc.Config.Jira
	edit(&cfg)
	h.acc.Config.Jira = &cfg
	if err := h.st.UpdateAccount(context.Background(), &h.acc); err != nil {
		h.t.Fatal(err)
	}
	h.newSyncer()
}

// newSyncer replaces the syncer with a fresh one over the same store and
// account (a restart).
func (h *harness) newSyncer() {
	h.t.Helper()
	cfg := h.acc.Config.Jira
	c, err := NewClient(Options{
		SiteURL: cfg.SiteURL, Deployment: cfg.Deployment, CloudID: cfg.CloudID, Login: cfg.Login,
		Token: func(context.Context) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.token, h.tokenErr
		},
		HTTP:  h.f.HTTPClient(),
		Sleep: func(context.Context, time.Duration) error { return nil },
		Now:   h.clock.now,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.syncer = NewSyncer(h.acc, Deps{
		Store: h.st, Remote: NewRemote(c), Client: c, Notifier: h.notes,
		Prefs: func() SyncPrefs {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.prefs
		},
		Log: slog.New(slog.DiscardHandler), Now: h.clock.now,
		Stored: func(_ context.Context, id string, pol ingest.Policy) {
			h.mu.Lock()
			h.stored = append(h.stored, storedCall{id, pol})
			h.mu.Unlock()
		},
		Backoff: func(int) time.Duration { return 20 * time.Millisecond },
	})
}

// pass runs one pass of the syncer (keys: issue keys queued).
func (h *harness) pass(full bool, keys ...string) error {
	return h.syncer.cycle(context.Background(), passRequest{full: full}, keys)
}

func (h *harness) mustPass(keys ...string) {
	h.t.Helper()
	if err := h.pass(false, keys...); err != nil {
		h.t.Fatalf("pass: %v", err)
	}
}

// folder returns the account's folder with the mailbox.
func (h *harness) folder(mailbox string) store.Folder {
	h.t.Helper()
	fs, err := h.st.ListFolders(context.Background(), h.acc.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, f := range fs {
		if f.Mailbox == mailbox {
			return f
		}
	}
	h.t.Fatalf("no folder %s", mailbox)
	return store.Folder{}
}

func (h *harness) hasFolder(mailbox string) bool {
	fs, _ := h.st.ListFolders(context.Background(), h.acc.ID)
	for _, f := range fs {
		if f.Mailbox == mailbox {
			return true
		}
	}
	return false
}

// rows lists the messages of a folder (by mailbox), keyed by remote id.
func (h *harness) rows(mailbox string) map[string]store.Message {
	h.t.Helper()
	f := h.folder(mailbox)
	out := map[string]store.Message{}
	cursor := ""
	for {
		items, next, _, err := h.st.ListMessages(context.Background(), h.acc.ID, f.ID, cursor, 500, api.SortDateDesc, api.FilterAll)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, m := range items {
			if _, dup := out[m.RemoteID]; dup {
				h.t.Fatalf("folder %s holds %s twice", mailbox, m.RemoteID)
			}
			out[m.RemoteID] = m
		}
		if next == "" {
			return out
		}
		cursor = next
	}
}

// message returns a stored message with its body text.
func (h *harness) text(id string) string {
	h.t.Helper()
	text, _, _, err := h.st.GetMessageText(context.Background(), h.acc.ID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return text
}

// raw returns a stored message's raw bytes.
func (h *harness) raw(id string) []byte {
	h.t.Helper()
	r, err := h.st.OpenMessageRaw(context.Background(), h.acc.ID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		h.t.Fatal(err)
	}
	return data
}

func (h *harness) issue(id string) (store.Issue, bool) {
	is, err := h.st.GetIssue(context.Background(), h.acc.ID, id)
	if err != nil {
		return store.Issue{}, false
	}
	return is, true
}

func spaceBox(id string) string { return spaceMailboxPrefix + id }

func viewBox(v api.VirtualFolder) string { return viewMailboxPrefix + string(v) }

func seen(m store.Message) bool { return hasFlag(m.Flags, api.FlagSeen) }

func hasFlag(flags []api.Flag, want api.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// days ago on the harness clock.
func (h *harness) ago(d time.Duration) time.Time { return h.clock.now().Add(-d) }

const day = 24 * time.Hour

// at sets the fake's clock for the builder calls that follow and restores
// it after fn.
func (h *harness) at(t time.Time, fn func()) {
	saved := h.clock.now()
	h.clock.set(t)
	fn()
	h.clock.set(saved)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

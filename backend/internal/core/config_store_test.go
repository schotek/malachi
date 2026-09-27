// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// samePreferences compares two preference sets by value, the pointer
// fields by what they point to.
func samePreferences(a, b api.Preferences) bool { return reflect.DeepEqual(a, b) }

// prefString shows a preference set with the pointer fields resolved.
func prefString(p api.Preferences) string {
	show := func(v any, ok bool) string {
		if !ok {
			return "<nil>"
		}
		return fmt.Sprint(v)
	}
	var compress, days any
	if p.CompressStore != nil {
		compress = *p.CompressStore
	}
	if p.AttachmentOfflineDays != nil {
		days = *p.AttachmentOfflineDays
	}
	return fmt.Sprintf("{interval %d, remote %s, offline %d, compress %s, attachments %s}",
		p.SyncIntervalSeconds, p.RemoteContent, p.OfflineDays,
		show(compress, p.CompressStore != nil), show(days, p.AttachmentOfflineDays != nil))
}

// basePrefs is a valid set without the pointer fields, as an older client
// sends it.
func basePrefs() api.Preferences {
	return api.Preferences{SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: 30}
}

func setPrefs(t *testing.T, b *Backend, p api.Preferences) api.Preferences {
	t.Helper()
	res, err := b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: p})
	if err != nil {
		t.Fatalf("config.set %s: %v", prefString(p), err)
	}
	return res.Preferences
}

func getPrefs(t *testing.T, b *Backend) api.Preferences {
	t.Helper()
	res, err := b.Config().Get(context.Background(), api.ConfigGetParams{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Preferences
}

// startProbe runs start before each syncer start it passes on.
type startProbe struct {
	SyncSupervisor
	start func(store.Account)
}

func (p startProbe) Start(a store.Account) {
	p.start(a)
	p.SyncSupervisor.Start(a)
}

// drainKick empties the raw loop's kick channel and reports whether a
// kick was pending.
func drainKick(b *Backend) bool {
	select {
	case <-b.rawKick:
		return true
	default:
		return false
	}
}

// An absent pointer field in config.set leaves the stored value alone; a
// set one replaces it; the result and config.get always carry both.
func TestConfigSetAbsentFieldsUnchanged(t *testing.T) {
	b := newTestBackend(t, config.Default())

	if got := getPrefs(t, b); got.CompressStore == nil || *got.CompressStore || got.AttachmentOfflineDays == nil || *got.AttachmentOfflineDays != 0 {
		t.Fatalf("built-in defaults = %s", prefString(got))
	}

	p := basePrefs()
	p.CompressStore, p.AttachmentOfflineDays = api.Ptr(true), api.Ptr(30)
	want := p
	if got := setPrefs(t, b, p); !samePreferences(got, want) {
		t.Fatalf("set result = %s, want %s", prefString(got), prefString(want))
	}

	// An older client: the three old fields only.
	old := basePrefs()
	old.SyncIntervalSeconds = 600
	want = old
	want.CompressStore, want.AttachmentOfflineDays = api.Ptr(true), api.Ptr(30)
	if got := setPrefs(t, b, old); !samePreferences(got, want) {
		t.Fatalf("set without the new fields = %s, want %s", prefString(got), prefString(want))
	}
	if got := getPrefs(t, b); !samePreferences(got, want) {
		t.Fatalf("get = %s, want %s", prefString(got), prefString(want))
	}

	// One of them only; false and 0 are values, not absence.
	one := basePrefs()
	one.CompressStore = api.Ptr(false)
	want = one
	want.AttachmentOfflineDays = api.Ptr(30)
	if got := setPrefs(t, b, one); !samePreferences(got, want) {
		t.Fatalf("set compressStore only = %s, want %s", prefString(got), prefString(want))
	}
	one = basePrefs()
	one.AttachmentOfflineDays = api.Ptr(0)
	want = one
	want.CompressStore = api.Ptr(false)
	if got := setPrefs(t, b, one); !samePreferences(got, want) {
		t.Fatalf("set attachmentOfflineDays only = %s, want %s", prefString(got), prefString(want))
	}
	for key, v := range map[string]string{prefCompressStore: "false", prefAttachmentOfflineDays: "0"} {
		if got, ok, err := b.store.GetPreference(context.Background(), key); err != nil || !ok || got != v {
			t.Errorf("stored %s = %q %v %v, want %q", key, got, ok, err, v)
		}
	}
}

// The result of config.set is fresh memory: a client's pointers are never
// kept or handed back.
func TestConfigSetResultOwnsItsPointers(t *testing.T) {
	b := newTestBackend(t, config.Default())
	p := basePrefs()
	p.CompressStore, p.AttachmentOfflineDays = api.Ptr(true), api.Ptr(7)
	got := setPrefs(t, b, p)
	if got.CompressStore == p.CompressStore || got.AttachmentOfflineDays == p.AttachmentOfflineDays {
		t.Fatal("the result shares the request's pointers")
	}
	*p.CompressStore, *p.AttachmentOfflineDays = false, 90
	if again := getPrefs(t, b); !*again.CompressStore || *again.AttachmentOfflineDays != 7 {
		t.Fatalf("writing through the request changed the preferences: %s", prefString(again))
	}
}

func TestConfigSetValidatesNewFields(t *testing.T) {
	b := newTestBackend(t, config.Default())
	for _, n := range []int{-2, -100, api.AttachmentOfflineDaysMax + 1} {
		p := basePrefs()
		p.AttachmentOfflineDays = api.Ptr(n)
		if _, err := b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: p}); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("attachmentOfflineDays %d: %v", n, err)
		}
		if err := ValidatePreferences(p); err == nil {
			t.Errorf("ValidatePreferences accepted attachmentOfflineDays %d", n)
		}
	}
	for _, n := range []int{api.AttachmentOfflineNone, 0, 1, 30, api.AttachmentOfflineDaysMax} {
		p := basePrefs()
		p.AttachmentOfflineDays = api.Ptr(n)
		if got := setPrefs(t, b, p); *got.AttachmentOfflineDays != n {
			t.Errorf("attachmentOfflineDays %d stored as %d", n, *got.AttachmentOfflineDays)
		}
	}
	// A refused set stores nothing.
	p := basePrefs()
	p.SyncIntervalSeconds = 900
	p.AttachmentOfflineDays = api.Ptr(-5)
	if _, err := b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: p}); err == nil {
		t.Fatal("invalid set accepted")
	}
	if got := getPrefs(t, b); got.SyncIntervalSeconds == 900 {
		t.Fatal("a refused set stored the interval")
	}
}

// config.set writes all its keys in one transaction: when one write fails,
// none of them is stored.
func TestConfigSetOneTransaction(t *testing.T) {
	b := newTestBackend(t, config.Default())
	ctx := context.Background()
	before := setPrefs(t, b, basePrefs())
	if _, err := b.store.DB().ExecContext(ctx, `CREATE TRIGGER refuse_attachments BEFORE INSERT ON preferences
		WHEN NEW.key = 'attachments.offline_days' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	p := basePrefs()
	p.SyncIntervalSeconds, p.RemoteContent, p.OfflineDays = 3600, api.RemoteAllow, 90
	p.CompressStore, p.AttachmentOfflineDays = api.Ptr(true), api.Ptr(30)
	drainKick(b)
	if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: p}); errCode(t, err) != api.CodeStorageError {
		t.Fatalf("set = %v, want storageError", err)
	}
	if got := getPrefs(t, b); !samePreferences(got, before) {
		t.Fatalf("a failed set stored part of it: %s, want %s", prefString(got), prefString(before))
	}
	if b.store.RawCodec() != store.RawPlain || drainKick(b) {
		t.Fatal("a failed set was applied")
	}
}

// Precedence of the new fields: stored, then the runtime default, then
// off and 0; a stored value that does not parse counts as none.
func TestPreferencePrecedence(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())

	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(30)})
	if got := getPrefs(t, b); !*got.CompressStore || *got.AttachmentOfflineDays != 30 {
		t.Fatalf("runtime defaults = %s", prefString(got))
	}
	if b.attachmentOfflineDays() != 30 {
		t.Fatalf("attachmentOfflineDays() = %d", b.attachmentOfflineDays())
	}

	p := basePrefs()
	p.CompressStore, p.AttachmentOfflineDays = api.Ptr(false), api.Ptr(api.AttachmentOfflineNone)
	setPrefs(t, b, p)
	if got := getPrefs(t, b); *got.CompressStore || *got.AttachmentOfflineDays != api.AttachmentOfflineNone {
		t.Fatalf("stored values lost to the runtime defaults: %s", prefString(got))
	}
	if b.attachmentOfflineDays() != api.AttachmentOfflineNone {
		t.Fatalf("attachmentOfflineDays() = %d", b.attachmentOfflineDays())
	}

	// Garbage in the store (a newer daemon's format, say) falls through.
	for key, v := range map[string]string{prefCompressStore: "maybe", prefAttachmentOfflineDays: "4000"} {
		if err := b.store.SetPreference(ctx, key, v); err != nil {
			t.Fatal(err)
		}
	}
	if got := getPrefs(t, b); !*got.CompressStore || *got.AttachmentOfflineDays != 30 {
		t.Fatalf("unparsable stored values = %s, want the runtime defaults", prefString(got))
	}
	b.SetRuntimeDefaults(RuntimeDefaults{})
	if got := getPrefs(t, b); *got.CompressStore || *got.AttachmentOfflineDays != 0 {
		t.Fatalf("unparsable stored values = %s, want the built-in defaults", prefString(got))
	}
}

// StartSync stores every runtime default that has no preference yet, for
// new and existing stores alike, and never replaces a stored value: a
// daemon started later without the environment keeps what applied.
func TestRuntimeDefaultsMaterialized(t *testing.T) {
	ctx := context.Background()
	b, _ := newSyncBackend(t)

	// An existing store with one of the two chosen already.
	p := basePrefs()
	p.CompressStore = api.Ptr(false)
	setPrefs(t, b, p)

	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(30)})
	runCtx, cancel := context.WithCancel(ctx)
	done := b.StartSync(runCtx)
	cancel()
	<-done

	for key, want := range map[string]string{prefCompressStore: "false", prefAttachmentOfflineDays: "30"} {
		if got, ok, err := b.store.GetPreference(ctx, key); err != nil || !ok || got != want {
			t.Errorf("after start %s = %q %v %v, want %q", key, got, ok, err, want)
		}
	}
	if b.store.RawCodec() != store.RawPlain {
		t.Error("the stored compressStore false lost to the runtime default")
	}

	// A later daemon over the same store, without the environment.
	later := New("test", b.store, config.Default(), nil)
	if got := getPrefs(t, later); *got.CompressStore || *got.AttachmentOfflineDays != 30 {
		t.Fatalf("later daemon = %s", prefString(got))
	}
}

// A fresh store takes both runtime defaults, and StartSync applies the
// codec before any syncer runs.
func TestRuntimeDefaultsAppliedAtStart(t *testing.T) {
	ctx := context.Background()
	b, f := newSyncBackend(t)
	id := seedAccount(t, b, "me@example.invalid")
	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineNone)})

	codecAtStart := make(chan store.RawCodec, 1)
	b.Supervisor = startProbe{SyncSupervisor: f, start: func(store.Account) { codecAtStart <- b.store.RawCodec() }}
	runCtx, cancel := context.WithCancel(ctx)
	done := b.StartSync(runCtx)
	defer func() {
		cancel()
		<-done
	}()
	select {
	case c := <-codecAtStart:
		if c != store.RawZstd {
			t.Fatalf("syncer of %s started with codec %s", id, c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no syncer started")
	}
	if got, ok, _ := b.store.GetPreference(ctx, prefAttachmentOfflineDays); !ok || got != "-1" {
		t.Fatalf("stored attachments.offline_days = %q %v", got, ok)
	}
	b.SetRuntimeDefaults(RuntimeDefaults{})
	if got := getPrefs(t, b); !*got.CompressStore || *got.AttachmentOfflineDays != api.AttachmentOfflineNone {
		t.Fatalf("after start without defaults = %s", prefString(got))
	}
}

// StartSync without any preference keeps the store plain; a stored
// compressStore true makes it zstd.
func TestStartSyncAppliesStoredCodec(t *testing.T) {
	ctx := context.Background()
	b, _ := newSyncBackend(t)
	start := func() {
		b.Supervisor, b.Delivery = newFakeSupervisor(), newFakeOutbox() // one Run each
		runCtx, cancel := context.WithCancel(ctx)
		done := b.StartSync(runCtx)
		cancel()
		<-done
	}
	start()
	if b.store.RawCodec() != store.RawPlain {
		t.Fatalf("codec without preferences = %s", b.store.RawCodec())
	}
	if err := b.store.SetPreference(ctx, prefCompressStore, "true"); err != nil {
		t.Fatal(err)
	}
	start()
	if b.store.RawCodec() != store.RawZstd {
		t.Fatalf("codec with compressStore stored = %s", b.store.RawCodec())
	}
}

// config.set applies what it stored: the codec of new raw files, a reload
// of the syncers and a kick of the raw maintenance loop, which never
// blocks however many sets pile up.
func TestConfigSetAppliesChanges(t *testing.T) {
	b, f := newSyncBackend(t)
	f.reset()
	drainKick(b)

	p := basePrefs()
	p.CompressStore = api.Ptr(true)
	setPrefs(t, b, p)
	if b.store.RawCodec() != store.RawZstd {
		t.Fatalf("codec after compressStore true = %s", b.store.RawCodec())
	}
	if f.count("reload") != 1 {
		t.Fatalf("supervisor calls = %v", f.recorded())
	}
	if !drainKick(b) {
		t.Fatal("no kick of the raw maintenance loop")
	}

	// An older client's set keeps the codec, but still wakes everything.
	setPrefs(t, b, basePrefs())
	if b.store.RawCodec() != store.RawZstd || !drainKick(b) {
		t.Fatal("a set without compressStore changed the codec or did not kick")
	}

	p.CompressStore = api.Ptr(false)
	for range 5 {
		setPrefs(t, b, p)
	}
	if b.store.RawCodec() != store.RawPlain || !drainKick(b) || drainKick(b) {
		t.Fatal("repeated sets: codec or kick wrong")
	}
}

// Concurrent config.set calls leave the codec as the preference stored
// last says.
func TestConfigSetConcurrentCodec(t *testing.T) {
	b, _ := newSyncBackend(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := basePrefs()
			p.CompressStore = api.Ptr(i%2 == 0)
			if _, err := b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: p}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	want := store.RawPlain
	if *getPrefs(t, b).CompressStore {
		want = store.RawZstd
	}
	if got := b.store.RawCodec(); got != want {
		t.Fatalf("codec %s, stored preference says %s", got, want)
	}
}

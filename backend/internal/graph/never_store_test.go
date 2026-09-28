// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
)

// Under NeverStoreAttachments a body keeps no attachment, however small
// and however recent the message, and is received into memory: with no
// staging area to write to, it is still stored.
func TestSyncNeverStoreAttachments(t *testing.T) {
	h := newHarness(t, SyncPrefs{OfflineDays: 30, NeverStoreAttachments: true})
	staging := filepath.Join(filepath.Dir(h.st.Path()), "staging")
	if err := os.RemoveAll(staging); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := h.fake.add("F-INBOX", "Small report", "alice@example.test", daysAgo(1), "body")
	raw := withAttachment("Small report", 2<<10)
	h.fake.setMIME(id, raw)
	start := time.Now()
	h.start()
	h.waitIdle(start)

	m, ok := h.byRemote(id)
	if !ok || m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || m.Size != int64(len(raw)) ||
		len(m.Attachments) != 1 || !m.Attachments[0].Remote || m.RemoteBytes != 2<<10 {
		t.Fatalf("message = %+v (ok=%v)", m, ok)
	}
	if info, err := os.Stat(staging); err != nil || info.IsDir() {
		t.Fatalf("the staging area was touched: %v", err)
	}
}

// valueServed reports whether the fake served a message's MIME ($value).
func (f *fakeGraph) valueServed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if strings.HasPrefix(r, "GET ") && strings.HasSuffix(r, "/$value") {
			return true
		}
	}
	return false
}

// A body is stored under the preferences as they are when the server
// answers for it, not when the folder's bodies start (here
// NeverStoreAttachments is switched on as the server serves it), and the
// daemon is told the policy it was stored under (Deps.Stored).
func TestSyncNeverStoreSwitchedOnDuringPass(t *testing.T) {
	h := newHarness(t, SyncPrefs{OfflineDays: 30})
	h.syncer.deps.Prefs = func() SyncPrefs {
		return SyncPrefs{OfflineDays: 30, NeverStoreAttachments: h.fake.valueServed()}
	}
	var mu sync.Mutex
	stored := map[string][]ingest.Policy{}
	h.syncer.deps.Stored = func(_ context.Context, id string, pol ingest.Policy) {
		mu.Lock()
		stored[id] = append(stored[id], pol)
		mu.Unlock()
	}
	id := h.fake.add("F-INBOX", "Small report", "alice@example.test", daysAgo(1), "body")
	h.fake.setMIME(id, withAttachment("Small report", 2<<10))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	m, ok := h.byRemote(id)
	if !ok || m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || len(m.RemoteParts) != 1 {
		t.Fatalf("message = %+v (ok=%v)", m, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := stored[m.ID]; len(got) != 1 || !got[0].NeverStore {
		t.Fatalf("told %+v", stored)
	}
}

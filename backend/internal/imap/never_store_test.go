// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package imap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
)

// Under NeverStoreAttachments the syncer keeps no attachment, however
// small and however recent the message, and receives every body into
// memory: with no staging area to write to, the bodies are still stored.
func TestSyncNeverStoreAttachments(t *testing.T) {
	h := newHarness(t, harnessOptions{prefs: SyncPrefs{OfflineDays: 90, NeverStoreAttachments: true}})
	staging := filepath.Join(filepath.Dir(h.st.Path()), "staging")
	if err := os.RemoveAll(staging); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := rawWithAttachment("small", "Small report", 2<<10)
	h.append("INBOX", raw, daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	msgs := h.messages(h.folder("inbox").ID)
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	m := msgs[0]
	if m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || len(m.RemoteParts) != 1 || m.RemoteParts[0] != "2" ||
		m.RemoteBytes != 2<<10 || m.Size != int64(len(raw)) || !m.Attachments[0].Remote {
		t.Fatalf("message = %+v", m)
	}
	r, err := h.st.OpenMessageRaw(context.Background(), h.acc.ID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(r)
	r.Close()
	if len(stored) >= len(raw)/2 {
		t.Fatalf("stored %d bytes of %d", len(stored), len(raw))
	}
	if info, err := os.Stat(staging); err != nil || info.IsDir() {
		t.Fatalf("the staging area was touched: %v", err)
	}
}

// flippingSession is a memory server session that calls flip when it is
// asked for whole bodies (UID FETCH BODY.PEEK[]), as a user switching a
// preference while the syncer downloads them.
type flippingSession struct {
	imapserver.Session
	flip func()
}

func (s *flippingSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	for _, sec := range options.BodySection {
		if sec.Specifier == imap.PartSpecifierNone && len(sec.Part) == 0 && len(sec.HeaderFields) == 0 {
			s.flip()
		}
	}
	return s.Session.Fetch(w, numSet, options)
}

// Move keeps the memserver's MOVE reachable (see saslSession).
func (s *flippingSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	return s.Session.(imapserver.SessionMove).Move(w, numSet, dest)
}

// A body is stored under the preferences as they are when it arrives, not
// when the folder's bodies start (here NeverStoreAttachments is switched
// on as the syncer asks for the bodies), and the daemon is told the
// policy it was stored under (Deps.Stored).
func TestSyncNeverStoreSwitchedOnDuringPass(t *testing.T) {
	var h *harness
	h = newHarness(t, harnessOptions{prefs: SyncPrefs{OfflineDays: 90}, session: func(s imapserver.Session) imapserver.Session {
		return &flippingSession{Session: s, flip: func() { h.setPrefs(SyncPrefs{OfflineDays: 90, NeverStoreAttachments: true}) }}
	}})
	var mu sync.Mutex
	stored := map[string][]ingest.Policy{}
	h.syncer.deps.Stored = func(_ context.Context, id string, pol ingest.Policy) {
		mu.Lock()
		stored[id] = append(stored[id], pol)
		mu.Unlock()
	}
	h.append("INBOX", rawWithAttachment("small", "Small report", 2<<10), daysAgo(1))
	start := time.Now()
	h.start()
	h.waitIdle(start)

	msgs := h.messages(h.folder("inbox").ID)
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	m := msgs[0]
	if m.BodyState != store.BodyFetched || m.RawState != store.RawPartial || len(m.RemoteParts) != 1 {
		t.Fatalf("message = %+v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := stored[m.ID]; len(got) != 1 || !got[0].NeverStore {
		t.Fatalf("told %+v", stored)
	}
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// setNeverStore stores Preferences.NeverStoreAttachments, leaving
// attachmentOfflineDays as it is.
func (m *mailbox) setNeverStore(t *testing.T, on bool) {
	t.Helper()
	if _, err := m.b.Config().Set(context.Background(), api.ConfigSetParams{Preferences: api.Preferences{
		SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: 0,
		NeverStoreAttachments: api.Ptr(on),
	}}); err != nil {
		t.Fatal(err)
	}
}

// blockStaging puts a file where the store's staging directory is: from
// then on staging a message on disk fails, so whatever is still received
// was received into memory. stagingBlocked checks that it is still so.
func blockStaging(t *testing.T, st *store.Store) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(st.Path()), "staging")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stagingBlocked(t *testing.T, st *store.Store) {
	t.Helper()
	info, err := os.Stat(filepath.Join(filepath.Dir(st.Path()), "staging"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len("not a directory")) {
		t.Fatalf("the staging area was touched: %v", err)
	}
}

// messageFiles is every file under the store's messages directory, by
// path, with its content.
func messageFiles(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(st.MessageDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// notesMessage is a message whose one attachment is small.
func notesMessage(messageID string) []byte {
	return []byte(strings.Join([]string{
		"From: Alice <alice@example.org>",
		"To: me@example.invalid",
		"Subject: Notes",
		"Date: Mon, 01 Sep 2025 10:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"see the notes",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		`Content-Disposition: attachment; filename="notes.txt"`,
		"",
		"the notes themselves",
		"--mix--",
		"",
	}, "\r\n"))
}

// seedWhole stores raw in the inbox under uid as a fetched, whole message
// whose row says strippable, without the staging area.
func (m *mailbox) seedWhole(t *testing.T, uid uint32, raw []byte, strippable int64) store.Message {
	t.Helper()
	ctx := context.Background()
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().AddDate(-1, 0, 0)
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uid, Subject: parsed.Subject,
		Date: at, InternalDate: at, RFCMessageID: parsed.MessageID, Size: int64(len(raw))}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if err := m.b.store.SetMessageBody(ctx, row.ID, store.BodyUpdate{Text: parsed.Text, HasHTML: parsed.HasHTML,
		Snippet: parsed.Snippet, Attachments: parsed.Attachments, HasAttachments: parsed.HasAttachments,
		Headers: parsed.Headers, State: store.BodyFetched}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.WriteMessageRaw(ctx, string(m.acc), row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		t.Fatal(err)
	}
	if err := m.b.store.SetStrippableBytes(ctx, row.ID, strippable); err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, api.MessageID(row.ID))
	return m.row(t, row.ID)
}

func (m *mailbox) embedded(id, partID string) (*api.MessageEmbeddedResult, error) {
	return m.b.Messages().Embedded(context.Background(), api.MessageEmbeddedParams{AccountID: m.acc,
		MessageID: api.MessageID(id), PartID: partID})
}

// The preference: off by default whatever the environment's defaults of
// the others, left alone when absent, and switching it off drops what
// memory holds and the re-evaluation mark.
func TestNeverStorePreference(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineNone)})
	if got := getPrefs(t, b); got.NeverStoreAttachments == nil || *got.NeverStoreAttachments || b.neverStoreAttachments() {
		t.Fatalf("default = %s", prefString(got))
	}

	p := basePrefs()
	p.NeverStoreAttachments = api.Ptr(true)
	drainKick(b)
	if got := setPrefs(t, b, p); !*got.NeverStoreAttachments || *got.AttachmentOfflineDays != api.AttachmentOfflineNone {
		t.Fatalf("set = %s", prefString(got))
	}
	if !drainKick(b) {
		t.Error("switching it on did not wake the raw maintenance loop")
	}
	if pol := b.attachmentPolicy(); !pol.NeverStore || pol.AttachmentOfflineDays != api.AttachmentOfflineNone {
		t.Errorf("policy %+v", pol)
	}
	if v, ok, err := b.store.GetPreference(ctx, prefNeverStoreAttachments); err != nil || !ok || v != "true" {
		t.Errorf("stored %q %v %v", v, ok, err)
	}
	// An older client leaves it alone.
	if got := setPrefs(t, b, basePrefs()); !*got.NeverStoreAttachments {
		t.Fatalf("absent field changed it: %s", prefString(got))
	}

	b.mem.put(b.mem.generation(), "acc", "m", []byte("held"), nil)
	if err := b.store.SetMeta(ctx, metaReevaluated, "done"); err != nil {
		t.Fatal(err)
	}
	p.NeverStoreAttachments = api.Ptr(false)
	if got := setPrefs(t, b, p); *got.NeverStoreAttachments {
		t.Fatalf("switched off = %s", prefString(got))
	}
	if n, _ := b.mem.stats(); n != 0 {
		t.Error("memory still holds a message after the switch-off")
	}
	if v := metaOf(t, b, metaReevaluated); v != "" {
		t.Errorf("re-evaluation mark %q after the switch-off", v)
	}

	// A stored value that does not parse counts as off.
	if err := b.store.SetPreference(ctx, prefNeverStoreAttachments, "maybe"); err != nil {
		t.Fatal(err)
	}
	if b.neverStoreAttachments() {
		t.Error("an unparsable stored value counts as on")
	}
}

// Under neverStoreAttachments message.download of a stored message writes
// nothing to the store, neither under messages/ nor into the staging area:
// the message is held in memory, its parts stay remote in the result and
// in the row, and message.part, message.embedded and a forward take them
// from memory; a second download answers at once, and once the copy is
// gone the parts are partNotDownloaded again until the next download.
func TestMessageDownloadNeverStore(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	m.setNeverStore(t, true)
	files := messageFiles(t, m.b.store)
	blockStaging(t, m.b.store)

	res, err := m.download(ctx, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.Message.Attachments {
		if a.Remote != (a.PartID == "3" || a.PartID == "4") {
			t.Errorf("attachment %s: remote %v", a.PartID, a.Remote)
		}
	}
	if got := messageFiles(t, m.b.store); !maps.Equal(got, files) {
		t.Fatal("the download wrote under messages/")
	}
	stagingBlocked(t, m.b.store)
	if row := m.row(t, msg.ID); row.RawState != store.RawPartial || !slices.Equal(row.RemoteParts, msg.RemoteParts) ||
		!row.HydratedAt.IsZero() || !row.UpdatedAt.Equal(msg.UpdatedAt) || row.StrippableBytes != msg.StrippableBytes {
		t.Fatalf("row after the download %+v", row)
	}
	if n, size := m.b.mem.stats(); n != 1 || size != int64(len(largeMessage("report@example.org"))) {
		t.Fatalf("held %d messages, %d bytes", n, size)
	}

	pdf, err := m.part(msg.ID, "3")
	if err != nil || !bytes.Equal(pdf.Data, bigPDF) || pdf.PartID != "3" || pdf.Filename != "report.pdf" || pdf.Size != int64(len(bigPDF)) {
		t.Fatalf("part from memory: %+v, %v", pdf, err)
	}
	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("stored part: %v", err)
	}
	if emb, err := m.embedded(msg.ID, "4"); err != nil || emb.Message.Subject != "The attached one" {
		t.Fatalf("attached message from memory: %+v, %v", emb, err)
	}
	fwd := m.create(t, api.ComposeForward, api.MessageID(msg.ID), "Forwarded")
	if len(fwd.Skipped) != 0 || len(fwd.Draft.Attachments) != 4 {
		t.Fatalf("forward: attachments %+v, skipped %+v", fwd.Draft.Attachments, fwd.Skipped)
	}
	for _, a := range fwd.Draft.Attachments {
		if a.Filename == "report.pdf" && a.Size != int64(len(bigPDF)) {
			t.Errorf("forwarded pdf of %d bytes", a.Size)
		}
	}
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.calls()); n != 1 {
		t.Fatalf("a held message was fetched again: %d fetches", n)
	}
	if got := messageFiles(t, m.b.store); !maps.Equal(got, files) {
		t.Fatal("serving from memory wrote under messages/")
	}

	// Gone from memory.
	m.b.mem.clear()
	if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("part after the copy went: %v", err)
	}
	if _, err := m.embedded(msg.ID, "4"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("attached message after the copy went: %v", err)
	}
	if fwd := m.create(t, api.ComposeForward, api.MessageID(msg.ID), "Forwarded"); len(fwd.Skipped) != 2 || !fwd.Skipped[0].Remote {
		t.Fatalf("forward after the copy went: skipped %+v", fwd.Skipped)
	}
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if pdf, err := m.part(msg.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) || len(srv.calls()) != 2 {
		t.Fatalf("part after downloading again: %v, %d fetches", err, len(srv.calls()))
	}
}

// The least recently used message goes first when memory is full, and its
// parts are partNotDownloaded again.
func TestMessageDownloadNeverStoreEvicted(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	raw := largeMessage("report@example.org")
	one := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	two := m.seedLarge(t, 8, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, raw)
	srv.put("INBOX", 8, raw)
	setMemCacheCap(t, int64(len(raw))+10) // room for one
	m.setNeverStore(t, true)

	for _, id := range []string{one.ID, two.ID} {
		if _, err := m.download(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.part(one.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("evicted message's part: %v", err)
	}
	if pdf, err := m.part(two.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) {
		t.Fatalf("held message's part: %v", err)
	}
	if _, err := m.download(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if pdf, err := m.part(one.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) || len(srv.calls()) != 3 {
		t.Fatalf("after downloading it again: %v, %d fetches", err, len(srv.calls()))
	}
	if _, err := m.part(two.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("the other one stayed: %v", err)
	}
}

// A body not downloaded yet is stored without any attachment (whatever its
// size) but the picture its HTML shows, and the whole message is held in
// memory; nothing is staged on disk.
func TestMessageDownloadNeverStorePendingBody(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	m.setNeverStore(t, true)
	raw := largeMessage("report@example.org")
	recent := time.Now().Add(-time.Hour)
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 9, Subject: "Report",
		Date: recent, InternalDate: recent, RFCMessageID: "report@example.org", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	srv.put("INBOX", 9, raw)
	blockStaging(t, m.b.store)

	res, err := m.download(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Size != int64(len(raw)) || len(res.Message.Attachments) != 4 {
		t.Fatalf("result %+v", res.Message)
	}
	for _, a := range res.Message.Attachments {
		if a.Remote != (a.PartID != "1.2") {
			t.Errorf("attachment %s: remote %v", a.PartID, a.Remote)
		}
	}
	got := m.row(t, row.ID)
	if got.BodyState != store.BodyFetched || got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2", "3", "4"}) ||
		!got.HydratedAt.IsZero() {
		t.Fatalf("row %+v", got)
	}
	f, err := m.b.store.OpenMessageRaw(ctx, string(m.acc), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	size, _ := f.Size()
	f.Close()
	if size >= int64(len(raw))/4 {
		t.Errorf("stored %d bytes of a message of %d", size, len(raw))
	}
	stagingBlocked(t, m.b.store)

	if notes, err := m.part(row.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("small attachment from memory: %v", err)
	}
	if pdf, err := m.part(row.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) {
		t.Fatalf("large attachment from memory: %v", err)
	}
	if logo, err := m.part(row.ID, "1.2"); err != nil || !bytes.HasPrefix(logo.Data, []byte("\x89PNG")) {
		t.Fatalf("the picture the HTML shows: %v", err)
	}
	if _, err := m.download(ctx, row.ID); err != nil || len(srv.calls()) != 1 {
		t.Fatalf("download of a held message: %v, %d fetches", err, len(srv.calls()))
	}
}

// What memory holds goes when the preference is switched off, when the
// account is paused or removed, and when the daemon quits.
func TestNeverStoreHeldCopyGoes(t *testing.T) {
	setup := func(t *testing.T) (*mailbox, *fakeServer, store.Message) {
		m := seedMailbox(t)
		srv := m.fakeServer()
		msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
		srv.put("INBOX", 7, largeMessage("report@example.org"))
		m.setNeverStore(t, true)
		if _, err := m.download(context.Background(), msg.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := m.part(msg.ID, "3"); err != nil {
			t.Fatalf("held part: %v", err)
		}
		return m, srv, msg
	}
	held := func(m *mailbox) int {
		n, _ := m.b.mem.stats()
		return n
	}
	ctx := context.Background()

	t.Run("switched off", func(t *testing.T) {
		m, srv, msg := setup(t)
		m.setNeverStore(t, false)
		if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
			t.Fatalf("part after the switch-off: %v", err)
		}
		// Downloaded as without the preference: stored whole.
		if _, err := m.download(ctx, msg.ID); err != nil {
			t.Fatal(err)
		}
		if row := m.row(t, msg.ID); row.RawState != store.RawFull || row.HydratedAt.IsZero() || len(srv.calls()) != 2 || held(m) != 0 {
			t.Fatalf("row %+v, %d fetches, %d held", row, len(srv.calls()), held(m))
		}
	})
	t.Run("account paused", func(t *testing.T) {
		m, _, msg := setup(t)
		for _, on := range []bool{false, true} {
			if _, err := m.b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: m.acc, Enabled: on}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded || held(m) != 0 {
			t.Fatalf("part after the pause: %v", err)
		}
	})
	t.Run("account removed", func(t *testing.T) {
		m, _, _ := setup(t)
		if _, err := m.b.Accounts().Remove(ctx, api.AccountRemoveParams{AccountID: m.acc, DeleteLocalData: true}); err != nil {
			t.Fatal(err)
		}
		if held(m) != 0 {
			t.Fatal("memory holds a removed account's message")
		}
	})
	t.Run("daemon quits", func(t *testing.T) {
		m, _, msg := setup(t)
		m.b.Close()
		if held(m) != 0 || m.b.mem.put(m.b.mem.generation(), string(m.acc), msg.ID, []byte("x"), nil) {
			t.Fatal("memory holds a message after Close")
		}
	})
}

// A switch-off during a download keeps what it fetched out of memory.
func TestNeverStoreSwitchOffDuringDownload(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	m.setNeverStore(t, true)
	srv.gate = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := m.download(context.Background(), msg.ID)
		done <- err
	}()
	eventually(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
	m.setNeverStore(t, false)
	close(srv.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n, _ := m.b.mem.stats(); n != 0 {
		t.Fatal("a download of before the switch-off is held")
	}
}

// A message of the Drafts folder whose attachments are on the server opens
// whole from memory, and so replaces the message.
func TestDraftOpenNeverStore(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	drafts := m.draftsFolder(t)
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	if _, err := m.b.Messages().Move(ctx, api.MessageMoveParams{AccountID: m.acc,
		MessageIDs: []api.MessageID{api.MessageID(msg.ID)}, TargetFolderID: api.FolderID(drafts.ID)}); err != nil {
		t.Fatal(err)
	}
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	m.setNeverStore(t, true)
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawPartial {
		t.Fatalf("the draft was stored: %+v", row)
	}
	res := m.open(t, msg.ID)
	if len(res.Skipped) != 0 || res.Draft.Replaces != api.MessageID(msg.ID) {
		t.Fatalf("draft.open: skipped %+v, replaces %q", res.Skipped, res.Draft.Replaces)
	}
	var names []string
	for _, a := range res.Draft.Attachments {
		names = append(names, a.Filename)
		if a.Filename == "report.pdf" && a.Size != int64(len(bigPDF)) {
			t.Errorf("pdf of %d bytes", a.Size)
		}
	}
	if !slices.Contains(names, "report.pdf") || !slices.Contains(names, "attached.eml") {
		t.Fatalf("attachments %v", names)
	}
}

// neverKey is the attachment step's key under neverStoreAttachments today.
func neverKey() string {
	return strconv.Itoa(ingest.NeverStoreRule) + ":never:" + time.Now().Format(time.DateOnly)
}

// neverRule is what metaReevaluated says once the settled messages were
// judged again under the current rule.
var neverRule = strconv.Itoa(ingest.NeverStoreRule)

// The attachment step under neverStoreAttachments: key "3:never:<date>";
// every stored message loses every attachment its HTML does not show,
// however recent, however recently downloaded, and one settled under the
// size threshold too (judged again once per switch-on); Drafts and a
// message never to be reduced keep theirs. The skeletons are staged in
// memory.
func TestAttachmentStepNeverStore(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	now := time.Now()

	recent := m.seedLarge(t, 7, now.AddDate(0, 0, -2), ingest.Policy{})
	hydrated := m.seedLarge(t, 8, now.AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 8, largeMessage("report@example.org"))
	if _, err := m.download(ctx, hydrated.ID); err != nil {
		t.Fatal(err)
	}
	small := m.seedWhole(t, 9, notesMessage("notes@example.org"), 0)
	never := m.seedWhole(t, 10, notesMessage("never@example.org"), store.StrippableNever)
	drafts := m.draftsFolder(t)
	draft := &store.Message{AccountID: string(m.acc), FolderID: drafts.ID, UID: 3, Subject: "Draft",
		InternalDate: now.AddDate(0, 0, -90), RFCMessageID: "report@example.org", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{draft}); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Store(ctx, m.b.store, ingest.Request{
		Target: ingest.Target{AccountID: string(m.acc), MessageID: draft.ID, Role: api.RoleDrafts, HasServerCopy: true, InternalDate: draft.InternalDate},
		Body:   bytes.NewReader(largeMessage("report@example.org")), Expect: store.RawExpect{BodyState: store.BodyNone},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := m.row(t, hydrated.ID); got.RawState != store.RawFull || got.HydratedAt.IsZero() {
		t.Fatalf("hydrated %+v", got)
	}

	m.setNeverStore(t, true)
	if key, err := step.Key(ctx, now); err != nil || key != "3:never:"+now.Format(time.DateOnly) {
		t.Fatalf("key %q %v", key, err)
	}
	blockStaging(t, m.b.store)
	runStep(t, step)
	for _, c := range []struct {
		name   string
		id     string
		remote []string
	}{
		{"recent", recent.ID, []string{"2", "3", "4"}},
		{"hydrated", hydrated.ID, []string{"2", "3", "4"}},
		{"small", small.ID, []string{"2"}},
		{"never", never.ID, nil},
		{"draft", draft.ID, nil},
	} {
		got := m.row(t, c.id)
		if !slices.Equal(got.RemoteParts, c.remote) || (len(c.remote) > 0) != (got.RawState == store.RawPartial) {
			t.Errorf("%s: %s %v, want %v", c.name, got.RawState, got.RemoteParts, c.remote)
		}
	}
	if got := m.row(t, never.ID); got.StrippableBytes != store.StrippableNever {
		t.Errorf("never: strippable %d", got.StrippableBytes)
	}
	if v := metaOf(t, m.b, metaReevaluated); v != neverRule {
		t.Errorf("re-evaluation mark %q", v)
	}
	if text, _, _, _ := m.b.store.GetMessageText(ctx, string(m.acc), small.ID); text != "see the notes" {
		t.Errorf("small message's text %q", text)
	}
	if n := len(srv.calls()); n != 1 {
		t.Fatalf("the step fetched: %d fetches", n)
	}
	stagingBlocked(t, m.b.store)

	// Settled at 0 after the pass: the mark keeps a later pass of the same
	// switch-on, the next day's too, from judging the settled messages
	// again. (A probe: under the mode's own rule 0 means nothing to leave
	// on the server; a message stored whole under the old rule while the
	// mode is on is marked by its writer, TestNeverStoreStoredUnder.)
	late := m.seedWhole(t, 11, notesMessage("late@example.org"), 0)
	runStep(t, step)
	if got := m.row(t, late.ID); got.RawState != store.RawFull || got.StrippableBytes != 0 {
		t.Fatalf("late message in the same switch-on: %+v", got)
	}
	// The next switch-on does.
	m.setNeverStore(t, false)
	if v := metaOf(t, m.b, metaReevaluated); v != "" {
		t.Fatalf("mark after the switch-off %q", v)
	}
	m.setNeverStore(t, true)
	runStep(t, step)
	if got := m.row(t, late.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) {
		t.Fatalf("late message after the next switch-on: %+v", got)
	}

	// Switching it off brings nothing back.
	m.setNeverStore(t, false)
	runStep(t, step)
	if got := m.row(t, small.ID); got.RawState != store.RawPartial {
		t.Fatalf("switched off: %+v", got)
	}
}

// Through the raw maintenance loop: switching the preference on runs the
// pass to its end, and enabling a paused account again runs it again for
// the account's messages, which the pass passed over.
func TestNeverStoreLoop(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 10*time.Millisecond, time.Hour)
	m := seedMailbox(t)
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(0, 0, -1), ingest.Policy{})
	stepMeta := rawStepMetaPrefix + attachmentStepName
	startRawLoop(t, m.b)
	waitIdle(t, m.b)

	if _, err := m.b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: m.acc, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	m.setNeverStore(t, true)
	eventually(t, "the pass to finish", func() bool { return metaOf(t, m.b, stepMeta) == neverKey()+"|done" })
	if got := m.row(t, msg.ID); got.RawState != store.RawFull {
		t.Fatalf("a paused account's message was reduced: %+v", got)
	}
	if _, err := m.b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: m.acc, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the account's message to be reduced", func() bool { return m.row(t, msg.ID).RawState == store.RawPartial })
	eventually(t, "the pass to finish again", func() bool { return metaOf(t, m.b, stepMeta) == neverKey()+"|done" })
}

// Enabling an account restarts the pass only under neverStoreAttachments,
// where the messages it passed over would otherwise wait for the next
// day's pass; the other policies do not reduce more for it.
func TestNeverStoreRestartOnlyWhenOn(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	stepMeta := rawStepMetaPrefix + attachmentStepName
	enable := func(on bool) {
		t.Helper()
		if _, err := m.b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: m.acc, Enabled: on}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.b.store.SetMeta(ctx, stepMeta, "1:0|done"); err != nil {
		t.Fatal(err)
	}
	enable(false)
	enable(true)
	if v := metaOf(t, m.b, stepMeta); v != "1:0|done" {
		t.Fatalf("progress %q", v)
	}
	m.setNeverStore(t, true)
	if err := m.b.store.SetMeta(ctx, stepMeta, neverKey()+"|done"); err != nil {
		t.Fatal(err)
	}
	enable(false)
	if v := metaOf(t, m.b, stepMeta); v != neverKey()+"|done" {
		t.Fatalf("pausing changed the progress: %q", v)
	}
	drainKick(m.b)
	enable(true)
	if v := metaOf(t, m.b, stepMeta); v != "" || !drainKick(m.b) {
		t.Fatalf("progress %q after enabling", v)
	}
}

// A small part its file lacks although the row calls it stored (a
// skeleton the row does not describe, which neverStoreAttachments makes of
// small parts too) is never served or forwarded empty: it is recorded as
// remote, and taken from the whole copy in memory while there is one.
func TestLostSmallPart(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	raw := largeMessage("report@example.org")
	lose := func(t *testing.T, uid uint32) store.Message {
		t.Helper()
		msg := m.seedLarge(t, uid, time.Now().AddDate(-1, 0, 0), ingest.Policy{})
		var skel bytes.Buffer
		if _, err := mime.Skeleton(bytes.NewReader(raw), &skel, map[string]bool{"2": true}, mime.DefaultLimits()); err != nil {
			t.Fatal(err)
		}
		if _, err := m.b.store.PutMessageRaw(ctx, string(m.acc), msg.ID, store.RawWrite{}, func(w io.Writer) error {
			_, err := w.Write(skel.Bytes())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return m.row(t, msg.ID)
	}
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	// Nothing in memory: partNotDownloaded, and the row learns it.
	msg := lose(t, 7)
	if _, err := m.part(msg.ID, "2"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("lost small part: %v", err)
	}
	if row := m.row(t, msg.ID); row.RawState != store.RawPartial || !slices.Equal(row.RemoteParts, []string{"2"}) {
		t.Fatalf("row %+v", row)
	}

	// A copy in memory serves it, to message.part and to a forward.
	for i, use := range []string{"part", "forward"} {
		msg := lose(t, uint32(8+i))
		m.b.mem.put(m.b.mem.generation(), string(m.acc), msg.ID, raw, parsed.Attachments)
		switch use {
		case "part":
			if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
				t.Fatalf("lost part from memory: %+v %v", notes, err)
			}
		case "forward":
			fwd := m.create(t, api.ComposeForward, api.MessageID(msg.ID), "Forwarded")
			var notes *api.DraftAttachment
			for i, a := range fwd.Draft.Attachments {
				if a.Filename == "notes.txt" {
					notes = &fwd.Draft.Attachments[i]
				}
			}
			if len(fwd.Skipped) != 0 || notes == nil || notes.Size != int64(len("short notes")) {
				t.Fatalf("forward: attachments %+v, skipped %+v", fwd.Draft.Attachments, fwd.Skipped)
			}
		}
		if row := m.row(t, msg.ID); !slices.Equal(row.RemoteParts, []string{"2"}) {
			t.Fatalf("%s: row %+v", use, row)
		}
	}
}

// Under neverStoreAttachments the pass runs every day: a whole message
// that turned up after a pass (moved out of Drafts, passed over while it
// changed) is reduced by the next day's, without the settled messages
// being judged again.
func TestNeverStorePassDaily(t *testing.T) {
	fastRawLoop(t, time.Millisecond, 10*time.Millisecond, time.Hour)
	m := seedMailbox(t)
	ctx := context.Background()
	step := newAttachmentStep(m.b)
	stepMeta := rawStepMetaPrefix + attachmentStepName
	m.setNeverStore(t, true)
	yesterday, err := step.Key(ctx, time.Now().AddDate(0, 0, -1))
	if err != nil {
		t.Fatal(err)
	}
	if today, _ := step.Key(ctx, time.Now()); today == yesterday {
		t.Fatalf("the key %q has no date", today)
	}
	// Yesterday's pass is done and its settled messages judged.
	for key, value := range map[string]string{stepMeta: yesterday + "|done", metaReevaluated: neverRule} {
		if err := m.b.store.SetMeta(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	m.b.takeRawRestart(attachmentStepName) // the switch-on's
	leftover := m.seedLarge(t, 7, time.Now().AddDate(0, 0, -1), ingest.Policy{})
	probe := m.seedWhole(t, 8, notesMessage("probe@example.org"), 0)

	startRawLoop(t, m.b)
	eventually(t, "today's pass", func() bool { return metaOf(t, m.b, stepMeta) == neverKey()+"|done" })
	if got := m.row(t, leftover.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2", "3", "4"}) {
		t.Fatalf("leftover %+v", got)
	}
	if got := m.row(t, probe.ID); got.RawState != store.RawFull || got.StrippableBytes != 0 {
		t.Fatalf("the settled messages were judged again: %+v", got)
	}
}

// Switching the preference either way starts the attachment pass from the
// start, so that off and on again the same day runs a pass although one
// finished for that day's key before; the settled messages are judged
// again then too.
func TestNeverStoreSwitchRestartsPass(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	stepMeta := rawStepMetaPrefix + attachmentStepName
	m.setNeverStore(t, true)
	runStep(t, step)
	if err := m.b.store.SetMeta(ctx, stepMeta, neverKey()+"|done"); err != nil {
		t.Fatal(err)
	}
	// Settled under the size threshold while it was off.
	small := m.seedWhole(t, 9, notesMessage("notes@example.org"), 0)

	for _, on := range []bool{false, true} {
		drainKick(m.b)
		m.setNeverStore(t, on)
		if v := metaOf(t, m.b, stepMeta); v != "" || !drainKick(m.b) {
			t.Fatalf("switched to %v: progress %q", on, v)
		}
		if !m.b.takeRawRestart(attachmentStepName) {
			t.Fatalf("switched to %v: the loop is not told to start over", on)
		}
	}
	// Setting it to what it is already restarts nothing.
	if err := m.b.store.SetMeta(ctx, stepMeta, neverKey()+"|done"); err != nil {
		t.Fatal(err)
	}
	m.setNeverStore(t, true)
	if v := metaOf(t, m.b, stepMeta); v != neverKey()+"|done" || m.b.takeRawRestart(attachmentStepName) {
		t.Fatalf("unchanged: progress %q", v)
	}
	runStep(t, step)
	if got := m.row(t, small.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) {
		t.Fatalf("settled while off: %+v", got)
	}
}

// A message stored under a policy without neverStoreAttachments while it
// is on by then (switched on while the message was being received) is
// judged again: settled at 0 by the size threshold, it is marked not
// evaluated, and the pass starts over; stored under the preference, or
// with it off, nothing happens.
func TestNeverStoreStoredUnder(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	stepMeta := rawStepMetaPrefix + attachmentStepName
	small := m.seedWhole(t, 9, notesMessage("notes@example.org"), 0)
	done := func() {
		t.Helper()
		if err := m.b.store.SetMeta(ctx, stepMeta, neverKey()+"|done"); err != nil {
			t.Fatal(err)
		}
		m.b.takeRawRestart(attachmentStepName)
		drainKick(m.b)
	}
	untouched := func(what string) {
		t.Helper()
		if got := m.row(t, small.ID); got.StrippableBytes != 0 {
			t.Fatalf("%s: strippable %d", what, got.StrippableBytes)
		}
		if v := metaOf(t, m.b, stepMeta); v != neverKey()+"|done" || m.b.takeRawRestart(attachmentStepName) || drainKick(m.b) {
			t.Fatalf("%s: the pass restarted (%q)", what, v)
		}
	}

	done()
	m.b.storedUnder(ctx, small.ID, ingest.Policy{})
	untouched("preference off")

	m.setNeverStore(t, true)
	done()
	m.b.storedUnder(ctx, small.ID, ingest.Policy{NeverStore: true})
	untouched("stored under the preference")

	cctx, cancel := context.WithCancel(ctx)
	cancel() // the writer's context may be over by then
	m.b.storedUnder(cctx, small.ID, ingest.Policy{AttachmentOfflineDays: 30})
	if got := m.row(t, small.ID); got.StrippableBytes != store.StrippableUnknown {
		t.Fatalf("stored under the old rule: strippable %d", got.StrippableBytes)
	}
	if v := metaOf(t, m.b, stepMeta); v != "" || !m.b.takeRawRestart(attachmentStepName) || !drainKick(m.b) {
		t.Fatalf("the pass did not start over: %q", v)
	}
}

// stallReader reads the first chunk of a message, then waits for release
// before it reads on; reading tells that the first chunk was read.
type stallReader struct {
	r       io.Reader
	once    sync.Once
	reading chan struct{}
	release chan struct{}
	first   bool
}

func newStall() *stallReader {
	return &stallReader{reading: make(chan struct{}), release: make(chan struct{})}
}

func (s *stallReader) wrap(r io.Reader) io.Reader { s.r = r; return s }

func (s *stallReader) Read(p []byte) (int, error) {
	if s.first {
		<-s.release
	}
	s.first = true
	n, err := s.r.Read(p[:min(len(p), 64)])
	s.once.Do(func() { close(s.reading) })
	return n, err
}

// message.download takes the attachment preferences as they are when the
// message arrives: switched on while the server was being reached, a
// body not downloaded yet is stored without its attachment and held
// whole; switched on while the message was being received, it is stored
// whole under the old rule, and the attachment pass judges it again even
// when the pass of the switch-on is over.
func TestNeverStoreSwitchOnDuringDownload(t *testing.T) {
	setup := func(t *testing.T) (*mailbox, *fakeServer, store.Message) {
		m := seedMailbox(t)
		srv := m.fakeServer()
		raw := notesMessage("notes@example.org")
		at := time.Now().AddDate(0, 0, -1)
		row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 9, Subject: "Notes",
			Date: at, InternalDate: at, RFCMessageID: "notes@example.org", Size: int64(len(raw))}
		if err := m.b.store.UpsertMessages(context.Background(), []*store.Message{row}); err != nil {
			t.Fatal(err)
		}
		srv.put("INBOX", 9, raw)
		return m, srv, m.row(t, row.ID)
	}
	download := func(m *mailbox, id string) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := m.download(context.Background(), id)
			done <- err
		}()
		return done
	}

	t.Run("before the message arrives", func(t *testing.T) {
		m, srv, msg := setup(t)
		srv.gate = make(chan struct{})
		done := download(m, msg.ID)
		eventually(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
		m.setNeverStore(t, true)
		close(srv.gate)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := m.row(t, msg.ID); got.BodyState != store.BodyFetched || got.RawState != store.RawPartial ||
			!slices.Equal(got.RemoteParts, []string{"2"}) {
			t.Fatalf("row %+v", got)
		}
		if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "the notes themselves" {
			t.Fatalf("held part: %v", err)
		}
	})
	t.Run("while the message is received", func(t *testing.T) {
		m, srv, msg := setup(t)
		step := newAttachmentStep(m.b).(*attachmentStep)
		stall := newStall()
		srv.wrap = stall.wrap
		done := download(m, msg.ID)
		<-stall.reading
		m.setNeverStore(t, true)
		runStep(t, step) // the pass of the switch-on, over before the commit
		m.b.takeRawRestart(attachmentStepName)
		close(stall.release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := m.row(t, msg.ID); got.RawState != store.RawFull || got.StrippableBytes != store.StrippableUnknown {
			t.Fatalf("stored whole, not marked: %+v", got)
		}
		if !m.b.takeRawRestart(attachmentStepName) {
			t.Fatal("the pass does not start over")
		}
		runStep(t, step)
		if got := m.row(t, msg.ID); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2"}) {
			t.Fatalf("after the pass: %+v", got)
		}
	})
}

// A held copy that lacks a part the stored message keeps on the server
// (the server named it anew) is no copy of it: message.download does not
// hold it and says so, fetches again when asked again, and a copy like
// that already held does not count as held.
func TestNeverStoreHeldCopyLacksPart(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	good := largeMessage("report@example.org")
	renamed := bytes.Replace(good, []byte(`filename="report.pdf"`), []byte(`filename="renamed.pdf"`), 1)
	srv.put("INBOX", 7, renamed)
	m.setNeverStore(t, true)

	for i := range 2 {
		if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeServerError {
			t.Fatalf("download %d: %v", i, err)
		}
		if n, _ := m.b.mem.stats(); n != 0 || len(srv.calls()) != i+1 {
			t.Fatalf("download %d: %d held, %d fetches", i, n, len(srv.calls()))
		}
	}

	// Such a copy held already: a download fetches the message again.
	parsed, err := mime.Parse(bytes.NewReader(renamed), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	m.b.mem.put(m.b.mem.generation(), string(m.acc), msg.ID, renamed, parsed.Attachments)
	srv.put("INBOX", 7, good)
	if _, err := m.download(ctx, msg.ID); err != nil || len(srv.calls()) != 3 {
		t.Fatalf("download over an unusable copy: %v, %d fetches", err, len(srv.calls()))
	}
	if pdf, err := m.part(msg.ID, "3"); err != nil || !bytes.Equal(pdf.Data, bigPDF) {
		t.Fatalf("part: %v", err)
	}
}

// Switching the preference on removes the attachments a message reduced
// under attachmentOfflineDays still holds, the small ones: its remote set
// grows, its text stays, the part is partNotDownloaded unless a download
// holds the message, and the pass does not take it up again. A partial
// message settled at 0 while its file holds such an attachment is judged
// again once too.
func TestNeverStorePartialReduced(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	settled := m.seedLarge(t, 8, time.Now().AddDate(-1, 0, 0), smallOnly)
	if err := m.b.store.SetStrippableBytes(ctx, settled.ID, 0); err != nil {
		t.Fatal(err)
	}
	if msg.RawState != store.RawPartial || !slices.Equal(msg.RemoteParts, []string{"3", "4"}) {
		t.Fatalf("seeded %+v", msg)
	}
	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("stored before: %v", err)
	}
	text, _, _, err := m.b.store.GetMessageText(ctx, string(m.acc), msg.ID)
	if err != nil {
		t.Fatal(err)
	}

	m.setNeverStore(t, true)
	blockStaging(t, m.b.store)
	runStep(t, step)
	stagingBlocked(t, m.b.store)
	for _, id := range []string{msg.ID, settled.ID} {
		if got := m.row(t, id); got.RawState != store.RawPartial || !slices.Equal(got.RemoteParts, []string{"2", "3", "4"}) ||
			got.StrippableBytes != 0 {
			t.Fatalf("after the pass: %+v", got)
		}
	}
	if got, _, _, _ := m.b.store.GetMessageText(ctx, string(m.acc), msg.ID); got != text {
		t.Errorf("text %q, was %q", got, text)
	}
	if _, err := m.part(msg.ID, "2"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("the small part after the pass: %v", err)
	}
	if logo, err := m.part(msg.ID, "1.2"); err != nil || !bytes.HasPrefix(logo.Data, []byte("\x89PNG")) {
		t.Fatalf("the picture the HTML shows: %v", err)
	}
	cands, err := m.b.store.ListStripCandidates(ctx, store.StripQuery{Partial: true})
	if err != nil || len(cands) != 0 {
		t.Fatalf("taken up again: %d, %v", len(cands), err)
	}

	srv.put("INBOX", 7, largeMessage("report@example.org"))
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("held part: %v", err)
	}
}

// A message a writer stores partial under attachmentOfflineDays after the
// pass of the switch-on (a sync that read the preferences before) holds
// small attachments: the pass starts over and removes them.
func TestNeverStorePartialStoredLate(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	step := newAttachmentStep(m.b).(*attachmentStep)
	m.setNeverStore(t, true)
	runStep(t, step)
	m.b.takeRawRestart(attachmentStepName)

	late := m.seedLarge(t, 9, time.Now().AddDate(-1, 0, 0), smallOnly) // stored under the old rule
	m.b.storedUnder(ctx, late.ID, smallOnly)
	if !m.b.takeRawRestart(attachmentStepName) {
		t.Fatal("the pass does not start over")
	}
	runStep(t, step)
	if got := m.row(t, late.ID); !slices.Equal(got.RemoteParts, []string{"2", "3", "4"}) || got.StrippableBytes != 0 {
		t.Fatalf("late message %+v", got)
	}
}

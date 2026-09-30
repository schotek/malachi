// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/fsretry"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// base64Lines encodes data in 76-character CRLF lines.
func base64Lines(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var sb strings.Builder
	for len(enc) > 76 {
		sb.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	sb.WriteString(enc)
	return sb.String()
}

// bigPDF is the content of the large attachment of largeMessage.
var bigPDF = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("0123456789abcdef"), 20<<10)...)

// largeMessage is a message the rule can reduce:
//
//	1.1 the HTML body, showing 1.2
//	1.2 a small picture (cid:logo@example.org)
//	2   notes.txt, small
//	3   report.pdf, bigPDF (320 KiB): a candidate
//	4   an attached message of 120 KiB: a candidate
func largeMessage(messageID string) []byte {
	attached := strings.Join([]string{
		"From: Carol <carol@example.net>",
		"Subject: The attached one",
		"Message-ID: <attached@example.net>",
		"Content-Type: text/plain; charset=utf-8",
		"",
		strings.Repeat("attached text line\r\n", 6<<10),
	}, "\r\n")
	return []byte(strings.Join([]string{
		"From: Alice <alice@example.org>",
		"To: me@example.invalid",
		"Subject: Report",
		"Date: Mon, 01 Sep 2025 10:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		`Content-Type: multipart/related; boundary="rel"`,
		"",
		"--rel",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<p>The numbers. <img src="cid:logo@example.org"></p>`,
		"--rel",
		"Content-Type: image/png",
		"Content-ID: <logo@example.org>",
		"Content-Transfer-Encoding: base64",
		"",
		"iVBORw0KGgo=",
		"--rel--",
		"",
		"--mix",
		"Content-Type: text/plain; charset=utf-8",
		`Content-Disposition: attachment; filename="notes.txt"`,
		"",
		"short notes",
		"--mix",
		"Content-Type: application/pdf",
		`Content-Disposition: attachment; filename="report.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64Lines(bigPDF),
		"--mix",
		"Content-Type: message/rfc822",
		`Content-Disposition: attachment; filename="attached.eml"`,
		"",
		attached,
		"--mix--",
		"",
	}, "\r\n"))
}

// seedLarge stores largeMessage in the inbox under uid as the syncer does
// (ingest.Store) under pol, received at received.
func (m *mailbox) seedLarge(t *testing.T, uid uint32, received time.Time, pol ingest.Policy) store.Message {
	t.Helper()
	ctx := context.Background()
	raw := largeMessage("report@example.org")
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uid, Subject: "Report",
		Date: received, InternalDate: received, RFCMessageID: "report@example.org", Size: int64(len(raw))}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Store(ctx, m.b.store, ingest.Request{
		Target: ingest.Target{AccountID: string(m.acc), MessageID: row.ID, Role: api.RoleInbox, HasServerCopy: uid > 0,
			InternalDate: received, Date: received},
		Body: bytes.NewReader(raw), Policy: pol, Now: time.Now(),
		Expect: store.RawExpect{BodyState: store.BodyNone},
	}, nil); err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, api.MessageID(row.ID))
	return m.row(t, row.ID)
}

// row reads a message's row.
func (m *mailbox) row(t *testing.T, id string) store.Message {
	t.Helper()
	got, err := m.b.store.GetMessage(context.Background(), string(m.acc), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var smallOnly = ingest.Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}

// fakeServer stands in for the mail server behind message.download
// (downloadState.fetchRaw): it serves messages by mailbox and UID and
// records every location asked for and how many bytes were read. With gate
// set, a fetch waits for it (or for its context) before it answers; with
// announce set, it announces that size instead of the message's; with wrap
// set, the message is read through what wrap makes of its reader.
type fakeServer struct {
	mu       sync.Mutex
	raw      map[string][]byte
	asked    []store.ServerLocation
	gate     chan struct{}
	err      error
	announce int64
	wrap     func(io.Reader) io.Reader
	read     int64
}

func serverKey(mailbox string, uid uint32) string { return fmt.Sprintf("%s/%d", mailbox, uid) }

func (m *mailbox) fakeServer() *fakeServer {
	s := &fakeServer{raw: map[string][]byte{}}
	m.b.dl.fetchRaw = s.fetch
	return s
}

func (s *fakeServer) put(mailbox string, uid uint32, raw []byte) {
	s.mu.Lock()
	s.raw[serverKey(mailbox, uid)] = raw
	s.mu.Unlock()
}

func (s *fakeServer) fetch(ctx context.Context, _ store.Account, _ store.Message, loc store.ServerLocation, fn func(io.Reader, int64) error) error {
	s.mu.Lock()
	s.asked = append(s.asked, loc)
	gate, err, announce, wrap := s.gate, s.err, s.announce, s.wrap
	raw, ok := s.raw[serverKey(loc.Folder.Mailbox, loc.UID)]
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return api.NewError(api.CodeCancelled, "cancelled")
		}
	}
	switch {
	case err != nil:
		return err
	case !ok:
		return fmt.Errorf("%w: not on the fake server", imap.ErrGone)
	}
	size := int64(len(raw))
	if announce != 0 {
		size = announce
	}
	body := &countingReader{r: bytes.NewReader(raw)}
	var r io.Reader = body
	if wrap != nil {
		r = wrap(body)
	}
	err = fn(r, size)
	s.mu.Lock()
	s.read += body.n
	s.mu.Unlock()
	return err
}

// countingReader counts what is read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// bytesRead is how much of the messages served was read.
func (s *fakeServer) bytesRead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read
}

func (s *fakeServer) calls() []store.ServerLocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.asked)
}

func (m *mailbox) download(ctx context.Context, id string) (*api.MessageDownloadResult, error) {
	return m.b.Messages().Download(ctx, api.MessageDownloadParams{AccountID: m.acc, MessageID: api.MessageID(id)})
}

func (m *mailbox) part(id, partID string) (*api.MessagePartResult, error) {
	return m.b.Messages().Part(context.Background(), api.MessagePartParams{AccountID: m.acc, MessageID: api.MessageID(id), PartID: partID})
}

// A partial message answers partNotDownloaded for its parts on the server
// until message.download fetched it; then every part is there, the
// message says so, and nothing more is fetched.
func TestMessageDownloadMakesWhole(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	if msg.RawState != store.RawPartial || !slices.Equal(msg.RemoteParts, []string{"3", "4"}) {
		t.Fatalf("seeded %+v", msg)
	}
	srv.put("INBOX", 7, largeMessage("report@example.org"))

	if _, err := m.part(msg.ID, "3"); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("remote part before the download: %v", err)
	}
	if notes, err := m.part(msg.ID, "2"); err != nil || string(notes.Data) != "short notes" {
		t.Fatalf("kept part: %v", err)
	}

	res, err := m.download(ctx, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.ID != api.MessageID(msg.ID) || len(res.Message.Attachments) != 4 {
		t.Fatalf("result %+v", res.Message)
	}
	for _, a := range res.Message.Attachments {
		if a.Remote {
			t.Errorf("attachment %s still remote after the download", a.PartID)
		}
	}
	pdf, err := m.part(msg.ID, "3")
	if err != nil || !bytes.Equal(pdf.Data, bigPDF) {
		t.Fatalf("part after the download: %v", err)
	}
	row := m.row(t, msg.ID)
	if row.RawState != store.RawFull || row.HydratedAt.IsZero() || len(row.RemoteParts) != 0 {
		t.Fatalf("row after the download %+v", row)
	}
	if calls := srv.calls(); len(calls) != 1 || calls[0].UID != 7 || calls[0].Folder.Mailbox != "INBOX" || calls[0].Folder.ID != string(m.inbox) {
		t.Fatalf("fetches %+v", calls)
	}

	// Nothing missing: answered at once, without the server.
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.calls()); n != 1 {
		t.Fatalf("idempotent call fetched: %d", n)
	}
}

func TestMessageDownloadWithoutFetch(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()

	// Whole (the seeded HTML message) and failed messages answer at once.
	if res, err := m.download(ctx, string(m.msgs[0])); err != nil || res.Message.ID != m.msgs[0] {
		t.Fatalf("whole message: %+v %v", res, err)
	}
	if err := m.b.store.MarkBodyState(ctx, string(m.msgs[1]), store.BodyFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := m.download(ctx, string(m.msgs[1])); err != nil {
		t.Fatalf("failed message: %v", err)
	}
	// Over the cap: never downloaded.
	if err := m.b.store.MarkBodyState(ctx, string(m.msgs[2]), store.BodyTooBig); err != nil {
		t.Fatal(err)
	}
	_, err := m.download(ctx, string(m.msgs[2]))
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeAttachmentTooBig {
		t.Fatalf("too big: %v", err)
	}
	if data, ok := e.Data.(map[string]int64); !ok || data["limit"] != ingest.MaxMessageBytes || data["size"] != 300 {
		t.Fatalf("too big data: %#v", e.Data)
	}
	if n := len(srv.calls()); n != 0 {
		t.Fatalf("fetched %d times", n)
	}

	for _, c := range []struct {
		name string
		p    api.MessageDownloadParams
		code api.ErrorCode
	}{
		{"missing ids", api.MessageDownloadParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"unknown account", api.MessageDownloadParams{AccountID: "acc_nope", MessageID: m.msgs[0]}, api.CodeAccountNotFound},
		{"unknown message", api.MessageDownloadParams{AccountID: m.acc, MessageID: "m_nope"}, api.CodeMessageNotFound},
	} {
		if _, err := m.b.Messages().Download(ctx, c.p); errCode(t, err) != c.code {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// A paused account downloads nothing.
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	if _, err := m.b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: m.acc, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("paused account: %v", err)
	}
	if n := len(srv.calls()); n != 0 {
		t.Fatalf("paused account fetched %d times", n)
	}
}

// A body not downloaded yet is downloaded whole, whatever the policy.
func TestMessageDownloadPendingBody(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	if _, err := m.b.Config().Set(ctx, api.ConfigSetParams{Preferences: api.Preferences{
		SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: 30,
		AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineNone),
	}}); err != nil {
		t.Fatal(err)
	}
	raw := largeMessage("report@example.org")
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 9, Subject: "Report",
		Date: time.Now().AddDate(-1, 0, 0), InternalDate: time.Now().AddDate(-1, 0, 0), RFCMessageID: "report@example.org", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	srv.put("INBOX", 9, raw)
	res, err := m.download(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Size != int64(len(raw)) || len(res.Message.Attachments) != 4 || res.Message.Attachments[2].Remote {
		t.Fatalf("result %+v", res.Message)
	}
	got := m.row(t, row.ID)
	if got.BodyState != store.BodyFetched || got.RawState != store.RawFull || got.HydratedAt.IsZero() || got.StrippableBytes == 0 {
		t.Fatalf("row %+v", got)
	}

	// An unparsable body is settled as the syncer settles it.
	bad := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 10, Subject: "Bad", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{bad}); err != nil {
		t.Fatal(err)
	}
	srv.put("INBOX", 10, []byte{})
	if _, err := m.download(ctx, bad.ID); errCode(t, err) != api.CodeMalformedMessage {
		t.Fatalf("unparsable: %v", err)
	}
	if got := m.row(t, bad.ID); got.BodyState != store.BodyFailed {
		t.Fatalf("unparsable row %+v", got)
	}
}

// Every call for one message shares one download.
func TestMessageDownloadShared(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	srv.gate = make(chan struct{})

	const callers = 5
	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := m.download(context.Background(), msg.ID)
			errs <- err
		}()
	}
	waitFor(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
	time.Sleep(50 * time.Millisecond) // the other callers join
	close(srv.gate)
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := len(srv.calls()); n != 1 {
		t.Fatalf("%d fetches for one message", n)
	}
}

// A caller that gives up gets cancelled; the download finishes anyway.
func TestMessageDownloadCallerGivesUp(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	srv.gate = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := m.download(ctx, msg.ID)
		errs <- err
	}()
	waitFor(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
	cancel()
	if err := <-errs; errCode(t, err) != api.CodeCancelled {
		t.Fatalf("caller: %v", err)
	}
	close(srv.gate)
	waitFor(t, "the download to finish", func() bool { return m.row(t, msg.ID).RawState == store.RawFull })
}

func TestMessageDownloadFailures(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)

	// Gone from the server: messageGone, and a pass of its folder.
	m.f.reset()
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeMessageGone {
		t.Fatalf("gone: %v", err)
	}
	if calls := m.f.recorded(); !slices.Contains(calls, fmt.Sprintf("trigger:%s:%s:false", m.acc, m.inbox)) {
		t.Fatalf("no pass of the folder: %v", calls)
	}

	// Another message under that UID: refused, nothing stored.
	srv.put("INBOX", 7, largeMessage("someone-else@example.org"))
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeServerError {
		t.Fatalf("mismatch: %v", err)
	}
	if got := m.row(t, msg.ID); got.RawState != store.RawPartial {
		t.Fatalf("row after a mismatch %+v", got)
	}

	// The server's own errors pass through.
	srv.err = api.NewError(api.CodeAuthFailed, "authentication rejected")
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeAuthFailed {
		t.Fatalf("auth: %v", err)
	}
	srv.err = nil

	// Out of time: serverTimeout, within the budget.
	defer func(d time.Duration) { downloadBudget = d }(downloadBudget)
	downloadBudget = 50 * time.Millisecond
	srv.gate = make(chan struct{})
	defer close(srv.gate)
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeServerTimeout {
		t.Fatalf("timeout: %v", err)
	}
}

// A download whose commit a reader of the stored file holds up past the
// store's retries (Windows refuses to replace an open file) is
// unavailable, to be tried again, as one the syncer raced is: the row and
// the file stay partial, and the next call goes through. Elsewhere the
// reader holds nothing up, and the first call goes through.
func TestMessageDownloadWhileTheFileIsRead(t *testing.T) {
	saved := fsretry.Waits
	fsretry.Waits = []time.Duration{time.Millisecond}
	t.Cleanup(func() { fsretry.Waits = saved })
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))

	r, err := m.b.store.OpenMessageRaw(ctx, string(m.acc), msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.download(ctx, msg.ID)
	r.Close()
	if err != nil {
		if code := errCode(t, err); code != api.CodeUnavailable {
			t.Fatalf("download while the file is read: %v", err)
		}
		if got := m.row(t, msg.ID); got.RawState != store.RawPartial {
			t.Fatalf("row after the refused download %+v", got)
		}
		if _, err := m.download(ctx, msg.ID); err != nil {
			t.Fatalf("download once the reader is done: %v", err)
		}
	}
	if got := m.row(t, msg.ID); got.RawState != store.RawFull {
		t.Fatalf("row after the download %+v", got)
	}

	// The answer itself, on every system.
	a, err := m.b.store.GetAccount(ctx, string(m.acc))
	if err != nil {
		t.Fatal(err)
	}
	busy := fmt.Errorf("ingest: finalise message file: %w", store.ErrBusy)
	if err := m.b.downloadError(ctx, a, m.row(t, msg.ID), store.ServerLocation{}, busy); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("busy: %v", err)
	}
}

// A message the server announces over the cap is refused before a byte of
// it is read: attachmentTooBig, and a body still pending is marked tooBig,
// as the syncer marks one. One announced within the cap downloads.
func TestMessageDownloadAnnouncedTooBig(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	srv.announce = ingest.MaxMessageBytes + 1
	if _, err := m.download(ctx, msg.ID); errCode(t, err) != api.CodeAttachmentTooBig {
		t.Fatalf("announced over the cap: %v", err)
	}
	if got := m.row(t, msg.ID); got.BodyState != store.BodyFetched || got.RawState != store.RawPartial {
		t.Fatalf("row after the refusal %+v", got)
	}

	pending := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: 9, Subject: "Big", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{pending}); err != nil {
		t.Fatal(err)
	}
	srv.put("INBOX", 9, largeMessage("big@example.org"))
	if _, err := m.download(ctx, pending.ID); errCode(t, err) != api.CodeAttachmentTooBig {
		t.Fatalf("pending body announced over the cap: %v", err)
	}
	if got := m.row(t, pending.ID); got.BodyState != store.BodyTooBig {
		t.Fatalf("pending row after the refusal %+v", got)
	}
	if n := srv.bytesRead(); n != 0 {
		t.Fatalf("%d bytes read of messages announced over the cap", n)
	}

	srv.announce = 0
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.row(t, msg.ID); got.RawState != store.RawFull {
		t.Fatalf("row after the download %+v", got)
	}
}

// A message whose local move has not reached the server is fetched where
// the server still has it; one with no place on the server yet is
// unavailable, and a pass is asked for.
func TestMessageDownloadLocation(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	ctx := context.Background()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	if _, err := m.b.Messages().Move(ctx, api.MessageMoveParams{AccountID: m.acc, MessageIDs: []api.MessageID{api.MessageID(msg.ID)}, TargetFolderID: m.trash}); err != nil {
		t.Fatal(err)
	}
	if moved := m.row(t, msg.ID); moved.UID != 0 || moved.FolderID != string(m.trash) {
		t.Fatalf("moved row %+v", moved)
	}
	if _, err := m.download(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	if calls := srv.calls(); len(calls) != 1 || calls[0].UID != 7 || calls[0].Folder.Mailbox != "INBOX" {
		t.Fatalf("fetched %+v", calls)
	}

	nowhere := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), Subject: "Nowhere", Size: 1}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{nowhere}); err != nil {
		t.Fatal(err)
	}
	m.f.reset()
	if _, err := m.download(ctx, nowhere.ID); errCode(t, err) != api.CodeUnavailable {
		t.Fatalf("no place on the server: %v", err)
	}
	if calls := m.f.recorded(); !slices.Contains(calls, fmt.Sprintf("trigger:%s::false", m.acc)) {
		t.Fatalf("no pass asked for: %v", calls)
	}
}

// Removing the account stops its downloads first: nothing is left behind.
func TestMessageDownloadAccountRemoved(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	srv.gate = make(chan struct{})
	defer close(srv.gate)

	errs := make(chan error, 1)
	go func() {
		_, err := m.download(context.Background(), msg.ID)
		errs <- err
	}()
	waitFor(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
	if _, err := m.b.Accounts().Remove(context.Background(), api.AccountRemoveParams{AccountID: m.acc, DeleteLocalData: true}); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err == nil {
		t.Fatal("the download of a removed account succeeded")
	}
	if _, err := os.Stat(filepath.Join(m.b.store.MessageDir(), string(m.acc))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("account directory: %v", err)
	}
	staged, _ := os.ReadDir(filepath.Join(filepath.Dir(m.b.store.Path()), "staging"))
	if len(staged) != 0 {
		t.Fatalf("%d staged files left", len(staged))
	}
}

// Shutdown cancels the downloads and refuses new ones.
func TestMessageDownloadShutdown(t *testing.T) {
	m := seedMailbox(t)
	srv := m.fakeServer()
	msg := m.seedLarge(t, 7, time.Now().AddDate(-1, 0, 0), smallOnly)
	srv.put("INBOX", 7, largeMessage("report@example.org"))
	srv.gate = make(chan struct{})
	defer close(srv.gate)

	errs := make(chan error, 1)
	go func() {
		_, err := m.download(context.Background(), msg.ID)
		errs <- err
	}()
	waitFor(t, "the download to start", func() bool { return len(srv.calls()) == 1 })
	m.b.Close()
	if err := <-errs; errCode(t, err) != api.CodeCancelled {
		t.Fatalf("download at shutdown: %v", err)
	}
	if _, err := m.download(context.Background(), msg.ID); errCode(t, err) != api.CodeCancelled {
		t.Fatalf("download after shutdown: %v", err)
	}
}

// message.download on a jira account builds the item again from the site
// (docs/api.md §4.3): a description whose large file stayed on the site
// comes back whole with the stored Message-ID; an item the site no longer
// has is messageGone, and the pass that call asks for removes it.
func TestDownloadJiraRebuild(t *testing.T) {
	for _, mode := range []jiratest.Mode{jiratest.Cloud, jiratest.DC} {
		t.Run(string(mode), func(t *testing.T) {
			var typo *jiratest.Issue
			prefs := &api.Preferences{RemoteContent: api.RemoteBlock, AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineNone)}
			e := startJiraE2E(t, mode, prefs, func(f *jiratest.Server) {
				typo = f.AddIssue("WEB", "Landing page typo", func(is *jiratest.Issue) { is.Description = "<p>See the report.</p>" })
				f.AddAttachment(typo.ID, "report.pdf", "application/pdf", bigPDF)
			})
			ctx, b, id := e.ctx, e.b, e.id
			_, byName := e.folders()
			desc := item(t, e.list(byName["Web"].ID), typo.Key, api.IssueItemDescription)
			got, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: desc.ID})
			if err != nil {
				t.Fatal(err)
			}
			var pdf api.Attachment
			for _, a := range got.Message.Attachments {
				if a.Filename == "report.pdf" {
					pdf = a
				}
			}
			if !pdf.Remote {
				t.Fatalf("the file is not left on the site: %+v", got.Message.Attachments)
			}
			if _, err := b.Messages().Part(ctx, api.MessagePartParams{AccountID: id, MessageID: desc.ID, PartID: pdf.PartID}); errCode(t, err) != api.CodePartNotDownloaded {
				t.Fatalf("part before the download: %v", err)
			}
			res, err := b.Messages().Download(ctx, api.MessageDownloadParams{AccountID: id, MessageID: desc.ID})
			if err != nil {
				t.Fatal(err)
			}
			if res.Message.Issue == nil || res.Message.Issue.Key != typo.Key || res.Message.RFCMessageID != got.Message.RFCMessageID {
				t.Fatalf("downloaded = %+v", res.Message)
			}
			for _, a := range res.Message.Attachments {
				if a.Remote {
					t.Fatalf("still remote after the download: %+v", a)
				}
			}
			part, err := b.Messages().Part(ctx, api.MessagePartParams{AccountID: id, MessageID: desc.ID, PartID: pdf.PartID})
			if err != nil || !bytes.Equal(part.Data, bigPDF) {
				t.Fatalf("part after the download: %d bytes, %v", len(part.Data), err)
			}

			// The site deletes the issue: the copy in the Open view, still
			// partial, is gone on download, then from the store.
			open := item(t, e.list(byName["Open"].ID), typo.Key, api.IssueItemDescription)
			e.f.DeleteIssue(typo.ID)
			if _, err := b.Messages().Download(ctx, api.MessageDownloadParams{AccountID: id, MessageID: open.ID}); errCode(t, err) != api.CodeMessageGone {
				t.Fatalf("download of a deleted issue: %v", err)
			}
			waitUntil(t, ctx, "the deleted issue's messages removed", func() bool {
				_, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: desc.ID})
				return err != nil && errCode(t, err) == api.CodeMessageNotFound
			})
		})
	}
}

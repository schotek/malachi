// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Everything that reads a stored message reads a compressed one the same:
// the answers before and after the conversion are equal.

// compressAll switches the store to zstd and converts every raw file
// through the codec step, as the maintenance loop does after
// compressStore is switched on; ids must then be stored compressed only.
func compressAll(t *testing.T, b *Backend, acc api.AccountID, ids ...api.MessageID) {
	t.Helper()
	ctx := context.Background()
	b.store.SetRawCodec(store.RawZstd)
	step := newCodecStep(b)
	for cursor := ""; ; {
		next, err := step.Batch(ctx, cursor)
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	for _, id := range ids {
		if !storedAs(t, b, acc, id, store.RawZstd) {
			t.Fatalf("%s is not stored compressed", id)
		}
	}
}

// storedAs reports whether the message's only raw file is in codec c.
func storedAs(t *testing.T, b *Backend, acc api.AccountID, id api.MessageID, c store.RawCodec) bool {
	t.Helper()
	path := b.store.MessageRawPath(string(acc), string(id))
	_, plainErr := os.Stat(path)
	_, zstErr := os.Stat(path + store.RawZstSuffix)
	for _, err := range []error{plainErr, zstErr} {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if c == store.RawZstd {
		return zstErr == nil && plainErr != nil
	}
	return plainErr == nil && zstErr != nil
}

// anyCID matches the cid: references of a created draft, whose ids are
// new for every draft.
var anyCID = regexp.MustCompile(`cid:[^"'\s>]+`)

// sameDraftFrom checks that two drafts created or opened from the same
// message are equal but for the ids of their attachment copies.
func sameDraftFrom(t *testing.T, b *Backend, before, after api.Draft) {
	t.Helper()
	ctx := context.Background()
	if !reflect.DeepEqual([]any{before.To, before.CC, before.BCC, before.Subject, before.InReplyTo, before.Forwarding, before.Replaces, before.TextBody},
		[]any{after.To, after.CC, after.BCC, after.Subject, after.InReplyTo, after.Forwarding, after.Replaces, after.TextBody}) {
		t.Errorf("header or text differ:\n before %+v\n after  %+v", before, after)
	}
	if anyCID.ReplaceAllString(before.HTMLBody, "cid:*") != anyCID.ReplaceAllString(after.HTMLBody, "cid:*") {
		t.Errorf("html differs:\n before %q\n after  %q", before.HTMLBody, after.HTMLBody)
	}
	if len(before.Attachments) != len(after.Attachments) {
		t.Fatalf("attachments: %d before, %d after", len(before.Attachments), len(after.Attachments))
	}
	for i, x := range before.Attachments {
		y := after.Attachments[i]
		if x.Filename != y.Filename || x.ContentType != y.ContentType || x.Size != y.Size || x.Inline != y.Inline {
			t.Errorf("attachment %d: %+v before, %+v after", i, x, y)
		}
		var data [2][]byte
		for j, id := range []string{x.ID, y.ID} {
			got, err := b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: before.AccountID, AttachmentID: id})
			if err != nil {
				t.Fatalf("attachment.get %s: %v", id, err)
			}
			data[j] = got.Data
		}
		if !bytes.Equal(data[0], data[1]) || len(data[0]) == 0 {
			t.Errorf("attachment %d (%s): the copies differ", i, x.Filename)
		}
	}
}

// message.body and message.part.
func TestCompressedMessageBodyAndPart(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()
	id := m.msgs[0]
	read := func() (*api.MessageBodyResult, []*api.MessagePartResult) {
		body, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: id})
		if err != nil {
			t.Fatal(err)
		}
		var parts []*api.MessagePartResult
		for _, part := range []string{"1.2", "2"} {
			p, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: id, PartID: part})
			if err != nil {
				t.Fatalf("part %s: %v", part, err)
			}
			parts = append(parts, p)
		}
		return body, parts
	}
	body, parts := read()
	if !body.HasHTML || body.HTML == "" || body.HTMLWithheld || len(parts[0].Data) == 0 {
		t.Fatalf("plain: body %+v", body)
	}
	compressAll(t, m.b, m.acc, id)
	zbody, zparts := read()
	if !reflect.DeepEqual(body, zbody) {
		t.Errorf("message.body differs:\n plain %+v\n zstd  %+v", body, zbody)
	}
	if !reflect.DeepEqual(parts, zparts) {
		t.Errorf("message.part differs")
	}
	if _, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: id, PartID: "9"}); errCode(t, err) != api.CodePartNotFound {
		t.Errorf("missing part: %v", err)
	}
}

// message.embedded.
func TestCompressedMessageEmbedded(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	id := m.seedRaw(t, "attached-html-message.eml")
	read := func() []*api.MessageEmbeddedResult {
		var out []*api.MessageEmbeddedResult
		for _, part := range []string{"2", "4"} {
			res, err := m.b.Messages().Embedded(ctx, api.MessageEmbeddedParams{AccountID: m.acc, MessageID: id, PartID: part})
			if err != nil {
				t.Fatalf("embedded %s: %v", part, err)
			}
			out = append(out, res)
		}
		return out
	}
	plain := read()
	compressAll(t, m.b, m.acc, id)
	if zst := read(); !reflect.DeepEqual(plain, zst) {
		t.Errorf("message.embedded differs:\n plain %+v\n zstd  %+v", plain[0], zst[0])
	}
}

// draft.create in every mode that quotes: the inline picture and the
// forwarded attachments are copied from the decompressed original.
func TestCompressedDraftCreate(t *testing.T) {
	m := seedMailbox(t)
	id := m.seedQuoted(t, "html-inline-cid.eml")
	modes := []api.ComposeMode{api.ComposeReply, api.ComposeReplyAll, api.ComposeForward}
	var plain []*api.DraftCreateResult
	for _, mode := range modes {
		plain = append(plain, m.create(t, mode, id, "Carol wrote:"))
	}
	compressAll(t, m.b, m.acc, id)
	for i, mode := range modes {
		zst := m.create(t, mode, id, "Carol wrote:")
		if zst.Quoted != plain[i].Quoted || !reflect.DeepEqual(zst.Skipped, plain[i].Skipped) || zst.Blocked != plain[i].Blocked {
			t.Errorf("%s: quoted %q/%q skipped %+v/%+v blocked %+v/%+v", mode, plain[i].Quoted, zst.Quoted,
				plain[i].Skipped, zst.Skipped, plain[i].Blocked, zst.Blocked)
		}
		sameDraftFrom(t, m.b, plain[i].Draft, zst.Draft)
	}
}

// draft.open of another client's draft.
func TestCompressedDraftOpen(t *testing.T) {
	m := seedMailbox(t)
	drafts := m.draftsFolder(t)
	m.seedParent(t)
	msg := m.seedIn(t, drafts, 7, "draft-other-client.eml", true)
	plain := m.open(t, msg.ID)
	if len(plain.Draft.Attachments) == 0 {
		t.Fatal("the fixture draft has no attachments")
	}
	compressAll(t, m.b, m.acc, api.MessageID(msg.ID))
	zst := m.open(t, msg.ID)
	if !reflect.DeepEqual(zst.Skipped, plain.Skipped) || zst.Blocked != plain.Blocked {
		t.Errorf("skipped %+v/%+v blocked %+v/%+v", plain.Skipped, zst.Skipped, plain.Blocked, zst.Blocked)
	}
	sameDraftFrom(t, m.b, plain.Draft, zst.Draft)
}

// message.send with compressStore on: the outbox copy, the only one of mail
// not sent yet, stays plain, and the conversion leaves it alone.
func TestCompressedStoreSend(t *testing.T) {
	ctx := context.Background()
	b, _ := newSyncBackend(t)
	acc := api.AccountID(seedAccount(t, b, "me@example.invalid"))
	p := basePrefs()
	p.CompressStore = api.Ptr(true)
	setPrefs(t, b, p)

	draft := api.Draft{AccountID: acc, To: []api.Address{{Address: "to@example.invalid"}}, Subject: "compressed",
		TextBody: strings.Repeat("Some text that compresses well. ", 100)}
	id, version := saveDraft(t, b, draft)
	res, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: acc, DraftID: id, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	if !storedAs(t, b, acc, res.OutboxID, store.RawPlain) {
		t.Fatal("the outbox copy is not stored plain")
	}
	msg, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: acc, MessageID: res.OutboxID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b.store.OpenMessageRaw(ctx, string(acc), string(res.OutboxID))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(raw)
	size, sizeErr := raw.Size()
	raw.Close()
	if err != nil || sizeErr != nil || int64(len(data)) != msg.Message.Size || size != msg.Message.Size {
		t.Fatalf("outbox copy: %d bytes, Size %d (%v), message size %d, %v", len(data), size, sizeErr, msg.Message.Size, err)
	}

	compressAll(t, b, acc)
	if !storedAs(t, b, acc, res.OutboxID, store.RawPlain) {
		t.Fatal("the conversion compressed the outbox copy")
	}
	st := storageOf(t, b)
	if st.Messages != 1 || st.CompressedMessages != 0 {
		t.Fatalf("storage = %+v", st)
	}
	body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: acc, MessageID: res.OutboxID})
	if err != nil || body.Text != draft.TextBody {
		t.Fatalf("body = %+v, %v", body, err)
	}
}

// TestEndToEndCompressedStore runs the production stack with the macOS
// app's default: StartSync stores and applies it, the syncer stores what it
// downloads compressed, the message reads as before, and a message sent
// goes out and comes back byte for byte through the plain outbox copy,
// the server's Sent folder and a compressed local copy.
func TestEndToEndCompressedStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	for _, mb := range []string{"INBOX", "Sent", "Trash"} {
		if err := user.Create(mb, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}, imap.CapSpecialUse: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go imapSrv.Serve(imapLn)
	t.Cleanup(func() { imapSrv.Close() })
	appendRawTestMessage(t, imapLn.Addr().String(), htmlTestMessage)
	smtpRec := startSMTPRecorder(t)

	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true)})
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisors did not stop")
		}
	})
	if v, ok, _ := b.store.GetPreference(ctx, prefCompressStore); !ok || v != "true" || b.store.RawCodec() != store.RawZstd {
		t.Fatalf("the default was not stored and applied: %q %v %s", v, ok, b.store.RawCodec())
	}

	acc := validConfig()
	acc.DisplayName = "Me"
	acc.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: imapLn.Addr().(*net.TCPAddr).Port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	acc.SMTP = &api.ServerConfig{Host: "127.0.0.1", Port: smtpRec.port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID

	// fetchedIn waits for the fetched body of the message with subject
	// in the folder with role and returns the message.
	fetchedIn := func(role api.FolderRole, subject string) api.MessageSummary {
		var found api.MessageSummary
		waitUntil(t, ctx, subject+" fetched", func() bool {
			folders, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
			if err != nil {
				return false
			}
			for _, f := range folders.Folders {
				if f.Role != role {
					continue
				}
				list, err := b.Messages().List(ctx, api.MessageListParams{AccountID: id, FolderID: f.ID})
				if err != nil {
					return false
				}
				for _, msg := range list.Messages {
					if msg.Subject != subject {
						continue
					}
					body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: msg.ID})
					if err == nil && body.BodyState == api.BodyFetched {
						found = msg
						return true
					}
				}
			}
			return false
		})
		return found
	}
	rawOf := func(msgID api.MessageID) []byte {
		r, err := b.store.OpenMessageRaw(ctx, string(id), string(msgID))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	rich := fetchedIn(api.RoleInbox, "Rich")
	if !storedAs(t, b, id, rich.ID, store.RawZstd) {
		t.Fatal("the downloaded message is not stored compressed")
	}
	if got := rawOf(rich.ID); string(got) != htmlTestMessage {
		t.Fatalf("stored message differs from the server's:\n%q", got)
	}
	body, err := b.Messages().Body(ctx, api.MessageBodyParams{AccountID: id, MessageID: rich.ID})
	if err != nil || !strings.Contains(body.HTML, "čeština") || strings.Contains(body.HTML, "<script") || !strings.Contains(body.Text, "plain čeština") {
		t.Fatalf("body = %+v, %v", body, err)
	}

	saved, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{
		AccountID: id,
		To:        []api.Address{{Name: "Bob", Address: "bob@example.org"}},
		Subject:   "Compressed round trip",
		TextBody:  strings.Repeat("Hello Bob, this is čeština. ", 50) + "\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Messages().Send(ctx, api.MessageSendParams{AccountID: id, DraftID: saved.DraftID, Version: saved.Version}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, ctx, "smtp delivery", func() bool { _, _, data := smtpRec.envelope(); return len(data) > 0 })
	_, _, delivered := smtpRec.envelope()
	waitUntil(t, ctx, "copy in server Sent folder", func() bool {
		return serverMailboxCount(t, imapLn.Addr().String(), "Sent") == 1
	})
	copyMsg := fetchedIn(api.RoleSent, "Compressed round trip")
	if !storedAs(t, b, id, copyMsg.ID, store.RawZstd) {
		t.Fatal("the local copy of the sent message is not stored compressed")
	}
	if got := rawOf(copyMsg.ID); !bytes.Equal(got, delivered) {
		t.Fatalf("the sent copy differs from what was delivered:\n copy      %q\n delivered %q", got, delivered)
	}
}

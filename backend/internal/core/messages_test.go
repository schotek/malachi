// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/remoteimg"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// seedRawHTML is the raw form of the first seeded message: an HTML part
// with an inline image (part 1.2), a script, a handler and a remote image,
// plus the PDF attachment (part 2) the envelope lists.
var seedRawHTML = strings.Join([]string{
	"From: Alice <alice@example.invalid>",
	"To: me@example.invalid",
	"Subject: first",
	"Message-ID: <one@example.invalid>",
	"MIME-Version: 1.0",
	`Content-Type: multipart/mixed; boundary="mix"`,
	"",
	"--mix",
	`Content-Type: multipart/related; boundary="rel"`,
	"",
	"--rel",
	"Content-Type: text/html; charset=utf-8",
	"",
	`<html><body><p>hello <b>body</b> <a href="https://example.invalid/x" onclick="x()">link</a></p>` +
		`<img src="cid:logo@example.invalid" alt="logo"><img src="https://remote.invalid/pic.png" width="200"><script>x()</script></body></html>`,
	"--rel",
	"Content-Type: image/png",
	"Content-ID: <logo@example.invalid>",
	`Content-Disposition: inline; filename="logo.png"`,
	"Content-Transfer-Encoding: base64",
	"",
	"iVBORw0KGgo=",
	"--rel--",
	"--mix",
	"Content-Type: application/pdf",
	`Content-Disposition: attachment; filename="a.pdf"`,
	"Content-Transfer-Encoding: base64",
	"",
	"JVBERi0xLjQK",
	"--mix--",
	"",
}, "\r\n")

// mailbox is one seeded account with an inbox, a trash folder, a
// non-selectable container and three messages in the inbox.
type mailbox struct {
	b     *Backend
	f     *fakeSupervisor
	acc   api.AccountID
	inbox api.FolderID
	trash api.FolderID
	cont  api.FolderID
	msgs  []api.MessageID // oldest first: m0 (unread, body fetched), m1 (seen), m2 (unread, no body)
}

func seedMailbox(t *testing.T) *mailbox {
	t.Helper()
	b, f := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
		{Mailbox: "Container", Name: "Container", Path: "Container", Subscribed: true, Selectable: false},
	})
	inbox := folders["INBOX"].ID
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := []*store.Message{
		{AccountID: acc, FolderID: inbox, UID: 1, Subject: "first", Date: base,
			From: []api.Address{{Name: "Alice", Address: "alice@example.invalid"}}, To: []api.Address{{Address: "me@example.invalid"}},
			CC: []api.Address{{Address: "cc@example.invalid"}}, RFCMessageID: "<one@example.invalid>", Size: 100, HasAttachments: true,
			Attachments: []api.Attachment{{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 10}}},
		{AccountID: acc, FolderID: inbox, UID: 2, Subject: "second", Date: base.Add(time.Hour), Flags: []api.Flag{api.FlagSeen},
			From: []api.Address{{Address: "bob@example.invalid"}}, Size: 200},
		{AccountID: acc, FolderID: inbox, UID: 3, Subject: "third", Date: base.Add(2 * time.Hour), Size: 300},
	}
	if err := b.store.UpsertMessages(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// SetMessageBody replaces the attachment list with the parser's view.
	if err := b.store.SetMessageBody(ctx, rows[0].ID, store.BodyUpdate{Text: "hello body", HasHTML: true, Snippet: "hello body",
		Attachments: rows[0].Attachments, HasAttachments: true,
		Headers: map[string]string{"List-Unsubscribe": "<mailto:u@example.invalid>"}}); err != nil {
		t.Fatal(err)
	}
	// message.body and message.part re-read the raw message.
	if _, err := b.store.WriteMessageRaw(ctx, acc, rows[0].ID, strings.NewReader(seedRawHTML), 25<<20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.store.RecountFolder(ctx, inbox); err != nil {
		t.Fatal(err)
	}
	f.reset()
	m := &mailbox{b: b, f: f, acc: api.AccountID(acc), inbox: api.FolderID(inbox),
		trash: api.FolderID(folders["Trash"].ID), cont: api.FolderID(folders["Container"].ID)}
	for _, r := range rows {
		m.msgs = append(m.msgs, api.MessageID(r.ID))
	}
	return m
}

func (m *mailbox) get(t *testing.T, id api.MessageID) (api.Message, error) {
	t.Helper()
	res, err := m.b.Messages().Get(context.Background(), api.MessageGetParams{AccountID: m.acc, MessageID: id})
	if err != nil {
		return api.Message{}, err
	}
	return res.Message, nil
}

func (m *mailbox) folderOf(t *testing.T, id api.MessageID) api.FolderID {
	t.Helper()
	msg, err := m.get(t, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return msg.FolderID
}

func TestMessageList(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	res, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 3 || res.Page.Total != 3 || res.Page.NextCursor != "" {
		t.Fatalf("list = %+v", res)
	}
	if res.Messages[0].Subject != "third" || res.Messages[2].Subject != "first" {
		t.Fatalf("default order not dateDesc: %+v", res.Messages)
	}
	third := res.Messages[0]
	if third.From == nil || third.To == nil || third.Flags == nil {
		t.Fatalf("nil slices in summary: %+v", third)
	}
	first := res.Messages[2]
	if first.ID != m.msgs[0] || first.AccountID != m.acc || first.FolderID != m.inbox || first.From[0].Name != "Alice" ||
		!first.HasAttachments || first.Size != 100 || first.Snippet != "hello body" || len(first.Flags) != 0 {
		t.Fatalf("first = %+v", first)
	}

	asc, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Sort: api.SortDateAsc, Page: api.Page{Limit: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(asc.Messages) != 2 || asc.Messages[0].Subject != "first" || asc.Page.NextCursor == "" || asc.Page.Total != 3 {
		t.Fatalf("asc page 1 = %+v", asc)
	}
	page2, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Sort: api.SortDateAsc, Page: api.Page{Limit: 2, Cursor: asc.Page.NextCursor}})
	if err != nil || len(page2.Messages) != 1 || page2.Messages[0].Subject != "third" || page2.Page.NextCursor != "" {
		t.Fatalf("asc page 2 = %+v, %v", page2, err)
	}

	unread, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Filter: api.FilterUnread})
	if err != nil || len(unread.Messages) != 2 || unread.Page.Total != 2 {
		t.Fatalf("unread = %+v, %v", unread, err)
	}
	// The deprecated unreadOnly is honoured while filter is absent, and
	// ignored once filter is given.
	legacy, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, UnreadOnly: true})
	if err != nil || len(legacy.Messages) != 2 || legacy.Page.Total != 2 {
		t.Fatalf("unreadOnly = %+v, %v", legacy, err)
	}
	all, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Filter: api.FilterAll, UnreadOnly: true})
	if err != nil || all.Page.Total != 3 {
		t.Fatalf("filter overrides unreadOnly = %+v, %v", all, err)
	}

	// "second" is the seen one; flagging it must show up under the flagged
	// filter regardless of its seen state.
	if _, err := svc.Flag(ctx, api.MessageFlagParams{AccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[1]}, Set: []api.Flag{api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	flagged, err := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Filter: api.FilterFlagged})
	if err != nil || len(flagged.Messages) != 1 || flagged.Page.Total != 1 || flagged.Messages[0].Subject != "second" {
		t.Fatalf("flagged = %+v, %v", flagged, err)
	}

	cases := []struct {
		name string
		p    api.MessageListParams
		code api.ErrorCode
	}{
		{"no account", api.MessageListParams{FolderID: m.inbox}, api.CodeInvalidArgument},
		{"no folder", api.MessageListParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"unknown account", api.MessageListParams{AccountID: "acc_nope", FolderID: m.inbox}, api.CodeAccountNotFound},
		{"unknown folder", api.MessageListParams{AccountID: m.acc, FolderID: "f_nope"}, api.CodeFolderNotFound},
		{"bad sort", api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Sort: "subject"}, api.CodeInvalidArgument},
		{"bad filter", api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Filter: "starred"}, api.CodeInvalidArgument},
		{"bad cursor", api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: "!!"}}, api.CodeInvalidArgument},
		{"cursor from other sort", api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: asc.Page.NextCursor}}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		_, err := svc.List(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
}

func TestMessageGet(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()

	msg, err := m.get(t, m.msgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "first" || len(msg.CC) != 1 || msg.RFCMessageID != "<one@example.invalid>" ||
		len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "a.pdf" || msg.Headers["List-Unsubscribe"] == "" {
		t.Fatalf("full message = %+v", msg)
	}
	if msg.References == nil || msg.BCC == nil || msg.ReplyTo == nil {
		t.Fatalf("nil slices in message: %+v", msg)
	}

	bare, err := m.get(t, m.msgs[2])
	if err != nil {
		t.Fatal(err)
	}
	if bare.Attachments == nil || len(bare.Attachments) != 0 || bare.Headers != nil || bare.From == nil {
		t.Fatalf("bare message = %+v", bare)
	}

	if _, err := m.get(t, "m_nope"); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("unknown: %v", err)
	}
	other := seedAccount(t, m.b, "other@example.invalid")
	if _, err := m.b.Messages().Get(ctx, api.MessageGetParams{AccountID: api.AccountID(other), MessageID: m.msgs[0]}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("foreign account: %v", err)
	}
	if _, err := m.b.Messages().Get(ctx, api.MessageGetParams{AccountID: "acc_nope", MessageID: m.msgs[0]}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := m.b.Messages().Get(ctx, api.MessageGetParams{AccountID: m.acc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("missing id: %v", err)
	}
}

func TestMessageBody(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	res, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0]})
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID != m.msgs[0] || res.BodyState != api.BodyFetched || !res.HasHTML || res.Text != "hello body" || res.HTMLWithheld {
		t.Fatalf("body = %+v", res)
	}
	// The sanitised HTML: script gone, handler gone, remote image gone,
	// the inline image pointing at this message's own part.
	wantHTML := `<p>hello <b>body</b> <a href="https://example.invalid/x" rel="noopener noreferrer">link</a></p>` +
		`<img src="malachi-cid:` + string(m.acc) + `/` + string(m.msgs[0]) + `/1.2" alt="logo"/>`
	if res.HTML != wantHTML {
		t.Fatalf("html:\n got %q\nwant %q", res.HTML, wantHTML)
	}
	if want := (api.BlockedContent{RemoteImages: 1, Scripts: 1, EventHandlers: 1}); res.Blocked != want {
		t.Fatalf("blocked = %+v, want %+v", res.Blocked, want)
	}
	if len(res.Links) != 1 || res.Links[0] != (api.Link{Text: "link", Href: "https://example.invalid/x"}) {
		t.Fatalf("links = %+v", res.Links)
	}
	if len(res.InlineParts) != 1 || res.InlineParts["logo@example.invalid"] != "1.2" {
		t.Fatalf("inlineParts = %+v", res.InlineParts)
	}
	// The applied policy is reported, so a client knows whether offering to
	// load the images would change anything.
	if res.RemoteContent != api.RemoteBlock {
		t.Fatalf("remoteContent = %q, want block", res.RemoteContent)
	}
	if res.SanitizerVersion != sanitize.Version || res.SanitizerVersion == "0-stub" {
		t.Fatalf("sanitizerVersion = %q, sanitiser reports %q", res.SanitizerVersion, sanitize.Version)
	}

	// Under allow the daemon fetches the remote image and the sanitiser
	// inlines it; the view never sees the https: URL.
	var asked []string
	m.b.FetchRemoteImages = func(_ context.Context, urls []string) map[string]remoteimg.Image {
		asked = append(asked, urls...)
		return map[string]remoteimg.Image{"https://remote.invalid/pic.png": {MediaType: "image/png", Data: []byte("PNG")}}
	}
	allow, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0], RemoteContent: api.RemoteAllow})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(allow.HTML, `src="data:image/png;base64,UE5H"`) || strings.Contains(allow.HTML, "https://remote.invalid") || allow.Blocked.RemoteImages != 0 {
		t.Fatalf("allow body = %+v", allow)
	}
	if len(asked) != 1 || asked[0] != "https://remote.invalid/pic.png" {
		t.Fatalf("fetcher asked for %v", asked)
	}
	if allow.RemoteContent != api.RemoteAllow {
		t.Fatalf("remoteContent = %q, want allow", allow.RemoteContent)
	}
	// An image the fetcher could not get is dropped and counted, like a
	// blocked one.
	m.b.FetchRemoteImages = func(context.Context, []string) map[string]remoteimg.Image { return nil }
	failed, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0], RemoteContent: api.RemoteAllow})
	if err != nil || strings.Contains(failed.HTML, "remote.invalid") || failed.Blocked.RemoteImages != 1 {
		t.Fatalf("failed fetch body = %+v, %v", failed, err)
	}

	// An HTML part whose raw message is gone is withheld, with the text
	// still served and no error.
	if err := m.b.store.SetMessageBody(ctx, string(m.msgs[1]), store.BodyUpdate{Text: "second text", HasHTML: true, Snippet: "second text"}); err != nil {
		t.Fatal(err)
	}
	gone, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[1]})
	if err != nil {
		t.Fatal(err)
	}
	if !gone.HTMLWithheld || gone.HTML != "" || gone.Text != "second text" || !gone.HasHTML || gone.BodyState != api.BodyFetched {
		t.Fatalf("withheld body = %+v", gone)
	}
	// The same when the sanitiser refuses.
	m.b.Sanitize = func(sanitize.Input) (sanitize.Output, error) {
		return sanitize.Output{Version: sanitize.Version}, api.NewError(api.CodeSanitizeFailed, "refused")
	}
	refused, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0]})
	if err != nil || !refused.HTMLWithheld || refused.HTML != "" || refused.Text != "hello body" || len(refused.Links) != 0 {
		t.Fatalf("refused body = %+v, %v", refused, err)
	}
	m.b.Sanitize = sanitize.Sanitize

	pending, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[2], RemoteContent: api.RemoteAllow})
	if err != nil {
		t.Fatal(err)
	}
	if pending.BodyState != api.BodyPending || pending.Text != "" || pending.HasHTML {
		t.Fatalf("pending body = %+v", pending)
	}

	if err := m.b.store.MarkBodyState(ctx, string(m.msgs[1]), store.BodyTooBig); err != nil {
		t.Fatal(err)
	}
	big, err := svc.Body(ctx, api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[1]})
	if err != nil || big.BodyState != api.BodyTooBig {
		t.Fatalf("tooBig body = %+v, %v", big, err)
	}

	cases := []struct {
		name string
		p    api.MessageBodyParams
		code api.ErrorCode
	}{
		{"knownSenders override", api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0], RemoteContent: api.RemoteKnownSenders}, api.CodeInvalidArgument},
		{"garbage override", api.MessageBodyParams{AccountID: m.acc, MessageID: m.msgs[0], RemoteContent: "yes"}, api.CodeInvalidArgument},
		{"missing ids", api.MessageBodyParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"unknown account", api.MessageBodyParams{AccountID: "acc_nope", MessageID: m.msgs[0]}, api.CodeAccountNotFound},
		{"unknown message", api.MessageBodyParams{AccountID: m.acc, MessageID: "m_nope"}, api.CodeMessageNotFound},
	}
	for _, c := range cases {
		_, err := svc.Body(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
}

func TestMessageFlag(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()
	ids := func(n int, id api.MessageID) []api.MessageID {
		out := make([]api.MessageID, n)
		for i := range out {
			out[i] = id
		}
		return out
	}
	distinct := make([]api.MessageID, api.MaxMessageIDsPerCall+1)
	for i := range distinct {
		distinct[i] = api.MessageID(fmt.Sprintf("m_%d", i))
	}

	invalid := []struct {
		name string
		p    api.MessageFlagParams
	}{
		{"no ids", api.MessageFlagParams{AccountID: m.acc, Set: []api.Flag{api.FlagSeen}}},
		{"empty id", api.MessageFlagParams{AccountID: m.acc, MessageIDs: []api.MessageID{""}, Set: []api.Flag{api.FlagSeen}}},
		{"too many ids", api.MessageFlagParams{AccountID: m.acc, MessageIDs: distinct, Set: []api.Flag{api.FlagSeen}}},
		{"unknown flag", api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1], Set: []api.Flag{"starred"}}},
		{"deleted in set", api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1], Set: []api.Flag{api.FlagDeleted}}},
		{"deleted in clear", api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1], Clear: []api.Flag{api.FlagDeleted}}},
		{"both lists", api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1], Set: []api.Flag{api.FlagSeen}, Clear: []api.Flag{api.FlagSeen}}},
		{"nothing to change", api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1]}},
		{"no account", api.MessageFlagParams{MessageIDs: m.msgs[:1], Set: []api.Flag{api.FlagSeen}}},
	}
	for _, c := range invalid {
		_, err := svc.Flag(ctx, c.p)
		if got := errCode(t, err); got != api.CodeInvalidArgument {
			t.Errorf("%s: code %v, want invalidArgument", c.name, got)
		}
	}
	if _, err := svc.Flag(ctx, api.MessageFlagParams{AccountID: "acc_nope", MessageIDs: m.msgs[:1], Set: []api.Flag{api.FlagSeen}}); errCode(t, err) != api.CodeAccountNotFound {
		t.Fatalf("unknown account: %v", err)
	}

	// All-or-nothing: one unknown id changes nothing.
	_, err := svc.Flag(ctx, api.MessageFlagParams{AccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], "m_nope"}, Set: []api.Flag{api.FlagSeen}})
	if errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	if msg, _ := m.get(t, m.msgs[0]); len(msg.Flags) != 0 {
		t.Fatalf("partial flag applied: %+v", msg.Flags)
	}
	if n := m.f.count("trigger:"); n != 0 {
		t.Fatalf("trigger after failures: %d", n)
	}

	// Success: duplicates collapse, flags applied, syncer nudged once.
	if _, err := svc.Flag(ctx, api.MessageFlagParams{AccountID: m.acc, MessageIDs: ids(api.MaxMessageIDsPerCall+1, m.msgs[0]),
		Set: []api.Flag{api.FlagSeen, api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	msg, _ := m.get(t, m.msgs[0])
	if len(msg.Flags) != 2 {
		t.Fatalf("flags = %v", msg.Flags)
	}
	if got := m.f.recorded(); len(got) != 1 || got[0] != fmt.Sprintf("trigger:%s::false", m.acc) {
		t.Fatalf("supervisor calls = %v", got)
	}
	if _, err := svc.Flag(ctx, api.MessageFlagParams{AccountID: m.acc, MessageIDs: m.msgs[:1], Clear: []api.Flag{api.FlagFlagged}}); err != nil {
		t.Fatal(err)
	}
	msg, _ = m.get(t, m.msgs[0])
	if len(msg.Flags) != 1 || msg.Flags[0] != api.FlagSeen {
		t.Fatalf("flags after clear = %v", msg.Flags)
	}
	if n, _ := m.b.store.CountPendingOps(ctx, string(m.acc)); n != 2 {
		t.Fatalf("pending ops = %d", n)
	}
	if n := m.f.count("trigger:"); n != 2 {
		t.Fatalf("triggers = %d", n)
	}
}

func TestMessageMove(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	cases := []struct {
		name string
		p    api.MessageMoveParams
		code api.ErrorCode
	}{
		{"no ids", api.MessageMoveParams{AccountID: m.acc, TargetFolderID: m.trash}, api.CodeInvalidArgument},
		{"no target", api.MessageMoveParams{AccountID: m.acc, MessageIDs: m.msgs[:1]}, api.CodeInvalidArgument},
		{"unknown account", api.MessageMoveParams{AccountID: "acc_nope", MessageIDs: m.msgs[:1], TargetFolderID: m.trash}, api.CodeAccountNotFound},
		{"unknown target", api.MessageMoveParams{AccountID: m.acc, MessageIDs: m.msgs[:1], TargetFolderID: "f_nope"}, api.CodeFolderNotFound},
		{"container target", api.MessageMoveParams{AccountID: m.acc, MessageIDs: m.msgs[:1], TargetFolderID: m.cont}, api.CodeInvalidArgument},
		{"unknown id", api.MessageMoveParams{AccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], "m_nope"}, TargetFolderID: m.trash}, api.CodeMessageNotFound},
	}
	for _, c := range cases {
		_, err := svc.Move(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
	if m.folderOf(t, m.msgs[0]) != m.inbox || m.f.count("trigger:") != 0 {
		t.Fatal("a rejected move changed something")
	}

	if _, err := svc.Move(ctx, api.MessageMoveParams{AccountID: m.acc, MessageIDs: m.msgs[:2], TargetFolderID: m.trash}); err != nil {
		t.Fatal(err)
	}
	if m.folderOf(t, m.msgs[0]) != m.trash || m.folderOf(t, m.msgs[1]) != m.trash || m.folderOf(t, m.msgs[2]) != m.inbox {
		t.Fatal("move not applied")
	}
	if n := m.f.count("trigger:"); n != 1 {
		t.Fatalf("triggers = %d", n)
	}
	list, _ := svc.List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.trash})
	if len(list.Messages) != 2 {
		t.Fatalf("trash list = %+v", list)
	}
}

func TestMessageDelete(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: m.acc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("no ids: %v", err)
	}
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: m.acc, MessageIDs: []api.MessageID{m.msgs[0], "m_nope"}}); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	if m.folderOf(t, m.msgs[0]) != m.inbox {
		t.Fatal("all-or-nothing violated")
	}

	// Default: to Trash, then gone on the second delete.
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: m.acc, MessageIDs: m.msgs[:1]}); err != nil {
		t.Fatal(err)
	}
	if m.folderOf(t, m.msgs[0]) != m.trash {
		t.Fatal("not moved to trash")
	}
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: m.acc, MessageIDs: m.msgs[:1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.get(t, m.msgs[0]); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("still present after deleting from trash: %v", err)
	}

	// Permanent skips the trash.
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: m.acc, MessageIDs: m.msgs[1:2], Permanent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.get(t, m.msgs[1]); errCode(t, err) != api.CodeMessageNotFound {
		t.Fatalf("still present after permanent delete: %v", err)
	}
	if n := m.f.count("trigger:"); n != 3 {
		t.Fatalf("triggers = %d", n)
	}

	// Without a trash folder only a permanent delete works.
	other := seedAccount(t, m.b, "other@example.invalid")
	folders := seedFolders(t, m.b, other, []store.Folder{{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true}})
	row := &store.Message{AccountID: other, FolderID: folders["INBOX"].ID, UID: 1, Subject: "x", Date: time.Now()}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: api.AccountID(other), MessageIDs: []api.MessageID{api.MessageID(row.ID)}}); errCode(t, err) != api.CodeFolderNotFound {
		t.Fatalf("no trash: %v", err)
	}
	if _, err := svc.Delete(ctx, api.MessageDeleteParams{AccountID: api.AccountID(other), MessageIDs: []api.MessageID{api.MessageID(row.ID)}, Permanent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(ctx, api.MessageSendParams{AccountID: m.acc}); errCode(t, err) != api.CodeInvalidArgument {
		t.Fatalf("send without draft: %v", err)
	}
}

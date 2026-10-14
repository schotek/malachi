// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// forwardMany sends draft.create forward with messageIds.
func (m *mailbox) forwardMany(ids ...api.MessageID) (*api.DraftCreateResult, error) {
	return m.b.Drafts().Create(context.Background(), api.DraftCreateParams{
		AccountID: m.acc, Mode: api.ComposeForward, MessageIDs: ids,
	})
}

// rawOf stores raw as the file of message id.
func (m *mailbox) rawOf(t *testing.T, id api.MessageID, raw string) {
	t.Helper()
	if _, err := m.b.store.WriteMessageRaw(context.Background(), string(m.acc), string(id), strings.NewReader(raw), 25<<20); err != nil {
		t.Fatal(err)
	}
}

const secondRaw = "From: bob@example.invalid\r\nTo: me@example.invalid\r\nSubject: second\r\n\r\nthe second body\r\n"

func TestForwardManyRefuses(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	many := make([]api.MessageID, api.MaxForwardMessages+1)
	for i := range many {
		many[i] = api.MessageID("m_x" + strings.Repeat("y", i))
	}
	cases := []struct {
		name string
		p    api.DraftCreateParams
		code api.ErrorCode
	}{
		{"reply", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageIDs: m.msgs[:2]}, api.CodeInvalidArgument},
		{"with messageId", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward, MessageID: m.msgs[0], MessageIDs: m.msgs[:2]}, api.CodeInvalidArgument},
		{"one, twice", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward, MessageIDs: []api.MessageID{m.msgs[0], m.msgs[0]}}, api.CodeInvalidArgument},
		{"empty id", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward, MessageIDs: []api.MessageID{m.msgs[0], ""}}, api.CodeInvalidArgument},
		{"too many", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward, MessageIDs: many}, api.CodeInvalidArgument},
		{"unknown", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward, MessageIDs: []api.MessageID{m.msgs[0], "m_nope"}}, api.CodeMessageNotFound},
		{"new", api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeNew, MessageIDs: m.msgs[:2]}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		if _, err := m.b.Drafts().Create(ctx, c.p); errCode(t, err) != c.code {
			t.Errorf("%s: %v, want %v", c.name, err, c.code)
		}
	}
	// Another account's messages are unknown in this one.
	other := seedAccount(t, m.b, "other@example.invalid")
	if _, err := m.b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: api.AccountID(other), Mode: api.ComposeForward,
		MessageIDs: m.msgs[:2]}); errCode(t, err) != api.CodeMessageNotFound {
		t.Errorf("foreign messages: %v", err)
	}
	// Forwarded from another account (messageAccountId): the copies go
	// into the draft's account.
	m.rawOf(t, m.msgs[1], secondRaw)
	res, err := m.b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: api.AccountID(other), Mode: api.ComposeForward,
		MessageIDs: m.msgs[:2], MessageAccountID: m.acc})
	if err != nil || res.Draft.AccountID != api.AccountID(other) || len(res.Draft.Attachments) != 2 {
		t.Fatalf("from another account: %+v %v", res, err)
	}
	if _, err := m.b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: api.AccountID(other),
		AttachmentID: res.Draft.Attachments[0].ID}); err != nil {
		t.Errorf("the copy is not the draft account's: %v", err)
	}
}

// Each message whole as message/rfc822, newest first; the subject is the
// newest one's (attached or not); nothing quoted; a message without its
// file is reported.
func TestForwardManyAttachesTheMessages(t *testing.T) {
	m := seedMailbox(t)
	m.rawOf(t, m.msgs[1], secondRaw)
	res, err := m.forwardMany(m.msgs[0], m.msgs[1], m.msgs[2], m.msgs[1])
	if err != nil {
		t.Fatal(err)
	}
	d := res.Draft
	if d.Subject != "Fwd: third" || d.AccountID != m.acc || d.Forwarding != m.msgs[2] || d.InReplyTo != "" ||
		d.HTMLBody != "" || d.TextBody != "" || res.Quoted != api.QuoteNone {
		t.Fatalf("draft %+v quoted %q", d, res.Quoted)
	}
	if len(d.Attachments) != 2 || d.Attachments[0].Filename != "second.eml" || d.Attachments[1].Filename != "first.eml" {
		t.Fatalf("attachments %+v", d.Attachments)
	}
	for _, a := range d.Attachments {
		if a.ContentType != "message/rfc822" || a.Inline {
			t.Errorf("attachment %+v", a)
		}
	}
	got, err := os.ReadFile(m.b.store.AttachmentPath(d.Attachments[0].ID))
	if err != nil || string(got) != secondRaw {
		t.Errorf("the second message as attached: %q %v", got, err)
	}
	// The third has no file: reported, as kept on the server.
	if len(res.Skipped) != 1 || res.Skipped[0].Filename != "third.eml" || !res.Skipped[0].Remote {
		t.Errorf("skipped %+v", res.Skipped)
	}
}

// Over the cap of one part: reported, not attached; a copy held in
// memory stands in for a message not whole on disk.
func TestForwardManyCapsAndHeldCopies(t *testing.T) {
	m := seedMailbox(t)
	m.rawOf(t, m.msgs[1], secondRaw)
	prev := quotedPartLimit
	quotedPartLimit = 64
	t.Cleanup(func() { quotedPartLimit = prev })
	held := "From: c@example.invalid\r\nSubject: third\r\n\r\nheld\r\n"
	if !m.b.mem.put(m.b.mem.generation(), string(m.acc), string(m.msgs[2]), []byte(held), nil) {
		t.Fatal("not held")
	}
	res, err := m.forwardMany(m.msgs...)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, a := range res.Draft.Attachments {
		names = append(names, a.Filename)
	}
	if strings.Join(names, ",") != "third.eml" {
		t.Errorf("attached %v", names)
	}
	skipped := []string{}
	for _, s := range res.Skipped {
		skipped = append(skipped, s.Filename)
		if s.Remote {
			t.Errorf("%s reported as remote", s.Filename)
		}
	}
	if strings.Join(skipped, ",") != "second.eml,first.eml" {
		t.Errorf("skipped %v", skipped)
	}
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The calendar part of the card shown gets no chip; without a card it is
// a file like any other.
func TestChipsLeaveOutTheCardsPart(t *testing.T) {
	atts := []api.Attachment{
		{PartID: "3", Filename: "invite.ics", ContentType: "text/calendar"},
		{PartID: "4", Filename: "agenda.pdf", ContentType: "application/pdf"},
	}
	card := &api.InvitationCard{PartID: "3"}
	if got := chipAttachments(atts, nil, card); len(got) != 1 || got[0].Filename != "agenda.pdf" {
		t.Errorf("with the card: %+v", got)
	}
	if got := chipAttachments(atts, nil, nil); len(got) != 2 {
		t.Errorf("without a card: %+v", got)
	}
	m := &api.Message{MessageSummary: api.MessageSummary{AccountID: "a"}, Attachments: atts,
		Invitation: &api.InvitationCard{InvitationSummary: api.InvitationSummary{Kind: api.InvitationKindInvitation,
			State: api.InvitationAnswered}, PartID: "3"}}
	var w *Window
	if c := w.shownInvitation(m); c == nil || c.PartID != "3" {
		t.Errorf("shown card %+v", c)
	}
	if b := bubbleAttachmentBadge(m.MessageSummary, &loadedMessage{msg: m}, true); b.Count != 1 {
		t.Errorf("bubble pill %+v", b)
	}
}

// The row of an invitation previews its time, of a reply its answer;
// anything else keeps the snippet.
func TestInvitationPreview(t *testing.T) {
	start := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	s := api.MessageSummary{Invitation: &api.InvitationSummary{Kind: api.InvitationKindInvitation,
		State: api.InvitationAnswered, Start: &start, End: &end}}
	if got := invitationPreview(s, "____"); got == "____" || !strings.Contains(got, "2026") {
		t.Errorf("invitation: %q", got)
	}
	if got := invitationPreview(api.MessageSummary{}, "Hello"); got != "Hello" {
		t.Errorf("plain: %q", got)
	}
	s.Invitation = &api.InvitationSummary{Kind: api.InvitationKindReply, State: api.InvitationProcessed,
		ReplyResponse: api.ResponseDeclined}
	if got := invitationPreview(s, ""); got != "Invitation declined" {
		t.Errorf("reply: %q", got)
	}
	s.Invitation = &api.InvitationSummary{Kind: api.InvitationKindInvitation, State: api.InvitationAnswered}
	if got := invitationPreview(s, "Hello"); got != "Hello" {
		t.Errorf("no time: %q", got)
	}
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// reply is an answer to an invitation in conversation t1.
func invReply(id string, min int, seen bool, from api.Address, r api.EventResponse) api.MessageSummary {
	s := mail(id, min, seen)
	s.From, s.To, s.Snippet = []api.Address{from}, nil, ""
	s.HasAttachments = true
	s.Invitation = &api.InvitationSummary{Method: api.InvitationMethodReply, Kind: api.InvitationKindReply,
		State: api.InvitationProcessed, Title: "Quarterly report", ReplyFrom: &from, ReplyResponse: r}
	return s
}

func TestInvitationReplyLine(t *testing.T) {
	for r, want := range map[api.EventResponse]string{
		api.ResponseAccepted:    "Invitation accepted",
		api.ResponseTentative:   "Invitation tentatively accepted",
		api.ResponseDeclined:    "Invitation declined",
		api.ResponseNeedsAction: "Replied to the invitation",
		"":                      "Replied to the invitation",
	} {
		if got := InvitationReplyLine(r, identity{}); got != want {
			t.Errorf("%q: %q, want %q", r, got, want)
		}
	}
	req := mail("m1", 0, true)
	req.Invitation = &api.InvitationSummary{Method: api.InvitationMethodRequest, Kind: api.InvitationKindInvitation}
	if IsInvitationReply(req) || IsInvitationReply(mail("m2", 0, true)) || !IsInvitationReply(invReply("r", 0, true, jana, "")) {
		t.Error("IsInvitationReply")
	}
}

// An answer to an invitation is a compact row in both layouts: an event
// with its sentence and sender, no card actions; the user's own answer in
// Sent is a sent row; an attendee's unread answer is marked read.
func TestInvitationReplyRows(t *testing.T) {
	invite := mail("m1", 0, true)
	invite.Invitation = &api.InvitationSummary{Method: api.InvitationMethodRequest, Kind: api.InvitationKindInvitation}
	mine := invReply("s1", 15, true, petr, api.ResponseTentative)
	m := Build(mailThread(1), []api.MessageSummary{invite}, []api.MessageSummary{mine}, mailAccount, identity{})
	if len(m.Items) != 2 {
		t.Fatalf("items %+v", m.Items)
	}
	r := m.Items[1]
	if r.Kind != ItemEvent || !r.Sent || r.Unread || !r.Mine || r.Sender != "Petr Svoboda" ||
		len(r.EventLines) != 1 || r.EventLines[0] != "Invitation tentatively accepted" || r.EventText != r.EventLines[0] {
		t.Errorf("own answer %+v", r)
	}
	if a := CardActions(mailAccount, mine, true); a.Reply || a.ReplyAll || a.Forward {
		t.Errorf("actions of an answer %+v", a)
	}
	rows := ChatRows(m, nil)
	if len(rows) != 2 || rows[1].Kind != ChatEvent || rows[1].Side != ChatCenter {
		t.Errorf("chat rows %+v", rows)
	}
	if m.MarkRead != "" {
		t.Errorf("mark read %q", m.MarkRead)
	}

	theirs := invReply("r1", 20, false, jana, api.ResponseAccepted)
	m = Build(mailThread(2), []api.MessageSummary{invite, theirs}, nil, mailAccount, identity{})
	if it := m.Items[1]; it.Kind != ItemEvent || !it.Unread || it.EventLines[0] != "Invitation accepted" || it.Mine {
		t.Errorf("attendee's answer %+v", it)
	}
	if m.MarkRead != "r1" {
		t.Errorf("mark read %q, want r1", m.MarkRead)
	}
	// A newer card that is read: nothing to mark, the answer stays as it is.
	m = Merge(m, mail("m2", 30, true), mailAccount, identity{})
	if m.MarkRead != "" {
		t.Errorf("mark read after a read card %q", m.MarkRead)
	}
}

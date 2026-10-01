// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package conversation

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// reply is Petr's reply in his Sent folder fs (thread.get's sent).
func reply(id string, min int) api.MessageSummary {
	return api.MessageSummary{
		ID: api.MessageID(id), AccountID: "a1", FolderID: "fs", ThreadID: "t1",
		From: []api.Address{petr}, To: []api.Address{jana},
		Subject: "Re: Quarterly report", Date: at(min), Snippet: "Thanks",
		// Unread on purpose: a sent card is never unread.
	}
}

// sentShape is shape with the sent cards marked "sent:".
func sentShape(m Model) []string {
	out := shape(m)
	for i, it := range m.Items {
		if it.Sent {
			out[i] = "sent:" + string(it.Message.ID)
		}
	}
	return out
}

func TestIsConversationRowCountsSent(t *testing.T) {
	cases := []struct {
		count, sent int
		want        bool
	}{
		{1, 0, false}, {1, 1, true}, {0, 2, true}, {2, 0, true}, {0, 1, false}, {1, -5, false},
	}
	for _, c := range cases {
		if got := IsConversationRow(api.ThreadSummary{MessageCount: c.count, SentCount: c.sent}); got != c.want {
			t.Errorf("IsConversationRow(%d, sent %d) = %v, want %v", c.count, c.sent, got, c.want)
		}
	}
}

func TestBuildWithSent(t *testing.T) {
	thread := mailThread(2)
	thread.SentCount = 2
	members := []api.MessageSummary{mail("m2", 20, false), mail("m1", 0, true)}
	sent := []api.MessageSummary{reply("r2", 30), reply("r1", 10), reply("r1", 10), {ID: ""}}
	m := Build(thread, members, sent, mailAccount, tr)
	if got, want := sentShape(m), []string{"msg:m1", "sent:r1", "msg:m2", "sent:r2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	// The newest folder member is marked read, not the newer reply; a sent
	// card is never unread and is the user's own.
	if m.MarkRead != "m2" || m.ScrollTo != 3 || m.Earlier != 0 {
		t.Errorf("MarkRead %q, ScrollTo %d, Earlier %d", m.MarkRead, m.ScrollTo, m.Earlier)
	}
	r := m.Items[m.Index("r2")]
	if r.Unread || !r.Mine || r.Sender != "Petr Svoboda" || r.Kind != ItemMessage {
		t.Errorf("sent card = %+v", r)
	}

	// Only replies, all of them unread: the newest member is marked.
	only := Build(mailThread(1), []api.MessageSummary{mail("m1", 0, false)}, []api.MessageSummary{reply("r1", 10)}, mailAccount, tr)
	if got := sentShape(only); !reflect.DeepEqual(got, []string{"msg:m1", "sent:r1"}) || only.MarkRead != "m1" {
		t.Errorf("one member and a reply: %v, MarkRead %q", got, only.MarkRead)
	}
}

func TestBuildWithSentPathological(t *testing.T) {
	// A sent message with a member's id is the member.
	dup := reply("m1", 5)
	m := Build(mailThread(1), []api.MessageSummary{mail("m1", 0, true)}, []api.MessageSummary{dup}, mailAccount, tr)
	if got := sentShape(m); !reflect.DeepEqual(got, []string{"msg:m1"}) {
		t.Errorf("sent with a member's id: %v", got)
	}
	// Sent alone is no conversation of the folder.
	for name, members := range map[string][]api.MessageSummary{"nil": nil, "no id": {{ID: ""}}} {
		m := Build(mailThread(0), members, []api.MessageSummary{reply("r1", 0), reply("r2", 1)}, mailAccount, tr)
		if len(m.Items) != 0 || m.Earlier != 0 || m.ScrollTo != -1 || m.MarkRead != "" || m.Thread != "t1" {
			t.Errorf("%s: sent only = %+v", name, m)
		}
	}
	// A sent event (never from a mail account) is left out.
	ev := statusEvent("e1", 5, "To Do", "Done")
	j := Build(jiraThread(1), []api.MessageSummary{issueMsg("d", 0, true, api.MessageIssue{Item: api.IssueItemDescription})},
		[]api.MessageSummary{ev}, jiraAccount, tr)
	if got := sentShape(j); !reflect.DeepEqual(got, []string{"msg:d"}) {
		t.Errorf("sent event: %v", got)
	}
	// Equal dates order by id, as members do.
	same := Build(mailThread(1), []api.MessageSummary{mail("m", 10, true)}, []api.MessageSummary{reply("a", 10), reply("z", 10)}, mailAccount, tr)
	if got := sentShape(same); !reflect.DeepEqual(got, []string{"sent:a", "msg:m", "sent:z"}) {
		t.Errorf("ties: %v", got)
	}
	// Beyond the cap only the newest replies stay.
	var many []api.MessageSummary
	for i := 0; i < api.MaxThreadMessages+3; i++ {
		many = append(many, reply(fmt.Sprintf("r%04d", i), i+1))
	}
	capped := Build(mailThread(1), []api.MessageSummary{mail("m", 0, true)}, many, mailAccount, tr)
	if len(capped.Items) != api.MaxThreadMessages+1 || capped.Index("r0002") >= 0 || capped.Index("r0003") != 1 {
		t.Errorf("capped: %d items, r0003 at %d", len(capped.Items), capped.Index("r0003"))
	}
}

// When older members are left out, a reply older than the oldest member
// shown is too: it would sit among the members the row says are missing.
func TestBuildWithSentCut(t *testing.T) {
	m := Build(mailThread(5), []api.MessageSummary{mail("m4", 40, true), mail("m5", 50, true)},
		[]api.MessageSummary{reply("r1", 10), reply("r4", 45)}, mailAccount, tr)
	if got, want := sentShape(m), []string{"more", "msg:m4", "sent:r4", "msg:m5"}; !reflect.DeepEqual(got, want) || m.Earlier != 3 {
		t.Errorf("cut: %v (Earlier %d), want %v", got, m.Earlier, want)
	}
	if got := sentShape(MergeSent(m, reply("r0", 5), mailAccount, tr)); !reflect.DeepEqual(got, []string{"more", "msg:m4", "sent:r4", "msg:m5"}) {
		t.Errorf("older reply merged into a cut conversation: %v", got)
	}
}

func TestMergeSent(t *testing.T) {
	base := Build(mailThread(2), []api.MessageSummary{mail("m1", 0, true), mail("m2", 20, false)}, nil, mailAccount, tr)
	m := MergeSent(base, reply("r1", 10), mailAccount, tr)
	if got := sentShape(m); !reflect.DeepEqual(got, []string{"msg:m1", "sent:r1", "msg:m2"}) || m.MarkRead != "m2" || m.ScrollTo != 2 {
		t.Fatalf("merged: %v, MarkRead %q, ScrollTo %d", got, m.MarkRead, m.ScrollTo)
	}
	if len(base.Items) != 2 {
		t.Error("MergeSent changed the model given")
	}
	// A changed reply moves to its date; a newer one is last, and still
	// not marked.
	moved := reply("r1", 30)
	moved.Flags = []api.Flag{api.FlagFlagged}
	m2 := MergeSent(m, moved, mailAccount, tr)
	if got := sentShape(m2); !reflect.DeepEqual(got, []string{"msg:m1", "msg:m2", "sent:r1"}) || m2.MarkRead != "m2" || m2.ScrollTo != 2 {
		t.Errorf("moved: %v, MarkRead %q", got, m2.MarkRead)
	}
	if len(m2.Items[2].Message.Flags) != 1 {
		t.Error("the reply was not replaced")
	}
	// Left alone: no id, another conversation, a member's id, an event.
	other := reply("r9", 5)
	other.ThreadID = "t2"
	for name, s := range map[string]api.MessageSummary{
		"no id": {}, "other thread": other, "member id": reply("m1", 5),
		"event": func() api.MessageSummary { e := statusEvent("e", 5, "A", "B"); e.ThreadID = "t1"; return e }(),
	} {
		if got := MergeSent(m, s, mailAccount, tr); !reflect.DeepEqual(sentShape(got), sentShape(m)) {
			t.Errorf("%s: %v", name, sentShape(got))
		}
	}
	// An empty model stays empty.
	empty := Build(mailThread(0), nil, nil, mailAccount, tr)
	if got := MergeSent(empty, reply("r1", 0), mailAccount, tr); len(got.Items) != 0 {
		t.Errorf("reply into an empty model: %v", sentShape(got))
	}
	// A member arriving with a reply's id takes its place.
	m3 := Merge(m, mail("r1", 10, false), mailAccount, tr)
	if got := sentShape(m3); !reflect.DeepEqual(got, []string{"msg:m1", "msg:r1", "msg:m2"}) || m3.Items[1].Sent {
		t.Errorf("member over a reply: %v", got)
	}
}

func TestRemoveWithSent(t *testing.T) {
	m := Build(mailThread(2), []api.MessageSummary{mail("m1", 0, true), mail("m2", 20, false)},
		[]api.MessageSummary{reply("r1", 10), reply("r2", 30)}, mailAccount, tr)
	r := Remove(m, "r2")
	if got := sentShape(r); !reflect.DeepEqual(got, []string{"msg:m1", "sent:r1", "msg:m2"}) || r.ScrollTo != 2 || r.MarkRead != "m2" {
		t.Errorf("reply removed: %v, ScrollTo %d", got, r.ScrollTo)
	}
	// The members go, the replies stay behind: no conversation of the
	// folder is left.
	gone := Remove(Remove(m, "m1"), "m2")
	if len(gone.Items) != 0 || gone.ScrollTo != -1 || gone.MarkRead != "" {
		t.Errorf("members gone: %v", sentShape(gone))
	}
	cut := Build(mailThread(4), []api.MessageSummary{mail("m3", 20, true)}, []api.MessageSummary{reply("r3", 30)}, mailAccount, tr)
	if gone := Remove(cut, "m3"); len(gone.Items) != 0 || gone.Earlier != 3 {
		t.Errorf("cut members gone: %v, Earlier %d", sentShape(gone), gone.Earlier)
	}
}

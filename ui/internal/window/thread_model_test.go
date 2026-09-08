// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

var threadBase = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

// member is a folder member of conversation tid, dated n hours after
// threadBase, from the given sender.
func member(id, tid string, n int, from string, flags ...api.Flag) api.MessageSummary {
	return api.MessageSummary{
		ID: api.MessageID(id), ThreadID: api.ThreadID(tid), AccountID: "acc", FolderID: "f_inbox",
		Subject: "s-" + id, Snippet: "p-" + id, Date: threadBase.Add(time.Duration(n) * time.Hour),
		From: []api.Address{{Name: from, Address: from + "@example.invalid"}}, Flags: flags,
	}
}

// thr is a conversation summary over latest and count members, of which
// unread are unread.
func thr(tid string, count, unread int, latest api.MessageSummary, flags ...api.Flag) api.ThreadSummary {
	return api.ThreadSummary{
		ID: api.ThreadID(tid), AccountID: "acc", Subject: latest.Subject, Participants: latest.From,
		MessageCount: count, UnreadCount: unread, LatestDate: latest.Date, Latest: latest,
		Snippet: latest.Snippet, Flags: flags, HasAttachments: latest.HasAttachments, FolderIDs: []api.FolderID{"f_inbox"},
	}
}

func groupedModel(threads ...api.ThreadSummary) *mailModel {
	m := &mailModel{grouped: true, listFilter: api.FilterAll}
	m.setThreads(threads, api.PageInfo{Total: len(threads)})
	return m
}

func keys(m *mailModel) []listKey {
	out := make([]listKey, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r.Key)
	}
	return out
}

func TestRebuildRowsCollapsedAndExpanded(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	b1 := member("b1", "t_b", 1, "carol")
	m := groupedModel(thr("t_a", 2, 1, a2), thr("t_b", 1, 0, b1, api.FlagSeen))
	want := []listKey{{Thread: "t_a"}, {Thread: "t_b", Message: "b1"}}
	if got := keys(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("collapsed rows = %v, want %v", got, want)
	}
	if r := m.rows[0]; !r.Thread || r.Message.ID != "a2" || r.Summary.MessageCount != 2 || r.Expanded || r.Loading {
		t.Fatalf("thread row = %+v", r)
	}
	if r := m.rows[1]; r.Thread || r.Member || r.Message.ID != "b1" {
		t.Fatalf("singleton row = %+v", r)
	}

	// Unfolded before the members are known: the row spins, no members.
	m.setExpanded("t_a", true)
	if got := keys(m); !reflect.DeepEqual(got, want) || !m.rows[0].Loading || !m.rows[0].Expanded {
		t.Fatalf("expanded, incomplete: %v %+v", got, m.rows[0])
	}
	a1 := member("a1", "t_a", 1, "alice")
	m.setMembers("t_a", thr("t_a", 2, 1, a2), []api.MessageSummary{a1, a2})
	want = []listKey{{Thread: "t_a"}, {Thread: "t_a", Message: "a1"}, {Thread: "t_a", Message: "a2"}, {Thread: "t_b", Message: "b1"}}
	if got := keys(m); !reflect.DeepEqual(got, want) || m.rows[0].Loading || !m.rows[1].Member {
		t.Fatalf("expanded rows = %v, %+v", got, m.rows[0])
	}
	if s, idx, ok := m.message("a1"); !ok || s.ID != "a1" || idx != 1 {
		t.Fatalf("member lookup = %+v %d %v", s, idx, ok)
	}
	if m.rowIndexOf(listKey{Thread: "t_b", Message: "b1"}) != 3 || m.rowIndexOf(listKey{Message: "zz"}) != -1 || m.rowCount() != 4 {
		t.Fatal("row index lookups")
	}
	m.setExpanded("t_a", false)
	if len(m.rows) != 2 || m.rows[0].Expanded {
		t.Fatalf("folded again: %v", keys(m))
	}
	// A folded member is still known, just without a row.
	if _, idx, ok := m.message("a1"); !ok || idx != -1 {
		t.Fatalf("folded member lookup idx = %d ok %v", idx, ok)
	}
	if k := m.keyFor("a1"); k != (listKey{Thread: "t_a", Message: "a1"}) {
		t.Fatalf("keyFor = %+v", k)
	}
}

func TestSetThreadsKeepsCache(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	a1 := member("a1", "t_a", 1, "alice")
	m := groupedModel(thr("t_a", 2, 1, a2))
	m.setExpanded("t_a", true)
	m.setMembers("t_a", thr("t_a", 2, 1, a2), []api.MessageSummary{a1, a2})

	// Same shape: the members survive the reload, the row stays open.
	m.setThreads([]api.ThreadSummary{thr("t_a", 2, 1, a2)}, api.PageInfo{Total: 1})
	if !m.members["t_a"].complete || len(m.rows) != 3 || m.rows[0].Loading {
		t.Fatalf("cache dropped: %+v rows %d", m.members["t_a"], len(m.rows))
	}
	// A reply arrived meanwhile: the members are asked for again.
	a3 := member("a3", "t_a", 3, "carol")
	m.setThreads([]api.ThreadSummary{thr("t_a", 3, 2, a3)}, api.PageInfo{Total: 1})
	if m.members["t_a"].complete || len(m.rows) != 1 || !m.rows[0].Loading {
		t.Fatalf("stale cache kept: %+v rows %d", m.members["t_a"], len(m.rows))
	}
	// Duplicates are ignored.
	m.setThreads([]api.ThreadSummary{thr("t_a", 3, 2, a3), thr("t_a", 3, 2, a3)}, api.PageInfo{Total: 1})
	if len(m.threads) != 1 {
		t.Fatal("duplicate thread listed")
	}
	if added := m.appendThreads([]api.ThreadSummary{thr("t_a", 3, 2, a3), thr("t_b", 1, 0, member("b1", "t_b", 0, "dave"))}, api.PageInfo{}); added != 1 || len(m.rows) != 2 {
		t.Fatalf("append added %d, rows %d", added, len(m.rows))
	}
}

func TestSetMembersEmptyDropsThread(t *testing.T) {
	m := groupedModel(thr("t_a", 2, 0, member("a2", "t_a", 2, "bob")), thr("t_b", 1, 0, member("b1", "t_b", 1, "carol")))
	m.setMembers("t_a", api.ThreadSummary{}, nil)
	if len(m.threads) != 1 || m.threads[0].ID != "t_b" || m.total != 1 || len(m.rows) != 1 {
		t.Fatalf("after drop: %v total %d", keys(m), m.total)
	}
}

func TestApplyNewMessageExistingThread(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	b1 := member("b1", "t_b", 5, "carol")
	m := groupedModel(thr("t_b", 1, 0, b1, api.FlagSeen), thr("t_a", 2, 1, a2))
	m.setExpanded("t_a", true)
	m.setMembers("t_a", thr("t_a", 2, 1, a2), []api.MessageSummary{member("a1", "t_a", 1, "alice"), a2})

	a3 := member("a3", "t_a", 9, "Alice") // a known address, capitalised: no new participant
	a3.HasAttachments = true
	if !m.applyNewMessage(a3, api.FilterAll, listKey{}) {
		t.Fatal("rejected")
	}
	if m.threads[0].ID != "t_a" {
		t.Fatalf("thread not moved to the top: %v", keys(m))
	}
	th := m.threads[0]
	if th.MessageCount != 3 || th.UnreadCount != 2 || !th.LatestDate.Equal(a3.Date) || th.Latest.ID != "a3" || th.Snippet != "p-a3" || !th.HasAttachments {
		t.Fatalf("aggregates = %+v", th)
	}
	if len(th.Participants) != 2 || th.Participants[0].Name != "Alice" || th.Participants[1].Name != "bob" {
		t.Fatalf("participants = %+v", th.Participants)
	}
	if mem := m.members["t_a"]; len(mem.list) != 3 || mem.list[2].ID != "a3" || !mem.complete {
		t.Fatalf("members = %+v", mem)
	}
	want := []listKey{{Thread: "t_a"}, {Thread: "t_a", Message: "a1"}, {Thread: "t_a", Message: "a2"}, {Thread: "t_a", Message: "a3"}, {Thread: "t_b", Message: "b1"}}
	if got := keys(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v", got)
	}
	// Delivered twice: nothing changes.
	if !m.applyNewMessage(a3, api.FilterAll, listKey{}) || m.threads[0].MessageCount != 3 {
		t.Fatal("duplicate counted")
	}

	// A reply to the selected single-message row unfolds it, so the row
	// the user reads stays.
	b2 := member("b2", "t_b", 10, "dave")
	if !m.applyNewMessage(b2, api.FilterAll, listKey{Thread: "t_b", Message: "b1"}) {
		t.Fatal("rejected")
	}
	if !m.expanded["t_b"] || m.threads[0].ID != "t_b" || m.rowIndexOf(listKey{Thread: "t_b", Message: "b1"}) != 1 {
		t.Fatalf("selected singleton not unfolded: %v", keys(m))
	}
}

func TestApplyNewMessageNewThread(t *testing.T) {
	m := groupedModel(thr("t_a", 1, 0, member("a1", "t_a", 1, "alice", api.FlagSeen), api.FlagSeen))
	c1 := member("c1", "t_c", 3, "carol")
	if !m.applyNewMessage(c1, api.FilterAll, listKey{}) || m.threads[0].ID != "t_c" || m.total != 2 {
		t.Fatalf("new thread: %v total %d", keys(m), m.total)
	}
	th := m.threads[0]
	if th.MessageCount != 1 || th.UnreadCount != 1 || th.Latest.ID != "c1" || len(th.Participants) != 1 || !m.members["t_c"].complete {
		t.Fatalf("synthesised = %+v", th)
	}
	// Under the unread filter a read arrival is not listed.
	d1 := member("d1", "t_d", 4, "dave", api.FlagSeen)
	if !m.applyNewMessage(d1, api.FilterUnread, listKey{}) || len(m.threads) != 2 {
		t.Fatalf("seen message listed under unread: %v", keys(m))
	}
	// No thread id: the caller reloads.
	e1 := member("e1", "", 5, "eve")
	if m.applyNewMessage(e1, api.FilterAll, listKey{}) {
		t.Fatal("accepted a message without thread id")
	}
}

func TestApplyFlagsAggregates(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	m := groupedModel(thr("t_a", 2, 2, a2))
	// Members not known: the change lands on the known one and the counts follow.
	changed := m.applyFlags([]api.MessageID{"a2"}, []api.Flag{api.FlagSeen}, nil)
	if len(changed) != 1 || m.threads[0].UnreadCount != 1 || !hasFlag(m.threads[0].Latest.Flags, api.FlagSeen) || !hasFlag(m.threads[0].Flags, api.FlagSeen) {
		t.Fatalf("incomplete: changed %v thread %+v", changed, m.threads[0])
	}
	if got := m.applyFlags([]api.MessageID{"a2"}, []api.Flag{api.FlagSeen}, nil); len(got) != 0 {
		t.Fatal("unchanged flag reported")
	}
	// Members known: the union is exact.
	a1 := member("a1", "t_a", 1, "alice", api.FlagFlagged)
	m.setExpanded("t_a", true)
	m.setMembers("t_a", thr("t_a", 2, 1, m.threads[0].Latest, api.FlagFlagged, api.FlagSeen), []api.MessageSummary{a1, m.threads[0].Latest})
	changed = m.applyFlags([]api.MessageID{"a1", "a2"}, nil, []api.Flag{api.FlagFlagged, api.FlagSeen})
	if len(changed) != 2 {
		t.Fatalf("changed = %v", changed)
	}
	th := m.threads[0]
	if th.UnreadCount != 2 || hasFlag(th.Flags, api.FlagFlagged) || hasFlag(th.Flags, api.FlagSeen) {
		t.Fatalf("recomputed = %+v", th)
	}
	if !m.flagTarget(m.rows[0]) {
		t.Fatal("flagTarget of an unflagged conversation should flag")
	}
	m.applyFlags([]api.MessageID{"a1"}, []api.Flag{api.FlagFlagged}, nil)
	if m.flagTarget(m.rows[0]) || !hasFlag(m.threads[0].Flags, api.FlagFlagged) {
		t.Fatal("flagTarget of a flagged conversation should unflag")
	}
	if ids := m.rowIDs(m.rows[0]); !reflect.DeepEqual(ids, []api.MessageID{"a1", "a2"}) {
		t.Fatalf("rowIDs = %v", ids)
	}
	if ids := m.rowIDs(m.rows[1]); !reflect.DeepEqual(ids, []api.MessageID{"a1"}) {
		t.Fatalf("member rowIDs = %v", ids)
	}
}

func TestRemoveMessagesAndRestore(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	a1 := member("a1", "t_a", 1, "alice")
	b1 := member("b1", "t_b", 0, "carol")
	m := groupedModel(thr("t_a", 2, 2, a2), thr("t_b", 1, 1, b1))
	// Not all known: the caller has to reload.
	if _, ok := m.removeMessages([]api.MessageID{"a2"}); ok {
		t.Fatal("removed from an incomplete conversation")
	}
	m.setExpanded("t_a", true)
	m.setMembers("t_a", thr("t_a", 2, 2, a2), []api.MessageSummary{a1, a2})

	// One member goes: the conversation shrinks to a single row.
	r, ok := m.removeMessages([]api.MessageID{"a2"})
	if !ok || len(m.threads) != 2 || m.threads[0].MessageCount != 1 || m.threads[0].Latest.ID != "a1" {
		t.Fatalf("after partial removal: %+v", m.threads[0])
	}
	if got := keys(m); !reflect.DeepEqual(got, []listKey{{Thread: "t_a", Message: "a1"}, {Thread: "t_b", Message: "b1"}}) {
		t.Fatalf("rows = %v", got)
	}
	m.restoreRemoval(r)
	if m.threads[0].MessageCount != 2 || len(m.rows) != 4 || !m.expanded["t_a"] {
		t.Fatalf("restore: %+v rows %v", m.threads[0], keys(m))
	}

	// The whole conversation goes, and comes back at its place.
	r, ok = m.removeMessages([]api.MessageID{"a1", "a2"})
	if !ok || len(m.threads) != 1 || m.threads[0].ID != "t_b" || m.total != 1 || m.rowIndexOf(listKey{Thread: "t_a"}) != -1 {
		t.Fatalf("after dropping: %v total %d", keys(m), m.total)
	}
	m.restoreRemoval(r)
	if len(m.threads) != 2 || m.threads[0].ID != "t_a" || m.total != 2 || m.rows[0].Key != (listKey{Thread: "t_a"}) || !m.members["t_a"].complete {
		t.Fatalf("restore dropped: %v total %d", keys(m), m.total)
	}
	if _, ok := m.memberOf["a2"]; !ok {
		t.Fatal("member index not restored")
	}
}

func TestCollapseLoading(t *testing.T) {
	m := groupedModel(thr("t_a", 2, 0, member("a2", "t_a", 2, "bob")), thr("t_b", 3, 0, member("b3", "t_b", 3, "carol")))
	m.setExpanded("t_a", true)
	m.setExpanded("t_b", true)
	m.members["t_a"].fetching = true
	m.setMembers("t_b", thr("t_b", 3, 0, member("b3", "t_b", 3, "carol")), []api.MessageSummary{member("b1", "t_b", 1, "x"), member("b2", "t_b", 2, "y"), member("b3", "t_b", 3, "carol")})
	m.collapseLoading()
	if m.expanded["t_a"] || !m.expanded["t_b"] || m.members["t_a"].fetching || len(m.rows) != 5 {
		t.Fatalf("expanded %v rows %d", m.expanded, len(m.rows))
	}
}

func TestFlatModeAccessors(t *testing.T) {
	var m mailModel
	m.setMessages([]api.MessageSummary{summary("a"), summary("b")}, api.PageInfo{Total: 2})
	if m.rowCount() != 2 || m.rowIndexOf(listKey{Message: "b"}) != 1 || m.keyFor("a") != (listKey{Message: "a"}) {
		t.Fatal("flat accessors")
	}
	r, ok := m.rowAt(1)
	if !ok || r.Thread || r.Message.ID != "b" || r.Key != (listKey{Message: "b"}) {
		t.Fatalf("rowAt = %+v", r)
	}
	if ids := m.rowIDs(r); !reflect.DeepEqual(ids, []api.MessageID{"b"}) {
		t.Fatalf("rowIDs = %v", ids)
	}
	changed := m.applyFlags([]api.MessageID{"a", "zz"}, []api.Flag{api.FlagSeen}, nil)
	if !reflect.DeepEqual(changed, []api.MessageID{"a"}) || !hasFlag(m.messages[0].Flags, api.FlagSeen) {
		t.Fatalf("flat applyFlags = %v", changed)
	}
	o := &api.OutboxInfo{State: api.OutboxQueued}
	m.setOutbox("b", o)
	if m.messages[1].Outbox != o {
		t.Fatal("setOutbox")
	}
	m.clearMessages()
	if m.rowCount() != 0 || m.rows != nil {
		t.Fatal("clear")
	}
}

func TestSummaryThread(t *testing.T) {
	a2 := member("a2", "t_a", 2, "bob")
	a2.HasAttachments = true
	th := thr("t_a", 3, 1, a2, api.FlagFlagged)
	got := summaryThread(th, true, false)
	if got.Count != 3 || got.Unread != 1 || !got.Flagged || !got.HasAttachments || !got.Expanded || got.Loading ||
		got.Subject != "s-a2" || got.Snippet != "p-a2" || !got.Date.Equal(a2.Date) || len(got.Participants) != 1 {
		t.Fatalf("summaryThread = %+v", got)
	}
}

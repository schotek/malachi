// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The grouped message list: one row per conversation of the selected
// folder (thread.list), expandable to the messages the folder holds
// (thread.get). This file is the plain-Go model of it; window/threads.go
// mirrors the rows into the ListBox. The daemon computes the threads and
// their aggregates; what happens here is bookkeeping between two loads: a
// notified arrival, an optimistic flag change, a removal and its undo.
//
// Flat mode (the switch off) keeps mailModel.messages as it always was;
// nothing below is consulted then except the mode-dispatching accessors.

// listKey addresses one row of the message list. A conversation row has
// Message empty; a member row (and a single-message conversation, which
// is shown as a plain row) has both; a flat-mode row has Thread empty.
type listKey struct {
	Thread  api.ThreadID
	Message api.MessageID
}

// listRow is what one row of the list stands for.
type listRow struct {
	Key     listKey
	Thread  bool               // a folded conversation (two or more members)
	Member  bool               // an expanded member, indented under its conversation row
	Message api.MessageSummary // the message the row shows; on a conversation row its newest folder member
	Summary api.ThreadSummary  // conversation rows: the aggregates the row shows
	// Expanded and Loading are conversation-row state: unfolded, and
	// unfolded while thread.get has not answered yet.
	Expanded bool
	Loading  bool
}

// threadMembers is what the window knows of one conversation's folder
// members, oldest first: only the newest one (from thread.list) until
// thread.get answers, then all of them.
type threadMembers struct {
	list     []api.MessageSummary
	complete bool
	fetching bool
	waiters  []func() // run once the members are known (threads.go)
}

// threadSnapshot is one conversation's whole state, for undoing a removal.
type threadSnapshot struct {
	index    int
	summary  api.ThreadSummary
	members  threadMembers
	expanded bool
	dropped  bool // the removal took the last member; the row went away
}

// removal is what removeMessages leaves behind for restoreRemoval.
type removal struct {
	threads []threadSnapshot
}

// setThreads replaces the list with the first page of thread.list. A
// conversation that was listed before with the same shape keeps its
// fetched members, so a reload (sync finished) does not blink an expanded
// conversation through the spinner. Duplicate ids keep the first.
func (m *mailModel) setThreads(list []api.ThreadSummary, page api.PageInfo) {
	prevSummary := make(map[api.ThreadID]api.ThreadSummary, len(m.threads))
	for _, t := range m.threads {
		prevSummary[t.ID] = t
	}
	prevMembers := m.members
	m.threads = m.threads[:0]
	m.tindex = make(map[api.ThreadID]int, len(list))
	m.members = make(map[api.ThreadID]*threadMembers, len(list))
	for _, t := range list {
		if _, dup := m.tindex[t.ID]; dup {
			continue
		}
		m.tindex[t.ID] = len(m.threads)
		m.threads = append(m.threads, t)
		if prev, ok := prevMembers[t.ID]; ok && prev.complete && sameShape(prevSummary[t.ID], t) {
			m.members[t.ID] = prev
		} else {
			m.members[t.ID] = membersFromListing(t)
		}
	}
	m.nextCursor = page.NextCursor
	m.total = page.Total
	m.listErr = nil
	m.reindexMembers()
	m.rebuildRows()
}

// membersFromListing is what thread.list tells of a conversation's folder
// members: the newest one, which for a single-message conversation is all
// of them.
func membersFromListing(t api.ThreadSummary) *threadMembers {
	return &threadMembers{list: []api.MessageSummary{t.Latest}, complete: t.MessageCount <= 1}
}

// sameShape reports whether a conversation's listing has not changed in
// what would invalidate its fetched members.
func sameShape(a, b api.ThreadSummary) bool {
	return a.MessageCount == b.MessageCount && a.UnreadCount == b.UnreadCount &&
		a.LatestDate.Equal(b.LatestDate) && a.Latest.ID == b.Latest.ID
}

// appendThreads adds a further page and returns how many rows it added.
func (m *mailModel) appendThreads(list []api.ThreadSummary, page api.PageInfo) (added int) {
	if m.tindex == nil {
		m.tindex = make(map[api.ThreadID]int, len(list))
		m.members = make(map[api.ThreadID]*threadMembers, len(list))
	}
	for _, t := range list {
		if _, dup := m.tindex[t.ID]; dup {
			continue
		}
		m.tindex[t.ID] = len(m.threads)
		m.threads = append(m.threads, t)
		m.members[t.ID] = membersFromListing(t)
		added++
	}
	m.nextCursor = page.NextCursor
	m.total = page.Total
	m.reindexMembers()
	m.rebuildRows()
	return added
}

// clearThreads forgets the grouped list (folder switch, disconnect).
func (m *mailModel) clearThreads() {
	m.threads = nil
	m.tindex = nil
	m.members = nil
	m.memberOf = nil
	m.expanded = nil
	m.rows = nil
	m.rowIdx = nil
}

// reindexMembers rebuilds the message → conversation map from the member
// lists.
func (m *mailModel) reindexMembers() {
	m.memberOf = make(map[api.MessageID]api.ThreadID, len(m.threads))
	for tid, mem := range m.members {
		for _, s := range mem.list {
			m.memberOf[s.ID] = tid
		}
	}
}

// setExpanded folds or unfolds a conversation row.
func (m *mailModel) setExpanded(tid api.ThreadID, on bool) {
	if m.expanded == nil {
		m.expanded = make(map[api.ThreadID]bool)
	}
	if on {
		m.expanded[tid] = true
	} else {
		delete(m.expanded, tid)
	}
	m.rebuildRows()
}

// setMembers stores the thread.get answer: the folder members, oldest
// first, and the summary as the daemon aggregated it. An empty list means
// the conversation left the folder meanwhile; its row goes.
func (m *mailModel) setMembers(tid api.ThreadID, t api.ThreadSummary, list []api.MessageSummary) {
	i, ok := m.tindex[tid]
	if !ok {
		return
	}
	if len(list) == 0 {
		m.dropThread(tid)
		m.rebuildRows()
		return
	}
	m.threads[i] = t
	m.members[tid] = &threadMembers{list: append([]api.MessageSummary(nil), list...), complete: true}
	m.reindexMembers()
	m.rebuildRows()
}

// dropThread removes a conversation from the list.
func (m *mailModel) dropThread(tid api.ThreadID) {
	i, ok := m.tindex[tid]
	if !ok {
		return
	}
	m.threads = append(m.threads[:i], m.threads[i+1:]...)
	delete(m.members, tid)
	delete(m.expanded, tid)
	m.reindexThreads()
	m.reindexMembers()
	if m.total > 0 {
		m.total--
	}
}

func (m *mailModel) reindexThreads() {
	m.tindex = make(map[api.ThreadID]int, len(m.threads))
	for i, t := range m.threads {
		m.tindex[t.ID] = i
	}
}

// collapseLoading folds every conversation whose members never arrived
// (the connection dropped), so no spinner outlives its reply.
func (m *mailModel) collapseLoading() {
	for tid := range m.expanded {
		if mem := m.members[tid]; mem == nil || !mem.complete {
			delete(m.expanded, tid)
		}
	}
	for _, mem := range m.members {
		mem.fetching = false
	}
	m.rebuildRows()
}

// applyNewMessage folds a notified arrival into the grouped list. A listed
// conversation takes the message (aggregates bumped, moved to the top;
// when selected as a single-message row it unfolds so what the user is
// reading stays a row); an unlisted one is added at the top when the
// filter would list it. The filter is not applied to a listed
// conversation, the same policy as matchesFilter. false means the message
// carries no thread id and the list has to be loaded again.
func (m *mailModel) applyNewMessage(s api.MessageSummary, f api.MessageFilter, selected listKey) bool {
	if s.ThreadID == "" {
		return false
	}
	if _, known := m.memberOf[s.ID]; known {
		return true
	}
	i, listed := m.tindex[s.ThreadID]
	if !listed {
		if !matchesFilter(s, f) {
			return true
		}
		t := api.ThreadSummary{
			ID: s.ThreadID, AccountID: s.AccountID, Subject: s.Subject,
			Participants: append([]api.Address(nil), s.From...), MessageCount: 1,
			LatestDate: s.Date, Latest: s, Snippet: s.Snippet,
			Flags: append([]api.Flag(nil), s.Flags...), HasAttachments: s.HasAttachments,
			FolderIDs: []api.FolderID{s.FolderID},
		}
		if !hasFlag(s.Flags, api.FlagSeen) {
			t.UnreadCount = 1
		}
		m.threads = append([]api.ThreadSummary{t}, m.threads...)
		if m.members == nil {
			m.members = make(map[api.ThreadID]*threadMembers)
		}
		m.members[t.ID] = &threadMembers{list: []api.MessageSummary{s}, complete: true}
		m.reindexThreads()
		m.reindexMembers()
		if m.total >= 0 {
			m.total++
		}
		m.rebuildRows()
		return true
	}
	t := m.threads[i]
	mem := m.members[t.ID]
	// The row the user is reading was this conversation's only message:
	// keep it as a member row rather than jumping to the reply.
	if selected.Thread == t.ID && selected.Message != "" && t.MessageCount <= 1 {
		m.setExpanded(t.ID, true)
	}
	if mem != nil && !s.Date.Before(mem.list[len(mem.list)-1].Date) {
		mem.list = append(mem.list, s)
	} else if mem != nil {
		mem.list = insertByDate(mem.list, s)
	}
	m.memberOf[s.ID] = t.ID
	t.MessageCount++
	if !hasFlag(s.Flags, api.FlagSeen) {
		t.UnreadCount++
	}
	if !s.Date.Before(t.LatestDate) {
		t.LatestDate = s.Date
		t.Latest = s
		t.Snippet = s.Snippet
	}
	t.Flags = unionFlags(t.Flags, s.Flags)
	t.HasAttachments = t.HasAttachments || s.HasAttachments
	t.Participants = frontParticipants(t.Participants, s.From)
	// To the top.
	m.threads = append(m.threads[:i], m.threads[i+1:]...)
	m.threads = append([]api.ThreadSummary{t}, m.threads...)
	m.reindexThreads()
	m.rebuildRows()
	return true
}

// insertByDate places s among list (oldest first) by date.
func insertByDate(list []api.MessageSummary, s api.MessageSummary) []api.MessageSummary {
	at := len(list)
	for i, x := range list {
		if s.Date.Before(x.Date) {
			at = i
			break
		}
	}
	list = append(list, api.MessageSummary{})
	copy(list[at+1:], list[at:])
	list[at] = s
	return list
}

// frontParticipants moves the senders of the newest message to the front
// of a participant list, each address once, compared case-insensitively.
func frontParticipants(list []api.Address, from []api.Address) []api.Address {
	out := make([]api.Address, 0, len(list)+len(from))
	seen := map[string]bool{}
	for _, a := range append(append([]api.Address(nil), from...), list...) {
		key := strings.ToLower(strings.TrimSpace(a.Address))
		if key == "" {
			key = "name:" + strings.ToLower(strings.TrimSpace(a.Name))
		}
		if key == "name:" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
		if len(out) == api.MaxThreadParticipants {
			break
		}
	}
	return out
}

// unionFlags returns a ∪ b in a's order, then b's.
func unionFlags(a, b []api.Flag) []api.Flag {
	out := append([]api.Flag(nil), a...)
	for _, f := range b {
		if !hasFlag(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// applyFlagChange returns flags with set added and clear removed (a flag
// in both ends up set) and whether anything changed.
func applyFlagChange(flags, set, clear []api.Flag) ([]api.Flag, bool) {
	changed := false
	out := make([]api.Flag, 0, len(flags)+len(set))
	for _, f := range flags {
		if hasFlag(clear, f) && !hasFlag(set, f) {
			changed = true
			continue
		}
		out = append(out, f)
	}
	for _, f := range set {
		if !hasFlag(out, f) {
			out = append(out, f)
			changed = true
		}
	}
	return out, changed
}

// applyFlags changes the flags of the given messages in whichever mode
// the list is in and returns the ids that actually changed. In grouped
// mode the conversation's aggregates follow: recomputed from the members
// when they are all known, adjusted by the change otherwise.
func (m *mailModel) applyFlags(ids []api.MessageID, set, clear []api.Flag) (changed []api.MessageID) {
	if !m.grouped {
		for _, id := range ids {
			if m.updateFlags(id, set, clear) {
				changed = append(changed, id)
			}
		}
		return changed
	}
	touched := map[api.ThreadID]bool{}
	for _, id := range ids {
		tid, ok := m.memberOf[id]
		if !ok {
			continue
		}
		mem := m.members[tid]
		for j := range mem.list {
			if mem.list[j].ID != id {
				continue
			}
			flags, did := applyFlagChange(mem.list[j].Flags, set, clear)
			if !did {
				break
			}
			wasUnread := !hasFlag(mem.list[j].Flags, api.FlagSeen)
			mem.list[j].Flags = flags
			changed = append(changed, id)
			touched[tid] = true
			if i, ok := m.tindex[tid]; ok && !mem.complete {
				t := &m.threads[i]
				if t.Latest.ID == id {
					t.Latest.Flags = flags
				}
				nowUnread := !hasFlag(flags, api.FlagSeen)
				switch {
				case wasUnread && !nowUnread && t.UnreadCount > 0:
					t.UnreadCount--
				case !wasUnread && nowUnread && t.UnreadCount < t.MessageCount:
					t.UnreadCount++
				}
				t.Flags = unionFlags(t.Flags, set)
				if t.MessageCount <= 1 {
					t.Flags = flags
				}
			}
			break
		}
	}
	for tid := range touched {
		m.recomputeThread(tid)
	}
	if len(changed) > 0 {
		m.rebuildRows()
	}
	return changed
}

// recomputeThread derives a conversation's aggregates from its members
// when they are all known.
func (m *mailModel) recomputeThread(tid api.ThreadID) {
	i, ok := m.tindex[tid]
	mem := m.members[tid]
	if !ok || mem == nil || !mem.complete || len(mem.list) == 0 {
		return
	}
	t := &m.threads[i]
	t.MessageCount = len(mem.list)
	t.UnreadCount = 0
	t.HasAttachments = false
	t.Flags = nil
	var senders []api.Address // newest message first
	for j := len(mem.list) - 1; j >= 0; j-- {
		s := mem.list[j]
		if !hasFlag(s.Flags, api.FlagSeen) {
			t.UnreadCount++
		}
		t.HasAttachments = t.HasAttachments || s.HasAttachments
		t.Flags = unionFlags(t.Flags, s.Flags)
		senders = append(senders, s.From...)
	}
	if t.Flags == nil {
		t.Flags = []api.Flag{}
	}
	t.Participants = frontParticipants(nil, senders)
	latest := mem.list[len(mem.list)-1]
	t.Latest = latest
	t.LatestDate = latest.Date
	t.Snippet = latest.Snippet
}

// removeMessages drops the given messages from the grouped list and
// returns what restoreRemoval needs. A conversation whose members are not
// all known cannot be edited in place: ok is false and the caller loads
// the list again.
func (m *mailModel) removeMessages(ids []api.MessageID) (removal, bool) {
	byThread := map[api.ThreadID][]api.MessageID{}
	var order []api.ThreadID
	for _, id := range ids {
		tid, ok := m.memberOf[id]
		if !ok {
			continue
		}
		if _, seen := byThread[tid]; !seen {
			order = append(order, tid)
		}
		byThread[tid] = append(byThread[tid], id)
	}
	for _, tid := range order {
		if mem := m.members[tid]; mem == nil || !mem.complete {
			return removal{}, false
		}
	}
	var r removal
	for _, tid := range order {
		i := m.tindex[tid]
		mem := m.members[tid]
		snap := threadSnapshot{index: i, summary: m.threads[i], expanded: m.expanded[tid],
			members: threadMembers{list: append([]api.MessageSummary(nil), mem.list...), complete: true}}
		gone := byThread[tid]
		kept := mem.list[:0:0]
		for _, s := range mem.list {
			drop := false
			for _, id := range gone {
				drop = drop || s.ID == id
			}
			if !drop {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			snap.dropped = true
			r.threads = append(r.threads, snap)
			m.dropThread(tid)
			continue
		}
		mem.list = kept
		r.threads = append(r.threads, snap)
		m.recomputeThread(tid)
	}
	m.reindexMembers()
	m.rebuildRows()
	return r, true
}

// restoreRemoval puts the conversations of a failed removal back as they
// were.
func (m *mailModel) restoreRemoval(r removal) {
	for _, snap := range r.threads {
		tid := snap.summary.ID
		if snap.dropped {
			at := snap.index
			if at > len(m.threads) {
				at = len(m.threads)
			}
			m.threads = append(m.threads, api.ThreadSummary{})
			copy(m.threads[at+1:], m.threads[at:])
			m.threads[at] = snap.summary
			m.reindexThreads()
			if m.total >= 0 {
				m.total++
			}
		} else if i, ok := m.tindex[tid]; ok {
			m.threads[i] = snap.summary
		} else {
			continue
		}
		mem := snap.members
		m.members[tid] = &mem
		if snap.expanded {
			if m.expanded == nil {
				m.expanded = make(map[api.ThreadID]bool)
			}
			m.expanded[tid] = true
		}
	}
	m.reindexMembers()
	m.rebuildRows()
}

// rebuildRows lays the grouped list out: a conversation with one member
// is a plain row; one with more is a conversation row, followed by its
// members (oldest first) when unfolded and known.
func (m *mailModel) rebuildRows() {
	m.rows = m.rows[:0]
	for _, t := range m.threads {
		mem := m.members[t.ID]
		latest := t.Latest
		if mem != nil && len(mem.list) > 0 {
			latest = mem.list[len(mem.list)-1]
		}
		if t.MessageCount <= 1 {
			m.rows = append(m.rows, listRow{Key: listKey{Thread: t.ID, Message: latest.ID}, Message: latest})
			continue
		}
		exp := m.expanded[t.ID]
		m.rows = append(m.rows, listRow{Key: listKey{Thread: t.ID}, Thread: true, Summary: t, Message: latest,
			Expanded: exp, Loading: exp && (mem == nil || !mem.complete)})
		if exp && mem != nil && mem.complete {
			for _, s := range mem.list {
				m.rows = append(m.rows, listRow{Key: listKey{Thread: t.ID, Message: s.ID}, Member: true, Message: s})
			}
		}
	}
	m.rowIdx = make(map[listKey]int, len(m.rows))
	for i, r := range m.rows {
		m.rowIdx[r.Key] = i
	}
}

// rowAt is the row at list position idx in either mode.
func (m *mailModel) rowAt(idx int) (listRow, bool) {
	if m.grouped {
		if idx < 0 || idx >= len(m.rows) {
			return listRow{}, false
		}
		return m.rows[idx], true
	}
	s, ok := m.messageAt(idx)
	if !ok {
		return listRow{}, false
	}
	return listRow{Key: listKey{Message: s.ID}, Message: s}, true
}

// rowCount is how many rows the list has in either mode.
func (m *mailModel) rowCount() int {
	if m.grouped {
		return len(m.rows)
	}
	return len(m.messages)
}

// rowIndexOf is the list position of key, -1 when absent.
func (m *mailModel) rowIndexOf(k listKey) int {
	if m.grouped {
		if i, ok := m.rowIdx[k]; ok {
			return i
		}
		return -1
	}
	if i, ok := m.index[k.Message]; ok {
		return i
	}
	return -1
}

// keyFor is the row key a message has in the current mode. In grouped
// mode a conversation's newest member has a row of its own only while the
// conversation is unfolded (or has one member); the conversation row is
// keyed by the thread alone.
func (m *mailModel) keyFor(id api.MessageID) listKey {
	if !m.grouped {
		return listKey{Message: id}
	}
	return listKey{Thread: m.memberOf[id], Message: id}
}

// threadOf is the conversation row's key for a message, if listed.
func (m *mailModel) threadKeyOf(id api.MessageID) (listKey, bool) {
	tid, ok := m.memberOf[id]
	if !ok {
		return listKey{}, false
	}
	return listKey{Thread: tid}, true
}

// rowIDs is every message a row stands for: all known folder members of
// a conversation row (nil while they are not all known), else the one
// message.
func (m *mailModel) rowIDs(r listRow) []api.MessageID {
	if !r.Thread {
		return []api.MessageID{r.Message.ID}
	}
	mem := m.members[r.Key.Thread]
	if mem == nil || !mem.complete {
		return nil
	}
	out := make([]api.MessageID, 0, len(mem.list))
	for _, s := range mem.list {
		out = append(out, s.ID)
	}
	return out
}

// rowMessages is rowIDs with the summaries.
func (m *mailModel) rowMessages(r listRow) []api.MessageSummary {
	if !r.Thread {
		return []api.MessageSummary{r.Message}
	}
	mem := m.members[r.Key.Thread]
	if mem == nil || !mem.complete {
		return nil
	}
	return append([]api.MessageSummary(nil), mem.list...)
}

// flagTarget is the flagged state toggle-flag moves a row to: a
// conversation with no flagged member gets every member flagged, one with
// any flagged member gets them all unflagged.
func (m *mailModel) flagTarget(r listRow) bool {
	if r.Thread {
		return !hasFlag(r.Summary.Flags, api.FlagFlagged)
	}
	return !hasFlag(r.Message.Flags, api.FlagFlagged)
}

// setOutbox replaces the outbox state of a listed message (flat mode; the
// outbox folder is never grouped).
func (m *mailModel) setOutbox(id api.MessageID, o *api.OutboxInfo) {
	if idx, ok := m.index[id]; ok {
		m.messages[idx].Outbox = o
	}
}

// summaryThread projects a conversation onto what its row displays.
func summaryThread(t api.ThreadSummary, expanded, loading bool) widget.Thread {
	return widget.Thread{
		Participants:   t.Participants,
		Subject:        t.Subject,
		Snippet:        t.Snippet,
		Date:           t.LatestDate,
		Count:          t.MessageCount,
		Unread:         t.UnreadCount,
		Flagged:        hasFlag(t.Flags, api.FlagFlagged),
		HasAttachments: t.HasAttachments,
		Expanded:       expanded,
		Loading:        loading,
	}
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The grouped message list (ui/internal/window/thread_model.go): one row
// per conversation of the selected folder (thread.list), expandable to the
// messages the folder holds (thread.get). The daemon computes the threads
// and their aggregates; what happens here is bookkeeping between two loads:
// a notified arrival, an optimistic flag change, a removal and its undo.
//
// Flat mode (the switch off) keeps `MailModel.messages` as it always was;
// nothing below is consulted then except the mode-dispatching accessors.

/// Addresses one row of the message list (thread_model.go `listKey`). A
/// conversation row has `message` nil; a member row (and a single-message
/// conversation, which is shown as a plain row) has both; a flat-mode row
/// has `thread` nil.
public struct ListKey: Hashable, Sendable {
    public var thread: ThreadID?
    public var message: MessageID?

    public init(thread: ThreadID? = nil, message: MessageID? = nil) {
        self.thread = thread
        self.message = message
    }
}

/// What one row of the list stands for (thread_model.go `listRow`).
public struct ListRow: Sendable, Equatable {
    public var key: ListKey
    /// A folded conversation (two or more members).
    public var thread: Bool
    /// An expanded member, indented under its conversation row.
    public var member: Bool
    /// The message the row shows; on a conversation row its newest folder
    /// member.
    public var message: MessageSummary
    /// Conversation rows: the aggregates the row shows.
    public var summary: ThreadSummary?
    /// Conversation-row state: unfolded, and unfolded while thread.get has
    /// not answered yet.
    public var expanded: Bool
    public var loading: Bool

    public init(
        key: ListKey, thread: Bool = false, member: Bool = false, message: MessageSummary,
        summary: ThreadSummary? = nil, expanded: Bool = false, loading: Bool = false
    ) {
        self.key = key
        self.thread = thread
        self.member = member
        self.message = message
        self.summary = summary
        self.expanded = expanded
        self.loading = loading
    }
}

/// What the window knows of one conversation's folder members, oldest
/// first (thread_model.go `threadMembers`): only the newest one (from
/// thread.list) until thread.get answers, then all of them. The waiters of
/// the Go struct (run once the members are known) live in the controller.
public struct ThreadMembers: Sendable, Equatable {
    public var list: [MessageSummary]
    public var complete: Bool
    /// thread.get is in flight.
    public var fetching: Bool

    public init(list: [MessageSummary], complete: Bool, fetching: Bool = false) {
        self.list = list
        self.complete = complete
        self.fetching = fetching
    }
}

/// One conversation's whole state, for undoing a removal (thread_model.go
/// `threadSnapshot`).
public struct ThreadSnapshot: Sendable, Equatable {
    public var index: Int
    public var summary: ThreadSummary
    public var members: ThreadMembers
    public var expanded: Bool
    /// The removal took the last member; the row went away.
    public var dropped: Bool

    public init(index: Int, summary: ThreadSummary, members: ThreadMembers, expanded: Bool, dropped: Bool = false) {
        self.index = index
        self.summary = summary
        self.members = members
        self.expanded = expanded
        self.dropped = dropped
    }
}

/// What `removeMessages` leaves behind for `restoreRemoval` (thread_model.go
/// `removal`).
public struct Removal: Sendable, Equatable {
    public var threads: [ThreadSnapshot]

    public init(threads: [ThreadSnapshot] = []) {
        self.threads = threads
    }
}

/// What a conversation row displays (widget/message_row.go `Thread`), a
/// projection of a thread summary over the members of the listed folder.
public struct RowThread: Sendable, Equatable {
    /// Newest first, as the daemon sent them.
    public var participants: [Address]
    public var subject: String
    public var snippet: String
    public var date: Date
    /// Members in the folder.
    public var count: Int
    public var unread: Int
    public var flagged: Bool
    public var hasAttachments: Bool
    public var expanded: Bool
    /// Unfolded, members not answered yet.
    public var loading: Bool

    public init(
        participants: [Address], subject: String, snippet: String, date: Date, count: Int, unread: Int,
        flagged: Bool, hasAttachments: Bool, expanded: Bool, loading: Bool
    ) {
        self.participants = participants
        self.subject = subject
        self.snippet = snippet
        self.date = date
        self.count = count
        self.unread = unread
        self.flagged = flagged
        self.hasAttachments = hasAttachments
        self.expanded = expanded
        self.loading = loading
    }
}

/// Projects a conversation onto what its row displays (thread_model.go
/// `summaryThread`).
public func summaryThread(_ t: ThreadSummary, expanded: Bool, loading: Bool) -> RowThread {
    RowThread(
        participants: t.participants, subject: t.subject, snippet: t.snippet, date: t.latestDate,
        count: t.messageCount, unread: t.unreadCount, flagged: hasFlag(t.flags, .flagged),
        hasAttachments: t.hasAttachments, expanded: expanded, loading: loading
    )
}

/// What thread.list tells of a conversation's folder members: the newest
/// one, which for a single-message conversation is all of them
/// (thread_model.go `membersFromListing`).
public func membersFromListing(_ t: ThreadSummary) -> ThreadMembers {
    ThreadMembers(list: [t.latest], complete: t.messageCount <= 1)
}

/// Whether a conversation's listing has not changed in what would
/// invalidate its fetched members (thread_model.go `sameShape`).
public func sameShape(_ a: ThreadSummary, _ b: ThreadSummary) -> Bool {
    a.messageCount == b.messageCount && a.unreadCount == b.unreadCount
        && a.latestDate == b.latestDate && a.latest.id == b.latest.id
}

/// Places `s` among `list` (oldest first) by date (thread_model.go
/// `insertByDate`).
public func insertByDate(_ list: [MessageSummary], _ s: MessageSummary) -> [MessageSummary] {
    var out = list
    let at = list.firstIndex { s.date < $0.date } ?? list.count
    out.insert(s, at: at)
    return out
}

/// Moves the senders of the newest message to the front of a participant
/// list, each address once, compared case-insensitively, capped at
/// `API.Limits.maxThreadParticipants` (thread_model.go `frontParticipants`).
public func frontParticipants(_ list: [Address], _ from: [Address]) -> [Address] {
    var out: [Address] = []
    var seen = Set<String>()
    for a in from + list {
        var key = a.address.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if key.isEmpty {
            key = "name:" + (a.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        }
        if key == "name:" || seen.contains(key) {
            continue
        }
        seen.insert(key)
        out.append(a)
        if out.count == API.Limits.maxThreadParticipants {
            break
        }
    }
    return out
}

/// a ∪ b in a's order, then b's (thread_model.go `unionFlags`).
public func unionFlags(_ a: [Flag], _ b: [Flag]) -> [Flag] {
    var out = a
    for f in b where !hasFlag(out, f) {
        out.append(f)
    }
    return out
}

/// `flags` with `set` added and `clear` removed (a flag in both ends up
/// set) and whether anything changed (thread_model.go `applyFlagChange`).
public func applyFlagChange(_ flags: [Flag], set: [Flag], clear: [Flag]) -> (flags: [Flag], changed: Bool) {
    var changed = false
    var out: [Flag] = []
    out.reserveCapacity(flags.count + set.count)
    for f in flags {
        if hasFlag(clear, f) && !hasFlag(set, f) {
            changed = true
            continue
        }
        out.append(f)
    }
    for f in set where !hasFlag(out, f) {
        out.append(f)
        changed = true
    }
    return (out, changed)
}

extension MailModel {
    // MARK: Loading

    /// Replaces the list with the first page of thread.list. A conversation
    /// that was listed before with the same shape keeps its fetched
    /// members, so a reload (sync finished) does not blink an expanded
    /// conversation through the spinner. Duplicate ids keep the first.
    public mutating func setThreads(_ list: [ThreadSummary], page: PageInfo) {
        var prevSummary: [ThreadID: ThreadSummary] = [:]
        for t in threads {
            prevSummary[t.id] = t
        }
        let prevMembers = members
        threads = []
        tindex = [:]
        members = [:]
        for t in list {
            if tindex[t.id] != nil {
                continue
            }
            tindex[t.id] = threads.count
            threads.append(t)
            if let prev = prevMembers[t.id], prev.complete, let ps = prevSummary[t.id], sameShape(ps, t) {
                members[t.id] = prev
            } else {
                members[t.id] = membersFromListing(t)
            }
        }
        nextCursor = page.nextCursor
        total = page.total
        listErr = nil
        reindexMembers()
        rebuildRows()
    }

    /// Adds a further page and returns how many rows it added.
    @discardableResult
    public mutating func appendThreads(_ list: [ThreadSummary], page: PageInfo) -> Int {
        var added = 0
        for t in list {
            if tindex[t.id] != nil {
                continue
            }
            tindex[t.id] = threads.count
            threads.append(t)
            members[t.id] = membersFromListing(t)
            added += 1
        }
        nextCursor = page.nextCursor
        total = page.total
        reindexMembers()
        rebuildRows()
        return added
    }

    /// Forgets the grouped list (folder switch, disconnect).
    public mutating func clearThreads() {
        threads = []
        tindex = [:]
        members = [:]
        memberOf = [:]
        expanded = []
        rows = []
        rowIdx = [:]
    }

    /// Rebuilds the message → conversation map from the member lists.
    public mutating func reindexMembers() {
        memberOf = [:]
        memberOf.reserveCapacity(threads.count)
        for (tid, mem) in members {
            for s in mem.list {
                memberOf[s.id] = tid
            }
        }
    }

    private mutating func reindexThreads() {
        tindex = [:]
        tindex.reserveCapacity(threads.count)
        for (i, t) in threads.enumerated() {
            tindex[t.id] = i
        }
    }

    // MARK: Folding

    /// Folds or unfolds a conversation row.
    public mutating func setExpanded(_ tid: ThreadID, _ on: Bool) {
        if on {
            expanded.insert(tid)
        } else {
            expanded.remove(tid)
        }
        rebuildRows()
    }

    /// Stores the thread.get answer: the folder members, oldest first, and
    /// the summary as the daemon aggregated it. An empty list means the
    /// conversation left the folder meanwhile; its row goes.
    public mutating func setMembers(_ tid: ThreadID, _ t: ThreadSummary, _ list: [MessageSummary]) {
        guard let i = tindex[tid] else {
            return
        }
        if list.isEmpty {
            dropThread(tid)
            rebuildRows()
            return
        }
        threads[i] = t
        members[tid] = ThreadMembers(list: list, complete: true)
        reindexMembers()
        rebuildRows()
    }

    /// Removes a conversation from the list.
    public mutating func dropThread(_ tid: ThreadID) {
        guard let i = tindex[tid] else {
            return
        }
        threads.remove(at: i)
        members[tid] = nil
        expanded.remove(tid)
        reindexThreads()
        reindexMembers()
        if total > 0 {
            total -= 1
        }
    }

    /// Folds every conversation whose members never arrived (the connection
    /// dropped), so no spinner outlives its reply.
    public mutating func collapseLoading() {
        for tid in expanded {
            if let mem = members[tid], mem.complete {
                continue
            }
            expanded.remove(tid)
        }
        for tid in members.keys {
            members[tid]?.fetching = false
        }
        rebuildRows()
    }

    // MARK: Changes between loads

    /// Folds a notified arrival into the grouped list. A listed conversation
    /// takes the message (aggregates bumped, moved to the top; when selected
    /// as a single-message row it unfolds so what the user is reading stays
    /// a row); an unlisted one is added at the top when the filter would
    /// list it. The filter is not applied to a listed conversation, the same
    /// policy as `matchesFilter`. False means the message carries no thread
    /// id and the list has to be loaded again.
    @discardableResult
    public mutating func applyNewMessage(_ s: MessageSummary, filter f: MessageFilter, selected: ListKey) -> Bool {
        guard let threadID = s.threadId else {
            return false
        }
        if memberOf[s.id] != nil {
            return true
        }
        guard let i = tindex[threadID] else {
            if !matchesFilter(s, f) {
                return true
            }
            let t = ThreadSummary(
                id: threadID, accountId: s.accountId, subject: s.subject, participants: s.from,
                messageCount: 1, unreadCount: hasFlag(s.flags, .seen) ? 0 : 1, latestDate: s.date, latest: s,
                snippet: s.snippet, flags: s.flags, hasAttachments: s.hasAttachments, folderIds: [s.folderId]
            )
            threads.insert(t, at: 0)
            members[t.id] = ThreadMembers(list: [s], complete: true)
            reindexThreads()
            reindexMembers()
            if total >= 0 {
                total += 1
            }
            rebuildRows()
            return true
        }
        var t = threads[i]
        // The row the user is reading was this conversation's only message:
        // keep it as a member row rather than jumping to the reply.
        if selected.thread == t.id, selected.message != nil, t.messageCount <= 1 {
            setExpanded(t.id, true)
        }
        if var mem = members[t.id] {
            if let last = mem.list.last, s.date >= last.date {
                mem.list.append(s)
            } else {
                mem.list = insertByDate(mem.list, s)
            }
            members[t.id] = mem
        }
        memberOf[s.id] = t.id
        t.messageCount += 1
        if !hasFlag(s.flags, .seen) {
            t.unreadCount += 1
        }
        if s.date >= t.latestDate {
            t.latestDate = s.date
            t.latest = s
            t.snippet = s.snippet
        }
        t.flags = unionFlags(t.flags, s.flags)
        t.hasAttachments = t.hasAttachments || s.hasAttachments
        t.participants = frontParticipants(t.participants, s.from)
        // To the top.
        threads.remove(at: i)
        threads.insert(t, at: 0)
        reindexThreads()
        rebuildRows()
        return true
    }

    /// Changes the flags of the given messages in whichever mode the list
    /// is in and returns the ids that actually changed. In grouped mode the
    /// conversation's aggregates follow: recomputed from the members when
    /// they are all known, adjusted by the change otherwise.
    @discardableResult
    public mutating func applyFlags(_ ids: [MessageID], set: [Flag] = [], clear: [Flag] = []) -> [MessageID] {
        var changed: [MessageID] = []
        if !grouped {
            for id in ids where updateFlags(id, set: set, clear: clear) {
                changed.append(id)
            }
            return changed
        }
        var touched = Set<ThreadID>()
        for id in ids {
            guard let tid = memberOf[id], var mem = members[tid] else {
                continue
            }
            for j in mem.list.indices where mem.list[j].id == id {
                let (flags, did) = applyFlagChange(mem.list[j].flags, set: set, clear: clear)
                if !did {
                    break
                }
                let wasUnread = !hasFlag(mem.list[j].flags, .seen)
                mem.list[j].flags = flags
                members[tid] = mem
                changed.append(id)
                touched.insert(tid)
                if let i = tindex[tid], !mem.complete {
                    if threads[i].latest.id == id {
                        threads[i].latest.flags = flags
                    }
                    let nowUnread = !hasFlag(flags, .seen)
                    if wasUnread, !nowUnread, threads[i].unreadCount > 0 {
                        threads[i].unreadCount -= 1
                    } else if !wasUnread, nowUnread, threads[i].unreadCount < threads[i].messageCount {
                        threads[i].unreadCount += 1
                    }
                    threads[i].flags = unionFlags(threads[i].flags, set)
                    if threads[i].messageCount <= 1 {
                        threads[i].flags = flags
                    }
                }
                break
            }
        }
        for tid in touched {
            recomputeThread(tid)
        }
        if !changed.isEmpty {
            rebuildRows()
        }
        return changed
    }

    /// Derives a conversation's aggregates from its members when they are
    /// all known.
    public mutating func recomputeThread(_ tid: ThreadID) {
        guard let i = tindex[tid], let mem = members[tid], mem.complete, !mem.list.isEmpty else {
            return
        }
        var t = threads[i]
        t.messageCount = mem.list.count
        t.unreadCount = 0
        t.hasAttachments = false
        t.flags = []
        var senders: [Address] = [] // newest message first
        for s in mem.list.reversed() {
            if !hasFlag(s.flags, .seen) {
                t.unreadCount += 1
            }
            t.hasAttachments = t.hasAttachments || s.hasAttachments
            t.flags = unionFlags(t.flags, s.flags)
            senders.append(contentsOf: s.from)
        }
        t.participants = frontParticipants([], senders)
        let latest = mem.list[mem.list.count - 1]
        t.latest = latest
        t.latestDate = latest.date
        t.snippet = latest.snippet
        threads[i] = t
    }

    /// Drops the given messages from the grouped list and returns what
    /// `restoreRemoval` needs. A conversation whose members are not all
    /// known cannot be edited in place: nil, and the caller loads the list
    /// again.
    public mutating func removeMessages(_ ids: [MessageID]) -> Removal? {
        var byThread: [ThreadID: [MessageID]] = [:]
        var order: [ThreadID] = []
        for id in ids {
            guard let tid = memberOf[id] else {
                continue
            }
            if byThread[tid] == nil {
                order.append(tid)
            }
            byThread[tid, default: []].append(id)
        }
        for tid in order {
            guard let mem = members[tid], mem.complete else {
                return nil
            }
        }
        var r = Removal()
        for tid in order {
            guard let i = tindex[tid], let mem = members[tid] else {
                continue
            }
            var snap = ThreadSnapshot(
                index: i, summary: threads[i], members: ThreadMembers(list: mem.list, complete: true),
                expanded: expanded.contains(tid)
            )
            let gone = Set(byThread[tid] ?? [])
            let kept = mem.list.filter { !gone.contains($0.id) }
            if kept.isEmpty {
                snap.dropped = true
                r.threads.append(snap)
                dropThread(tid)
                continue
            }
            members[tid]?.list = kept
            r.threads.append(snap)
            recomputeThread(tid)
        }
        reindexMembers()
        rebuildRows()
        return r
    }

    /// Puts the conversations of a failed removal back as they were.
    public mutating func restoreRemoval(_ r: Removal) {
        for snap in r.threads {
            let tid = snap.summary.id
            if snap.dropped {
                let at = min(snap.index, threads.count)
                threads.insert(snap.summary, at: at)
                reindexThreads()
                if total >= 0 {
                    total += 1
                }
            } else if let i = tindex[tid] {
                threads[i] = snap.summary
            } else {
                continue
            }
            members[tid] = snap.members
            if snap.expanded {
                expanded.insert(tid)
            }
        }
        reindexMembers()
        rebuildRows()
    }

    // MARK: Rows

    /// Lays the grouped list out: a conversation with one member is a plain
    /// row; one with more is a conversation row, followed by its members
    /// (oldest first) when unfolded and known.
    public mutating func rebuildRows() {
        rows = []
        for t in threads {
            let mem = members[t.id]
            var latest = t.latest
            if let mem, let last = mem.list.last {
                latest = last
            }
            if t.messageCount <= 1 {
                rows.append(ListRow(key: ListKey(thread: t.id, message: latest.id), message: latest))
                continue
            }
            let exp = expanded.contains(t.id)
            let complete = mem?.complete ?? false
            rows.append(ListRow(
                key: ListKey(thread: t.id), thread: true, message: latest, summary: t,
                expanded: exp, loading: exp && !complete
            ))
            if exp, let mem, mem.complete {
                for s in mem.list {
                    rows.append(ListRow(key: ListKey(thread: t.id, message: s.id), member: true, message: s))
                }
            }
        }
        rowIdx = [:]
        rowIdx.reserveCapacity(rows.count)
        for (i, r) in rows.enumerated() {
            rowIdx[r.key] = i
        }
    }

    /// The row at list position `idx` in either mode.
    public func rowAt(_ idx: Int) -> ListRow? {
        if grouped {
            guard idx >= 0, idx < rows.count else {
                return nil
            }
            return rows[idx]
        }
        guard let s = messageAt(idx) else {
            return nil
        }
        return ListRow(key: ListKey(message: s.id), message: s)
    }

    /// How many rows the list has in either mode.
    public var rowCount: Int {
        grouped ? rows.count : messages.count
    }

    /// The list position of `k`, -1 when absent.
    public func rowIndexOf(_ k: ListKey) -> Int {
        if grouped {
            return rowIdx[k] ?? -1
        }
        guard let id = k.message, let i = index[id] else {
            return -1
        }
        return i
    }

    /// The row key a message has in the current mode. In grouped mode a
    /// conversation's newest member has a row of its own only while the
    /// conversation is unfolded (or has one member); the conversation row
    /// is keyed by the thread alone.
    public func keyFor(_ id: MessageID) -> ListKey {
        if !grouped {
            return ListKey(message: id)
        }
        return ListKey(thread: memberOf[id], message: id)
    }

    /// Every message a row stands for: all known folder members of a
    /// conversation row (nil while they are not all known), else the one
    /// message.
    public func rowIDs(_ r: ListRow) -> [MessageID]? {
        rowMessages(r)?.map(\.id)
    }

    /// `rowIDs` with the summaries.
    public func rowMessages(_ r: ListRow) -> [MessageSummary]? {
        if !r.thread {
            return [r.message]
        }
        guard let tid = r.key.thread, let mem = members[tid], mem.complete else {
            return nil
        }
        return mem.list
    }

    /// The flagged state toggle-flag moves a row to: a conversation with no
    /// flagged member gets every member flagged, one with any flagged member
    /// gets them all unflagged.
    public func flagTarget(_ r: ListRow) -> Bool {
        if r.thread, let summary = r.summary {
            return !hasFlag(summary.flags, .flagged)
        }
        return !hasFlag(r.message.flags, .flagged)
    }
}

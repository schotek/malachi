// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/thread_model_test.go.

/// 2026-09-01T10:00:00Z (thread_model_test.go `threadBase`).
let threadBase = Date(timeIntervalSince1970: 1_788_256_800)

/// A folder member of conversation `tid`, dated `n` hours after
/// `threadBase`, from the given sender (thread_model_test.go `member`). A
/// nil `tid` is a message an older daemon has not linked yet.
func member(_ id: String, _ tid: String?, _ n: Int, _ from: String, _ flags: Flag...) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "acc", folderId: "f_inbox", threadId: tid.map { ThreadID($0) },
        from: [Address(name: from, address: from + "@example.invalid")], subject: "s-" + id,
        date: threadBase.addingTimeInterval(Double(n) * 3600), snippet: "p-" + id, flags: flags,
        hasAttachments: false, size: 0
    )
}

/// A conversation summary over `latest` and `count` members, of which
/// `unread` are unread (thread_model_test.go `thr`).
func thr(_ tid: String, _ count: Int, _ unread: Int, _ latest: MessageSummary, _ flags: Flag...) -> ThreadSummary {
    ThreadSummary(
        id: ThreadID(tid), accountId: "acc", subject: latest.subject, participants: latest.from,
        messageCount: count, unreadCount: unread, latestDate: latest.date, latest: latest, snippet: latest.snippet,
        flags: flags, hasAttachments: latest.hasAttachments, folderIds: ["f_inbox"]
    )
}

func groupedModel(_ threads: ThreadSummary...) -> MailModel {
    var m = MailModel(grouped: true, listFilter: .all)
    m.setThreads(threads, page: PageInfo(total: threads.count))
    return m
}

func keys(_ m: MailModel) -> [ListKey] {
    m.rows.map(\.key)
}

@Suite struct ThreadModelTests {
    @Test func rebuildRowsCollapsedAndExpanded() throws {
        let a2 = member("a2", "t_a", 2, "bob")
        let b1 = member("b1", "t_b", 1, "carol")
        var m = groupedModel(thr("t_a", 2, 1, a2), thr("t_b", 1, 0, b1, .seen))
        var want = [ListKey(thread: "t_a"), ListKey(thread: "t_b", message: "b1")]
        #expect(keys(m) == want)
        let row0 = m.rows[0]
        #expect(row0.thread)
        #expect(row0.message.id == "a2")
        #expect(row0.summary?.messageCount == 2)
        #expect(!row0.expanded)
        #expect(!row0.loading)
        let row1 = m.rows[1]
        #expect(!row1.thread)
        #expect(!row1.member)
        #expect(row1.message.id == "b1")

        // Unfolded before the members are known: the row spins, no members.
        m.setExpanded("t_a", true)
        #expect(keys(m) == want)
        #expect(m.rows[0].loading)
        #expect(m.rows[0].expanded)
        let a1 = member("a1", "t_a", 1, "alice")
        m.setMembers("t_a", thr("t_a", 2, 1, a2), [a1, a2])
        want = [ListKey(thread: "t_a"), ListKey(thread: "t_a", message: "a1"), ListKey(thread: "t_a", message: "a2"), ListKey(thread: "t_b", message: "b1")]
        #expect(keys(m) == want)
        #expect(!m.rows[0].loading)
        #expect(m.rows[1].member)
        let found = try #require(m.message("a1"))
        #expect(found.summary.id == "a1")
        #expect(found.index == 1)
        #expect(m.rowIndexOf(ListKey(thread: "t_b", message: "b1")) == 3)
        #expect(m.rowIndexOf(ListKey(message: "zz")) == -1)
        #expect(m.rowCount == 4)
        m.setExpanded("t_a", false)
        #expect(m.rows.count == 2)
        #expect(!m.rows[0].expanded)
        // A folded member is still known, just without a row.
        let folded = try #require(m.message("a1"))
        #expect(folded.index == -1)
        #expect(m.keyFor("a1") == ListKey(thread: "t_a", message: "a1"))
    }

    @Test func setThreadsKeepsCache() {
        let a2 = member("a2", "t_a", 2, "bob")
        let a1 = member("a1", "t_a", 1, "alice")
        var m = groupedModel(thr("t_a", 2, 1, a2))
        m.setExpanded("t_a", true)
        m.setMembers("t_a", thr("t_a", 2, 1, a2), [a1, a2])

        // Same shape: the members survive the reload, the row stays open.
        m.setThreads([thr("t_a", 2, 1, a2)], page: PageInfo(total: 1))
        #expect(m.members["t_a"]?.complete == true)
        #expect(m.rows.count == 3)
        #expect(!m.rows[0].loading)
        // A reply arrived meanwhile: the members are asked for again.
        let a3 = member("a3", "t_a", 3, "carol")
        m.setThreads([thr("t_a", 3, 2, a3)], page: PageInfo(total: 1))
        #expect(m.members["t_a"]?.complete == false)
        #expect(m.rows.count == 1)
        #expect(m.rows[0].loading)
        // Duplicates are ignored.
        m.setThreads([thr("t_a", 3, 2, a3), thr("t_a", 3, 2, a3)], page: PageInfo(total: 1))
        #expect(m.threads.count == 1, "duplicate thread listed")
        let added = m.appendThreads([thr("t_a", 3, 2, a3), thr("t_b", 1, 0, member("b1", "t_b", 0, "dave"))], page: PageInfo(total: 0))
        #expect(added == 1)
        #expect(m.rows.count == 2)
    }

    @Test func setMembersEmptyDropsThread() {
        let a2 = member("a2", "t_a", 2, "bob")
        var m = groupedModel(thr("t_a", 2, 0, a2), thr("t_b", 1, 0, member("b1", "t_b", 1, "carol")))
        m.setMembers("t_a", thr("t_a", 2, 0, a2), [])
        #expect(m.threads.count == 1)
        #expect(m.threads[0].id == "t_b")
        #expect(m.total == 1)
        #expect(m.rows.count == 1)
    }

    @Test func applyNewMessageExistingThread() throws {
        let a2 = member("a2", "t_a", 2, "bob")
        let b1 = member("b1", "t_b", 5, "carol")
        var m = groupedModel(thr("t_b", 1, 0, b1, .seen), thr("t_a", 2, 1, a2))
        m.setExpanded("t_a", true)
        m.setMembers("t_a", thr("t_a", 2, 1, a2), [member("a1", "t_a", 1, "alice"), a2])

        var a3 = member("a3", "t_a", 9, "Alice") // a known address, capitalised: no new participant
        a3.hasAttachments = true
        let hoisted1 = m.applyNewMessage(a3, filter: .all, selected: ListKey())
        #expect(hoisted1, "rejected")
        #expect(m.threads[0].id == "t_a", "thread not moved to the top")
        let th = m.threads[0]
        #expect(th.messageCount == 3)
        #expect(th.unreadCount == 2)
        #expect(th.latestDate == a3.date)
        #expect(th.latest.id == "a3")
        #expect(th.snippet == "p-a3")
        #expect(th.hasAttachments)
        #expect(th.participants.count == 2)
        #expect(th.participants.first?.name == "Alice")
        #expect(th.participants.last?.name == "bob")
        let mem = try #require(m.members["t_a"])
        #expect(mem.list.count == 3)
        #expect(mem.list[2].id == "a3")
        #expect(mem.complete)
        let want = [ListKey(thread: "t_a"), ListKey(thread: "t_a", message: "a1"), ListKey(thread: "t_a", message: "a2"), ListKey(thread: "t_a", message: "a3"), ListKey(thread: "t_b", message: "b1")]
        #expect(keys(m) == want)
        // Delivered twice: nothing changes.
        let hoisted2 = m.applyNewMessage(a3, filter: .all, selected: ListKey())
        #expect(hoisted2)
        #expect(m.threads[0].messageCount == 3, "duplicate counted")

        // A reply to the selected single-message row unfolds it, so the row
        // the user reads stays.
        let b2 = member("b2", "t_b", 10, "dave")
        let hoisted3 = m.applyNewMessage(b2, filter: .all, selected: ListKey(thread: "t_b", message: "b1"))
        #expect(hoisted3, "rejected")
        #expect(m.expanded.contains("t_b"))
        #expect(m.threads[0].id == "t_b")
        #expect(m.rowIndexOf(ListKey(thread: "t_b", message: "b1")) == 1, "selected singleton not unfolded")
    }

    @Test func applyNewMessageNewThread() {
        var m = groupedModel(thr("t_a", 1, 0, member("a1", "t_a", 1, "alice", .seen), .seen))
        let c1 = member("c1", "t_c", 3, "carol")
        let hoisted4 = m.applyNewMessage(c1, filter: .all, selected: ListKey())
        #expect(hoisted4)
        #expect(m.threads[0].id == "t_c")
        #expect(m.total == 2)
        let th = m.threads[0]
        #expect(th.messageCount == 1)
        #expect(th.unreadCount == 1)
        #expect(th.latest.id == "c1")
        #expect(th.participants.count == 1)
        #expect(m.members["t_c"]?.complete == true)
        // Under the unread filter a read arrival is not listed.
        let d1 = member("d1", "t_d", 4, "dave", .seen)
        let hoisted5 = m.applyNewMessage(d1, filter: .unread, selected: ListKey())
        #expect(hoisted5)
        #expect(m.threads.count == 2, "seen message listed under unread")
        // No thread id: the caller reloads.
        let e1 = member("e1", nil, 5, "eve")
        let hoisted6 = m.applyNewMessage(e1, filter: .all, selected: ListKey())
        #expect(!hoisted6, "accepted a message without thread id")
    }

    @Test func applyFlagsAggregates() {
        let a2 = member("a2", "t_a", 2, "bob")
        var m = groupedModel(thr("t_a", 2, 2, a2))
        // Members not known: the change lands on the known one and the counts follow.
        var changed = m.applyFlags(["a2"], set: [.seen])
        #expect(changed.count == 1)
        #expect(m.threads[0].unreadCount == 1)
        #expect(hasFlag(m.threads[0].latest.flags, .seen))
        #expect(hasFlag(m.threads[0].flags, .seen))
        let hoisted7 = m.applyFlags(["a2"], set: [.seen])
        #expect(hoisted7.isEmpty, "unchanged flag reported")
        // Members known: the union is exact.
        let a1 = member("a1", "t_a", 1, "alice", .flagged)
        m.setExpanded("t_a", true)
        m.setMembers("t_a", thr("t_a", 2, 1, m.threads[0].latest, .flagged, .seen), [a1, m.threads[0].latest])
        changed = m.applyFlags(["a1", "a2"], clear: [.flagged, .seen])
        #expect(changed.count == 2)
        let th = m.threads[0]
        #expect(th.unreadCount == 2)
        #expect(!hasFlag(th.flags, .flagged))
        #expect(!hasFlag(th.flags, .seen))
        #expect(m.flagTarget(m.rows[0]), "flagTarget of an unflagged conversation should flag")
        m.applyFlags(["a1"], set: [.flagged])
        #expect(!m.flagTarget(m.rows[0]), "flagTarget of a flagged conversation should unflag")
        #expect(hasFlag(m.threads[0].flags, .flagged))
        #expect(m.rowIDs(m.rows[0]) == ["a1", "a2"])
        #expect(m.rowIDs(m.rows[1]) == ["a1"])
    }

    @Test func removeMessagesAndRestore() throws {
        let a2 = member("a2", "t_a", 2, "bob")
        let a1 = member("a1", "t_a", 1, "alice")
        let b1 = member("b1", "t_b", 0, "carol")
        var m = groupedModel(thr("t_a", 2, 2, a2), thr("t_b", 1, 1, b1))
        // Not all known: the caller has to reload.
        let hoisted8 = m.removeMessages(["a2"])
        #expect(hoisted8 == nil, "removed from an incomplete conversation")
        m.setExpanded("t_a", true)
        m.setMembers("t_a", thr("t_a", 2, 2, a2), [a1, a2])

        // One member goes: the conversation shrinks to a single row.
        let hoisted9 = m.removeMessages(["a2"])
        var r = try #require(hoisted9)
        #expect(m.threads.count == 2)
        #expect(m.threads[0].messageCount == 1)
        #expect(m.threads[0].latest.id == "a1")
        #expect(keys(m) == [ListKey(thread: "t_a", message: "a1"), ListKey(thread: "t_b", message: "b1")])
        m.restoreRemoval(r)
        #expect(m.threads[0].messageCount == 2)
        #expect(m.rows.count == 4)
        #expect(m.expanded.contains("t_a"))

        // The whole conversation goes, and comes back at its place.
        let hoisted10 = m.removeMessages(["a1", "a2"])
        r = try #require(hoisted10)
        #expect(m.threads.count == 1)
        #expect(m.threads[0].id == "t_b")
        #expect(m.total == 1)
        #expect(m.rowIndexOf(ListKey(thread: "t_a")) == -1)
        m.restoreRemoval(r)
        #expect(m.threads.count == 2)
        #expect(m.threads[0].id == "t_a")
        #expect(m.total == 2)
        #expect(m.rows[0].key == ListKey(thread: "t_a"))
        #expect(m.members["t_a"]?.complete == true)
        #expect(m.memberOf["a2"] != nil, "member index not restored")
    }

    @Test func collapseLoadingTest() {
        var m = groupedModel(thr("t_a", 2, 0, member("a2", "t_a", 2, "bob")), thr("t_b", 3, 0, member("b3", "t_b", 3, "carol")))
        m.setExpanded("t_a", true)
        m.setExpanded("t_b", true)
        m.members["t_a"]?.fetching = true
        m.setMembers("t_b", thr("t_b", 3, 0, member("b3", "t_b", 3, "carol")), [member("b1", "t_b", 1, "x"), member("b2", "t_b", 2, "y"), member("b3", "t_b", 3, "carol")])
        m.collapseLoading()
        #expect(!m.expanded.contains("t_a"))
        #expect(m.expanded.contains("t_b"))
        #expect(m.members["t_a"]?.fetching == false)
        #expect(m.rows.count == 5)
    }

    @Test func flatModeAccessors() throws {
        var m = MailModel()
        m.setMessages([summary("a"), summary("b")], page: PageInfo(total: 2))
        #expect(m.rowCount == 2)
        #expect(m.rowIndexOf(ListKey(message: "b")) == 1)
        #expect(m.keyFor("a") == ListKey(message: "a"))
        let r = try #require(m.rowAt(1))
        #expect(!r.thread)
        #expect(r.message.id == "b")
        #expect(r.key == ListKey(message: "b"))
        #expect(m.rowIDs(r) == ["b"])
        let changed = m.applyFlags(["a", "zz"], set: [.seen])
        #expect(changed == ["a"])
        #expect(hasFlag(m.messages[0].flags, .seen))
        let o = OutboxInfo(state: .queued, attempts: 0)
        m.setOutbox("b", o)
        #expect(m.messages[1].outbox == o)
        m.clearMessages()
        #expect(m.rowCount == 0)
        #expect(m.rows.isEmpty)
    }

    @Test func summaryThreadTest() {
        var a2 = member("a2", "t_a", 2, "bob")
        a2.hasAttachments = true
        let th = thr("t_a", 3, 1, a2, .flagged)
        let got = summaryThread(th, expanded: true, loading: false)
        #expect(got.count == 3)
        #expect(got.unread == 1)
        #expect(got.flagged)
        #expect(got.hasAttachments)
        #expect(got.expanded)
        #expect(!got.loading)
        #expect(got.subject == "s-a2")
        #expect(got.snippet == "p-a2")
        #expect(got.date == a2.date)
        #expect(got.participants.count == 1)
    }
}

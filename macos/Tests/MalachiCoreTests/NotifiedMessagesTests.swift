// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/notified_test.go. The mutating
/// calls go into locals first, outside `#expect`.
@Suite struct NotifiedMessagesTests {
    private let inboxA = FolderKey(account: "a", folder: "inbox")
    private let archiveA = FolderKey(account: "a", folder: "archive")
    private let inboxB = FolderKey(account: "b", folder: "inbox")

    private func ids(_ s: [NotifiedEntry]) -> [MessageID] {
        s.map(\.id)
    }

    @Test func addRemove() {
        var s = NotifiedMessages()
        s.add("m1", inboxA)
        s.add("m2", archiveA)
        s.add("m3", inboxB)
        // Delivered twice: one entry, now the newest.
        let readded = s.add("m1", inboxA)
        #expect(readded.isEmpty)
        #expect(ids(s.entries) == ["m2", "m3", "m1"])

        // Only what the set holds comes back: nothing is withdrawn blindly.
        let removed = s.remove(["m9", "m3", "m3"])
        #expect(removed == ["m3"])
        let again = s.remove(["m3"])
        #expect(again.isEmpty)
        let none = s.remove([])
        #expect(none.isEmpty)
        #expect(ids(s.entries) == ["m2", "m1"])

        var empty = NotifiedMessages()
        let fromEmpty = empty.remove(["m1"])
        #expect(fromEmpty.isEmpty)
    }

    @Test func bounded() {
        var s = NotifiedMessages()
        for i in 0..<notifiedMax {
            let evicted = s.add(MessageID("m\(i)"), inboxA)
            #expect(evicted.isEmpty)
        }
        // The oldest goes, and the caller is told to withdraw it.
        let evicted = s.add("new", inboxA)
        #expect(evicted == ["m0"])
        #expect(s.entries.count == notifiedMax)
        #expect(s.entries.first?.id == "m1")
        #expect(s.entries.last?.id == "new")
        // Re-adding a held message evicts nothing.
        let readded = s.add("m1", inboxA)
        #expect(readded.isEmpty)
    }

    @Test func folderAndAccount() {
        var s = NotifiedMessages()
        s.add("m1", inboxA)
        s.add("m2", archiveA)
        s.add("m3", inboxA)
        s.add("m4", inboxB)

        let got = s.ofAccount("a")
        #expect(ids(got) == ["m1", "m2", "m3"])
        #expect(got[1].key == archiveA)
        #expect(s.ofAccount("zzz").isEmpty)

        // Viewing the inbox withdraws the inbox's notifications, not the
        // archive's or the other account's inbox.
        let inbox = s.removeFolder(inboxA)
        #expect(inbox == ["m1", "m3"])
        let inboxAgain = s.removeFolder(inboxA)
        #expect(inboxAgain.isEmpty)

        // Account b was removed or paused.
        let accountB = s.removeAccounts(except: ["a"])
        #expect(accountB == ["m4"])
        let rest = s.removeAccounts(except: [])
        #expect(rest == ["m2"])
        #expect(s.entries.isEmpty)
    }

    @Test func outdated() {
        let e = NotifiedEntry(id: "m1", key: inboxA)
        var unread = summary("m1", .flagged)
        unread.accountId = "a"
        unread.folderId = "inbox"
        var read = unread
        read.flags = [.flagged, .seen]
        var moved = unread
        moved.folderId = "archive"
        var noFolder = unread
        noFolder.folderId = "" // an older daemon: nothing to compare

        #expect(notificationOutdated(e, .success(unread)) == false)
        #expect(notificationOutdated(e, .success(read)) == true)
        #expect(notificationOutdated(e, .success(moved)) == true)
        #expect(notificationOutdated(e, .success(noFolder)) == false)
        #expect(notificationOutdated(e, .failure(RPCError(code: .messageNotFound, message: "gone"))) == true)
        #expect(notificationOutdated(e, .failure(RPCError(code: .accountNotFound, message: "gone"))) == true)
        #expect(notificationOutdated(e, .failure(RPCError(code: .storageError, message: "disk"))) == nil)
        #expect(notificationOutdated(e, .failure(RPCClient.ClientError.disconnected)) == nil)
        #expect(notificationOutdated(e, .failure(RPCClient.ClientError.timeout(method: "message.get"))) == nil)
        #expect(notificationOutdated(e, .failure(CancellationError())) == nil)
    }

    @Test func identifier() {
        #expect(notificationID("m_42") == "message-m_42")
    }
}

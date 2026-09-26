// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/outbox_test.go.
@Suite struct OutboxTests {
    @Test func outboxBannerTextTest() {
        let cases: [(String, OutboxInfo?, String, String, Bool)] = [
            ("not in the outbox", nil, "", "", false),
            ("sent", OutboxInfo(state: .sent, attempts: 0), "", "", false),
            ("unknown state", OutboxInfo(state: "bogus", attempts: 0), "", "", false),
            ("queued", OutboxInfo(state: .queued, attempts: 0), "Queued for sending", "", true),
            ("queued after a failure keeps the error quiet", OutboxInfo(state: .queued, attempts: 2, error: RPCError(code: .networkError, message: "dial")), "Queued for sending", "", true),
            ("sending", OutboxInfo(state: .sending, attempts: 1), "Sending…", "", true),
            ("failed with a known code", OutboxInfo(state: .failed, attempts: 0, error: RPCError(code: .authFailed, message: "535")), "Sending the message failed: the server rejected the user name or password", "Retry", true),
            ("failed with an unknown code", OutboxInfo(state: .failed, attempts: 0, error: RPCError(code: .internalError, message: "boom")), "Sending the message failed", "Retry", true),
            ("failed without an error", OutboxInfo(state: .failed, attempts: 0), "Sending the message failed", "Retry", true),
        ]
        for (name, info, title, button, shown) in cases {
            let got = outboxBannerText(info)
            #expect(got.title == title && got.button == button && got.shown == shown,
                    "\(name): got \(got.title)/\(got.button)/\(got.shown)")
        }
    }

    @Test func inOutboxTest() {
        let m = MailModel(
            accounts: [testAccount("a")],
            folders: ["a": [
                testFolder("in", path: "INBOX", role: .inbox),
                testFolder("out", path: "Outbox", role: .outbox),
            ]]
        )
        var inbox = summary("1")
        inbox.accountId = "a"
        inbox.folderId = "in"
        #expect(!m.inOutbox(inbox), "inbox message reported in the outbox")
        // The folder role alone is enough (a list summary from the outbox).
        var byFolder = summary("2")
        byFolder.accountId = "a"
        byFolder.folderId = "out"
        #expect(m.inOutbox(byFolder), "message in the outbox folder not recognised")
        // So is the delivery state alone (folders not loaded yet).
        var byInfo = summary("3")
        byInfo.accountId = "a"
        byInfo.folderId = "gone"
        byInfo.outbox = OutboxInfo(state: .queued, attempts: 0)
        #expect(m.inOutbox(byInfo), "message with delivery state not recognised")
        #expect(!MailModel().inOutbox(inbox), "empty model reported the outbox")
    }

    @Test func trashTooltipTest() {
        #expect(trashTooltip(outbox: true) == "Cancel Sending")
        #expect(trashTooltip(outbox: false) == "Move to Trash")
    }

    /// The delivery bookkeeping of trackOutbox (no Go counterpart; it is
    /// bound to the window there).
    @Test func outboxTracker() {
        var t = OutboxTracker()
        let hoisted1 = t.track("a", total: 3)
        #expect(hoisted1 == 0, "the first look is a baseline")
        let hoisted2 = t.track("a", total: 3)
        #expect(hoisted2 == 0)
        let hoisted3 = t.track("a", total: 1)
        #expect(hoisted3 == 2, "two delivered")
        let hoisted4 = t.track("a", total: 4)
        #expect(hoisted4 == 0, "growth is not a delivery")
        t.noteCancelled("a")
        let hoisted5 = t.track("a", total: 3)
        #expect(hoisted5 == 0, "a cancelled message is not a delivery")
        let hoisted6 = t.track("a", total: 2)
        #expect(hoisted6 == 1, "the cancellation counts once")
        let hoisted7 = t.track("b", total: 0)
        #expect(hoisted7 == 0)
        let hoisted8 = t.track("b", total: 0)
        #expect(hoisted8 == 0)

        // A cancel is used up only by a shrink it explains: a reload that
        // lands before the daemon's delete keeps it for the one that sees
        // the drop.
        t.noteCancelled("a")
        let early = t.track("a", total: 2)
        #expect(early == 0, "nothing left yet")
        let dropped = t.track("a", total: 1)
        #expect(dropped == 0, "the drop is the cancel, not a delivery")
        let later = t.track("a", total: 0)
        #expect(later == 1, "the cancel was used up by the drop it explained")

        // A delivery and a cancel in one shrink.
        _ = t.track("c", total: 3)
        t.noteCancelled("c")
        let mixed = t.track("c", total: 1)
        #expect(mixed == 1, "one of the two was ours")

        // A refused cancel is taken back, never below zero.
        _ = t.track("d", total: 2)
        t.noteCancelled("d")
        t.noteCancelFailed("d")
        t.noteCancelFailed("d")
        let delivered = t.track("d", total: 1)
        #expect(delivered == 1, "the refused cancel explains nothing")
        t.noteCancelled("d")
        let cancelled = t.track("d", total: 0)
        #expect(cancelled == 0, "a note after the refusals still counts once")
    }
}

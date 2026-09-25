// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

private struct Boom: Error {}

/// The counterpart of ui/internal/window/compose_open_test.go.
@MainActor
@Suite struct ComposeSourceTests {
    /// The source of a reply is the summary until the full message and the
    /// body are loaded; a body that is not fetched contributes no text.
    @Test func composeSourceTest() {
        let date = Date(timeIntervalSince1970: 1_788_775_200) // 2026-09-07T10:00:00Z
        var s = summary("m_1")
        s.accountId = "acc"
        s.from = [Address(address: "a@example.invalid")]
        s.to = [Address(address: "me@example.invalid")]
        s.subject = "s"
        s.date = date

        var src = composeSource(summary: s, loaded: nil)
        #expect(src.id == "m_1")
        #expect(src.accountID == "acc")
        #expect(src.from.count == 1)
        #expect(src.to.count == 1)
        #expect(src.subject == "s")
        #expect(src.date == date)
        #expect(src.text == "")
        #expect(src.replyTo.isEmpty)

        var headers = s
        headers.from = [Address(name: "A", address: "a@example.invalid")]
        headers.subject = "full"
        let full = Message(
            summary: headers, cc: [Address(address: "c@example.invalid")], replyTo: [Address(address: "r@example.invalid")]
        )
        let lm = LoadedMessage(msg: full, body: MessageBodyResult(
            messageId: "m_1", bodyState: .pending, hasHtml: false, text: "not yet", remoteContent: .block, sanitizerVersion: "1"))
        src = composeSource(summary: s, loaded: lm)
        #expect(src.from.first?.name == "A")
        #expect(src.replyTo.count == 1)
        #expect(src.cc.count == 1)
        #expect(src.subject == "full")
        #expect(src.text == "")
        lm.body = MessageBodyResult(
            messageId: "m_1", bodyState: .fetched, hasHtml: false, text: "hello", remoteContent: .block, sanitizerVersion: "1")
        src = composeSource(summary: s, loaded: lm)
        #expect(src.text == "hello")

        // Go's zero date is no date.
        s.date = .goZero
        #expect(composeSource(summary: s, loaded: nil).date == nil)
        #expect(composeWhat(.forward) == "Preparing the forwarded message")
        #expect(composeWhat(.reply) == "Preparing the reply")
        #expect(composeWhat(.replyAll) == "Preparing the reply")
    }

    /// No toast when there is no backend to ask or it lacks the call; a
    /// sentence for everything else.
    @Test func composeFallbackTextTest() {
        #expect(composeFallbackText("Preparing the reply", RPCClient.ClientError.disconnected) == "")
        #expect(composeFallbackText("Preparing the reply", RPCClient.ClientError.notConnected) == "")
        #expect(composeFallbackText("Preparing the reply", RPCError(code: .notImplemented, message: "x")) == "")
        let others: [any Error] = [
            RPCClient.ClientError.timeout(method: "draft.create"),
            CancellationError(),
            RPCError(code: .messageNotFound, message: "gone"),
            Boom(),
        ]
        for err in others {
            #expect(!composeFallbackText("Preparing the reply", err).isEmpty, "\(err): no toast")
        }
    }
}

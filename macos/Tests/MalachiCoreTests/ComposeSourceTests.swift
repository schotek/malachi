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

    /// compose_open.go `forwardNeedsDownload`: a forward downloads first
    /// when any part is on the server (an attachment, or a picture the
    /// HTML shows), or the body is not downloaded yet, or the cache cannot
    /// tell (message.download answers at once when nothing is missing).
    @Test func forwardNeedsDownloadTest() {
        func att(_ id: String, inline: Bool = false, remote: Bool? = nil) -> MalachiCore.Attachment {
            MalachiCore.Attachment(partId: id, filename: id + ".bin", contentType: "application/octet-stream", size: 200_000,
                                   inline: inline, contentId: inline ? id + "@x" : nil, remote: remote)
        }
        func body(_ state: BodyState) -> MessageBodyResult {
            MessageBodyResult(messageId: "m_1", bodyState: state, hasHtml: false, text: "", remoteContent: .block, sanitizerVersion: "1")
        }
        #expect(forwardNeedsDownload(nil), "not in the cache")
        #expect(forwardNeedsDownload(LoadedMessage(body: body(.pending))), "nothing known before message.get")
        #expect(forwardNeedsDownload(LoadedMessage(body: body(.fetched))), "message.get failed")
        let local = Message(summary: summary("m_1"), attachments: [att("2"), att("3", remote: false)])
        #expect(!forwardNeedsDownload(LoadedMessage(msg: local, body: body(.fetched))))
        #expect(!forwardNeedsDownload(LoadedMessage(msg: local)), "body not asked for yet")
        #expect(forwardNeedsDownload(LoadedMessage(msg: local, body: body(.pending))), "a body not downloaded yet")
        let remote = Message(summary: summary("m_1"), attachments: [att("2"), att("3", remote: true)])
        #expect(forwardNeedsDownload(LoadedMessage(msg: remote, body: body(.fetched))))
        #expect(forwardNeedsDownload(LoadedMessage(msg: remote)), "on the server, body not loaded here")
        #expect(!forwardNeedsDownload(LoadedMessage(msg: local, body: body(.tooBig))), "too big to download anyway")
        let picture = Message(summary: summary("m_1"), attachments: [att("2", inline: true, remote: true)])
        #expect(forwardNeedsDownload(LoadedMessage(msg: picture, body: body(.fetched))), "a picture the HTML shows, on the server")
        let storedPicture = Message(summary: summary("m_1"), attachments: [att("2", inline: true)])
        #expect(!forwardNeedsDownload(LoadedMessage(msg: storedPicture, body: body(.fetched))), "a stored picture")
        #expect(!forwardNeedsDownload(LoadedMessage(msg: Message(summary: summary("m_1")), body: body(.failed))))
    }

    /// compose_open.go `replyNeedsDownload`: a reply downloads first only
    /// when the body on display counts pictures kept on the mail server
    /// only (the daemon's `remotePictures`); an attachment there, even one
    /// with a Content-ID, or a message the cache has no body of, is no
    /// reason to.
    @Test func replyNeedsDownloadTest() {
        func att(_ id: String, cid: String? = nil, inline: Bool = false, remote: Bool? = nil) -> MalachiCore.Attachment {
            MalachiCore.Attachment(partId: id, filename: id + ".png", contentType: "image/png", size: 200_000,
                                   inline: inline, contentId: cid, remote: remote)
        }
        func body(html: String? = "<p>x</p>", remotePictures: Int? = nil) -> MessageBodyResult {
            MessageBodyResult(messageId: "m_1", bodyState: .fetched, hasHtml: html != nil, html: html, text: "x",
                              remotePictures: remotePictures, remoteContent: .block, sanitizerVersion: "1")
        }
        func loaded(_ atts: [MalachiCore.Attachment], _ b: MessageBodyResult? = body()) -> LoadedMessage {
            LoadedMessage(msg: Message(summary: summary("m_1"), attachments: atts), body: b)
        }
        let picture = att("2", cid: "p@x", inline: true, remote: true)
        #expect(!replyNeedsDownload(nil), "not in the cache")
        #expect(!replyNeedsDownload(LoadedMessage()), "nothing known yet")
        #expect(!replyNeedsDownload(loaded([picture], nil)), "body not loaded yet")
        #expect(!replyNeedsDownload(loaded([])))
        #expect(!replyNeedsDownload(loaded([att("2", cid: "p@x", remote: true)])), "a Content-ID is no reason")
        #expect(!replyNeedsDownload(loaded([att("2", remote: true)])), "an attachment is not quoted")
        #expect(!replyNeedsDownload(loaded([picture])), "a picture the daemon holds is not counted")
        #expect(replyNeedsDownload(loaded([att("2", remote: true), picture], body(remotePictures: 2))))
        #expect(replyNeedsDownload(LoadedMessage(body: body(remotePictures: 1))), "counted, message.get failed")
        #expect(!replyNeedsDownload(loaded([picture], body(html: nil, remotePictures: 2))), "text only")
    }

    /// A failed download asks, unless asking would change nothing: no
    /// daemon, one without message.download, or a message it can never
    /// download.
    @Test func askForwardWithoutTest() {
        let atOnce: [any Error] = [
            RPCError(code: .methodNotFound, message: "x"),
            RPCError(code: .notImplemented, message: "x"),
            RPCClient.ClientError.notConnected,
            RPCClient.ClientError.disconnected,
            RPCError(code: .attachmentTooBig, message: "over the cap"),
        ]
        for err in atOnce {
            #expect(!askForwardWithout(err), "\(err)")
        }
        let ask: [any Error] = [
            RPCError(code: .offline, message: "x"),
            RPCError(code: .messageGone, message: "x"),
            RPCError(code: .unavailable, message: "x"),
            RPCError(code: .serverTimeout, message: "x"),
            RPCError(code: .cancelled, message: "x"),
            RPCClient.ClientError.timeout(method: "message.download"),
            RPCClient.ClientError.transport("reset"),
            CancellationError(),
            Boom(),
        ]
        for err in ask {
            #expect(askForwardWithout(err), "\(err)")
        }
    }
}

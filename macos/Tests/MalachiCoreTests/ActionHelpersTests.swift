// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/actions_test.go: the pure helpers
// behind the message pane and the actions. The catalogue is English in
// tests, so the msgids come back verbatim.

/// A body result with only the fields a case needs.
private func body(
    _ state: BodyState = .fetched, html: String? = nil, text: String = "", withheld: Bool? = nil,
    remoteImages: Int = 0, policy: RemoteContentPolicy = .block, remotePictures: Int? = nil
) -> MessageBodyResult {
    MessageBodyResult(
        messageId: "m", bodyState: state, hasHtml: html != nil, html: html, htmlWithheld: withheld, text: text,
        blocked: BlockedContent(remoteImages: remoteImages), remotePictures: remotePictures, remoteContent: policy,
        sanitizerVersion: "1"
    )
}

@MainActor
@Suite struct ActionHelpersTests {
    @Test func subjectTextTest() {
        #expect(subjectText("  Hello  ") == "Hello")
        #expect(subjectText(" \t") == "(No subject)")
    }

    @Test func bodyTextTest() {
        let cases: [(String, MessageBodyResult?, String)] = [
            ("nil", nil, "(Empty message)"),
            ("pending", body(.pending), "Downloading…"),
            ("tooBig", body(.tooBig, text: "ignored"), "This message is too large to download."),
            ("failed", body(.failed), "This message could not be read."),
            ("fetched", body(.fetched, text: "Hi\n\n"), "Hi"),
            ("empty", body(.fetched, text: " \n"), "(Empty message)"),
            ("tags stay text", body(.fetched, text: "<b>x</b>"), "<b>x</b>"),
            ("crlf", body(.fetched, text: "Hi\r\n"), "Hi"),
        ]
        for (name, b, want) in cases {
            #expect(bodyText(b) == want, Comment(rawValue: name))
        }
    }

    @Test func flagChangeTest() {
        var change = flagChange(.seen, on: true)
        #expect(change.set == [.seen])
        #expect(change.clear == nil)
        change = flagChange(.flagged, on: false)
        #expect(change.set == nil)
        #expect(change.clear == [.flagged])
    }

    @Test func pruneLoaded() {
        var entries: [MessageID: LoadedMessage] = [:]
        for (i, id) in (["a", "b", "c", "d"] as [MessageID]).enumerated() {
            entries[id] = LoadedMessage(seq: UInt64(i + 1))
        }
        var cache = LoadedCache(entries: entries)
        cache.prune(limit: 2, maxBytes: LoadedCache.maxLoadedBytes)
        #expect(cache.count == 2)
        #expect(cache["c"] != nil)
        #expect(cache["d"] != nil)
        cache.prune(limit: 2, maxBytes: LoadedCache.maxLoadedBytes) // no-op at the limit
        #expect(cache.count == 2)

        // Bodies count too: big HTML evicts older entries before the count
        // limit, but the newest entry always stays, however big.
        func big(_ seq: UInt64, _ n: Int) -> LoadedMessage {
            LoadedMessage(body: body(html: String(repeating: "x", count: n)), seq: seq)
        }
        cache = LoadedCache(entries: ["a": big(1, 600), "b": big(2, 600), "c": big(3, 600)])
        cache.prune(limit: 10, maxBytes: 1000)
        #expect(cache.count == 1)
        #expect(cache["c"] != nil, "byte cap should keep only the newest")
        cache = LoadedCache(entries: ["a": big(1, 5000)])
        cache.prune(limit: 10, maxBytes: 1000)
        #expect(cache.count == 1, "the newest entry must survive the byte cap")

        // The cache numbers what it stores and prunes as it goes.
        cache = LoadedCache()
        for id in ["p", "q", "r"] as [MessageID] {
            _ = cache.loadedFor(id)
        }
        #expect(cache.count == 3)
        #expect(cache["p"]!.seq < cache["r"]!.seq)
        let hoisted1 = cache.loadedFor("p")
        #expect(hoisted1 === cache["p"])
        cache.prune(limit: 1)
        #expect(cache.count == 1)
        #expect(cache["r"] != nil)
    }

    @Test func loadedMessageState() {
        let lm = LoadedMessage()
        #expect(!lm.complete)
        #expect(!lm.bodySettled)
        lm.err = errTest
        #expect(lm.bodySettled, "body error must settle the body")
        #expect(!lm.complete, "body error must not complete the entry")
        lm.err = nil
        lm.body = body()
        lm.msg = Message(summary: summary("m"))
        #expect(lm.complete)
        #expect(lm.size == 0)
        lm.body = body(html: "<p>hi</p>", text: "hi")
        #expect(lm.size == 11)
    }

    @Test func loadableImagesTest() {
        let html = body(html: "<p>x</p>", remoteImages: 3)
        let cases: [(String, MessageBodyResult?, Int)] = [
            ("nil body", nil, 0),
            ("blocked html", html, 3),
            // Already allowed: the daemon fetched what it could, and what the
            // counter still holds no button can bring back.
            ("allowed html", body(html: "<p>x</p>", remoteImages: 3, policy: .allow), 0),
            ("text only", body(remoteImages: 2), 0),
            ("nothing blocked", body(html: "<p>x</p>"), 0),
        ]
        for (name, b, want) in cases {
            #expect(loadableImages(b) == want, Comment(rawValue: name))
        }
    }

    /// The bar shows the wait from the click until the daemon answers,
    /// whatever the body says meanwhile.
    @Test func remoteBarStateTest() {
        let blocked = body(html: "<p>x</p>", remoteImages: 2)
        let allowed = body(html: "<p>x</p>", remoteImages: 2, policy: .allow)
        let cases: [(String, LoadedMessage?, RemoteBarState)] = [
            ("nothing loaded", nil, RemoteBarState()),
            ("body on its way", LoadedMessage(), RemoteBarState()),
            ("blocked", LoadedMessage(body: blocked), RemoteBarState(visible: true, blocked: 2)),
            ("loading", LoadedMessage(body: blocked, loadingImages: true), RemoteBarState(visible: true, loading: true)),
            ("loading before the body", LoadedMessage(loadingImages: true), RemoteBarState(visible: true, loading: true)),
            ("images in", LoadedMessage(body: allowed), RemoteBarState()),
        ]
        for (name, lm, want) in cases {
            #expect(remoteBarState(for: lm) == want, Comment(rawValue: name))
        }
        #expect(linkTextFor("https://b", [Link(text: "A", href: "https://a"), Link(text: "B", href: "https://b")]) == "B")
        #expect(linkTextFor("https://c", []) == "")
    }

    /// The pictures bar counts the pictures of an HTML body kept on the
    /// mail server only, and shows the wait from the click until the body
    /// was asked for again (remote.go `picturesBarStateFor`,
    /// `remotePictures`).
    @Test func picturesBarStateTest() {
        let html = "<p><img src=\"malachi-cid:a/m/2\"></p>"
        #expect(remotePictures(nil) == 0)
        #expect(remotePictures(body(html: html)) == 0, "absent is 0")
        #expect(remotePictures(body(html: html, remotePictures: 3)) == 3)
        #expect(remotePictures(body(html: html, remotePictures: -1)) == 0)
        #expect(remotePictures(body(.pending, html: html, remotePictures: 3)) == 0, "not in the HTML view")
        #expect(remotePictures(body(html: nil, remotePictures: 3)) == 0, "plain text")
        #expect(remotePictures(body(html: "", withheld: true, remotePictures: 3)) == 0, "HTML withheld")

        let server = body(html: html, remotePictures: 2)
        let local = body(html: html)
        let cases: [(String, LoadedMessage?, PicturesBarState)] = [
            ("nothing loaded", nil, PicturesBarState()),
            ("body on its way", LoadedMessage(), PicturesBarState()),
            ("on the server", LoadedMessage(body: server), PicturesBarState(visible: true, remote: 2)),
            ("downloading", LoadedMessage(body: server, loadingPictures: true), PicturesBarState(visible: true, loading: true)),
            ("all here", LoadedMessage(body: local), PicturesBarState()),
            ("remote images loading", LoadedMessage(body: server, loadingImages: true), PicturesBarState(visible: true, remote: 2)),
        ]
        for (name, lm, want) in cases {
            #expect(picturesBarState(for: lm) == want, Comment(rawValue: name))
        }
    }

    /// After the download the body is asked for under allow when the
    /// remote images are shown or on their way, so they stay (remote.go
    /// `picturesPolicy`).
    @Test func picturesPolicyTest() {
        let html = "<p>x</p>"
        #expect(picturesPolicy(LoadedMessage()) == nil)
        #expect(picturesPolicy(LoadedMessage(body: body(html: html, remoteImages: 2))) == nil)
        #expect(picturesPolicy(LoadedMessage(body: body(html: html, policy: .allow))) == .allow)
        #expect(picturesPolicy(LoadedMessage(body: body(html: html, remoteImages: 2), loadingImages: true)) == .allow)
    }

    /// A picture the daemon no longer serves asks for the body again only
    /// when the cached body lists it and counts none on the server, nothing
    /// newer is on its way, and it has not asked since the last download
    /// (remote.go `recheckPictures`).
    @Test func recheckPicturesTest() {
        var listed = body(html: "<p>x</p>")
        listed.inlineParts = ["p@x": "1.2", "q@x": "1.3"]
        var counted = listed
        counted.remotePictures = 1
        var text = body(html: nil, text: "x")
        text.inlineParts = listed.inlineParts
        let cases: [(String, LoadedMessage?, String, Bool)] = [
            ("not cached", nil, "1.2", false),
            ("no body", LoadedMessage(), "1.2", false),
            ("listed, none counted", LoadedMessage(body: listed), "1.2", true),
            ("another listed one", LoadedMessage(body: listed), "1.3", true),
            ("not a picture of the body", LoadedMessage(body: listed), "2", false),
            ("asked already", LoadedMessage(body: listed, picturesRechecked: true), "1.2", false),
            ("the bar is up already", LoadedMessage(body: counted), "1.2", false),
            ("body on its way", LoadedMessage(body: listed, fetching: true), "1.2", false),
            ("remote images on their way", LoadedMessage(body: listed, loadingImages: true), "1.2", false),
            ("pictures on their way", LoadedMessage(body: listed, loadingPictures: true), "1.2", false),
            ("text shown", LoadedMessage(body: text), "1.2", false),
        ]
        for (name, lm, part, want) in cases {
            #expect(recheckPictures(lm, part) == want, Comment(rawValue: name))
        }
    }

    /// A download asks for the body again when it counts pictures on the
    /// server, unless Download Pictures does that itself or a body is on
    /// its way (remote.go `reloadAfterDownload`).
    @Test func reloadAfterDownloadTest() {
        let counted = body(html: "<p>x</p>", remotePictures: 2)
        let cases: [(String, LoadedMessage?, Bool)] = [
            ("not cached", nil, false),
            ("no body", LoadedMessage(), false),
            ("pictures on the server", LoadedMessage(body: counted), true),
            ("remote images on their way", LoadedMessage(body: counted, loadingImages: true), true),
            ("none on the server", LoadedMessage(body: body(html: "<p>x</p>")), false),
            ("Download Pictures asks itself", LoadedMessage(body: counted, loadingPictures: true), false),
            ("body on its way", LoadedMessage(body: counted, fetching: true), false),
            ("text shown", LoadedMessage(body: body(html: nil, text: "x", remotePictures: 2)), false),
        ]
        for (name, lm, want) in cases {
            #expect(reloadAfterDownload(lm) == want, Comment(rawValue: name))
        }
    }

    /// The sensitivity rule of setMessageActionsSensitive as a pure
    /// function (no Go counterpart; the GTK test is manual).
    @Test func messageActionStateTest() {
        var m = MailModel(
            accounts: [testAccount("a")],
            folders: ["a": [
                testFolder("in", path: "INBOX", role: .inbox),
                testFolder("arch", path: "Archive", role: .archive),
                testFolder("out", path: "Outbox", role: .outbox, total: 1),
            ]]
        )
        var s = summary("1", .seen)
        s.accountId = "a"
        s.folderId = "in"
        m.setMessages([s], page: PageInfo(total: 1))

        #expect(messageActionState(nil, model: m) == .none)
        #expect(messageActionState(m.rowAt(0), on: false, model: m) == .none)

        var st = messageActionState(m.rowAt(0), model: m)
        #expect(st.on && st.reply && st.replyAll && st.forward && st.trash && st.loadImages)
        #expect(st.archive, "an archive folder exists")
        #expect(!st.junk, "no junk folder")
        #expect(!st.markRead && st.markUnread, "a read message can only be marked unread")
        #expect(st.star && st.toggleFlag && st.trustSender)
        #expect(!st.flagged && !st.outbox)

        // In the archive already: archive is off.
        m.messages[0].folderId = "arch"
        m.messages[0].flags = [.flagged]
        st = messageActionState(m.rowAt(0), model: m)
        #expect(!st.archive)
        #expect(st.markRead && !st.markUnread)
        #expect(st.flagged)

        // An outbox message keeps reply, forward and trash (which cancels).
        m.messages[0].folderId = "out"
        st = messageActionState(m.rowAt(0), model: m)
        #expect(st.outbox && st.reply && st.forward && st.trash && st.loadImages)
        #expect(!st.star && !st.archive && !st.junk && !st.markRead && !st.markUnread && !st.toggleFlag && !st.trustSender)

        // A conversation row: read when every member is, flagged when any is.
        let a2 = member("a2", "t_a", 2, "bob", .seen)
        var g = groupedModel(thr("t_a", 2, 1, a2, .flagged))
        g.accounts = m.accounts
        g.folders = m.folders
        g.threads[0].latest.accountId = "a"
        g.threads[0].latest.folderId = "in"
        g.rebuildRows()
        st = messageActionState(g.rowAt(0), model: g)
        #expect(st.flagged)
        #expect(st.markRead && st.markUnread, "one unread member of two: both directions apply")
        g.threads[0].unreadCount = 0
        g.rebuildRows()
        st = messageActionState(g.rowAt(0), model: g)
        #expect(!st.markRead && st.markUnread)
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/conversation/chat_test.go (English catalogue: the msgids come
// back verbatim), with the builders of conversation_test.go as
// ConversationTests.swift has them. Go's zero item and draft of a row are
// nil here.

/// Characters the cleaning must drop, as scalar constants: the source
/// stays free of invisible characters.
private let rlo = jiraScalar(0x202E) // RIGHT-TO-LEFT OVERRIDE
private let zwsp = jiraScalar(0x200B) // ZERO WIDTH SPACE

/// 2026-09-01T09:00:00Z.
private let t0 = Date(timeIntervalSince1970: 1_788_253_200)

private func at(_ min: Int) -> Date {
    t0.addingTimeInterval(Double(min) * 60)
}

private let jana = Address(name: "Jana Dvořáková", address: "jana@acme.example")
private let petr = Address(name: "Petr Svoboda", address: "petr@acme.example")

private let mailAccount = Account(
    id: "a1", config: AccountConfig(name: "Work", email: "petr@acme.example"), enabled: true,
    state: SyncState(accountId: "a1", status: .idle))
private let jiraAccount = Account(
    id: "j1", config: AccountConfig(name: "Acme Jira", email: "petr@acme.example", kind: .jira), enabled: true,
    state: SyncState(accountId: "j1", status: .idle), capabilities: [.comment, .forward])

/// A member of the mail conversation t1 in folder f1, Jana's to Petr.
private func mail(_ id: String, _ min: Int, _ seen: Bool) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: "f1", threadId: "t1", from: [jana], to: [petr],
        subject: "Re: Quarterly report", date: at(min), snippet: "See the figures", flags: seen ? [.seen] : [],
        hasAttachments: false, size: 0)
}

/// Petr's reply in his Sent folder fs (thread.get's sent).
private func reply(_ id: String, _ min: Int) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "a1", folderId: "fs", threadId: "t1", from: [petr], to: [jana],
        subject: "Re: Quarterly report", date: at(min), snippet: "Thanks", flags: [], hasAttachments: false, size: 0)
}

private let issue = IssueInfo(
    key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "Printer on the third floor jams",
    status: "In Progress", statusCategory: .inProgress, type: "Incident", priority: "High", assignee: "Petr Svoboda",
    reporter: "Jana Dvořáková", commentVisibilities: [.public, .internal])

/// A member of the issue ITSD-42 (thread tj of account j1).
private func issueMsg(_ id: String, _ min: Int, _ seen: Bool, _ item: MessageIssue) -> MessageSummary {
    var item = item
    item.info = issue
    return MessageSummary(
        id: MessageID(id), accountId: "j1", folderId: "space-itsd", threadId: "tj",
        from: [Address(name: "Jana Dvořáková", address: "")], subject: "ITSD-42: Printer on the third floor jams",
        date: at(min), snippet: "", flags: seen ? [.seen] : [], hasAttachments: false, size: 0, issue: item)
}

private func item(
    _ kind: IssueItemKind, changes: [IssueChange] = [], via: String? = nil, mine: Bool? = nil
) -> MessageIssue {
    MessageIssue(info: issue, item: kind, changes: changes, via: via, mine: mine)
}

private func statusEvent(_ id: String, _ min: Int, _ from: String, _ to: String) -> MessageSummary {
    issueMsg(id, min, false, item(.event, changes: [IssueChange(field: .status, from: from, to: to)]))
}

private func mailThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "a1", subject: "Quarterly report", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: mail("latest", 0, true), snippet: "", flags: [], hasAttachments: false,
        folderIds: [])
}

private func jiraThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "tj", accountId: "j1", subject: "", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: statusEvent("latest", 0, "", ""), snippet: "", flags: [], hasAttachments: false,
        folderIds: [], issue: issue)
}

/// An item of the model as "kind:id" ("more" for the truncated row).
private func shape(_ m: Conversation.Model) -> [String] {
    m.items.map { it in
        switch it.kind {
        case .message: return "msg:" + (it.message?.id.rawValue ?? "")
        case .event: return "event:" + (it.message?.id.rawValue ?? "")
        case .truncated: return "more"
        }
    }
}

/// A row of the chat as "kind:id@side" ("more@center" for the row of older
/// messages, "draft:<id>@right" for a draft).
private func chatShape(_ rows: [Conversation.ChatRow]) -> [String] {
    rows.map { r in
        let s: String
        switch r.kind {
        case .bubble: s = "msg:" + (r.item?.message?.id.rawValue ?? "")
        case .event: s = "event:" + (r.item?.message?.id.rawValue ?? "")
        case .truncated: s = "more"
        case .draft: s = "draft:" + (r.draft?.draft.id?.rawValue ?? "")
        }
        let side: String
        switch r.side {
        case .left: side = "left"
        case .right: side = "right"
        case .center: side = "center"
        }
        return s + "@" + side
    }
}

private func draftIDs(_ cards: [Conversation.DraftCard]) -> [String] {
    cards.map { $0.draft.id?.rawValue ?? "" }
}

/// A draft card as Go's composite literal makes it: only the draft set.
private func bareCard(_ id: String, _ min: Int) -> Conversation.DraftCard {
    Conversation.DraftCard(
        draft: Draft(id: DraftID(id), accountId: "", updatedAt: at(min)), sender: "", text: "", comment: false,
        internal: false, internalLabel: "")
}

struct ConversationChatTests {
    @Test func chatRowsMail() {
        var own = mail("m2", 10, true)
        own.from = [petr]
        // The user's reply in Sent from another identity: not the account's
        // address, so not mine, and still on the right.
        var other = reply("r1", 15)
        other.from = [Address(name: "Petr", address: "petr@home.example")]
        let members = [mail("m3", 20, false), own, mail("m1", 0, true)]
        let sent = [reply("r2", 30), other]
        let m = Conversation.build(mailThread(5), members, sent: sent, account: mailAccount)
        #expect(m.earlier == 2)
        let r1 = m.items[m.index("r1")]
        #expect(!r1.mine && r1.sent, "the reply from another identity: mine \(r1.mine), sent \(r1.sent)")
        let drafts = Conversation.drafts(
            [
                Draft(id: "d2", accountId: "", textBody: "newer", updatedAt: at(50)),
                Draft(id: "d1", accountId: "", textBody: "older", updatedAt: at(40)),
                Draft(id: "d3", accountId: "", textBody: "newest", updatedAt: at(60)),
            ], account: mailAccount)
        let rows = Conversation.chatRows(m, drafts: drafts)
        #expect(chatShape(rows) == [
            "more@center",
            "msg:m1@left", "msg:m2@right", "msg:r1@right", "msg:m3@left", "msg:r2@right",
            "draft:d1@right", "draft:d2@right", "draft:d3@right",
        ])
        guard rows.count == 9 else { return }
        #expect(rows[0].item?.text == "2 earlier messages are not shown")
        #expect(rows[2].item == m.items[m.index("m2")] && rows[6].draft?.text == "older")
        #expect(rows[7].item == nil && rows[1].draft == nil, "a draft row carries an item, or a bubble a draft")
        let to = Conversation.chatScrollTo(rows)
        #expect(to == rows.count - 1 && rows[to].draft?.draft.id == "d3")

        // Without drafts the chat opens at the newest message.
        let plain = Conversation.chatRows(m, drafts: [])
        let plainTo = Conversation.chatScrollTo(plain)
        #expect(plainTo == plain.count - 1 && plain[plainTo].item?.message?.id == "r2")
        // The model is not changed.
        #expect(shape(m) == ["more", "msg:m1", "msg:m2", "msg:r1", "msg:m3", "msg:r2"])
    }

    /// Drafts saved at the same time keep the order given; the list given
    /// is not reordered.
    @Test func chatRowsDraftOrder() {
        let m = Conversation.build(mailThread(1), [mail("m1", 0, true)], account: mailAccount)
        let drafts = [bareCard("c", 20), bareCard("b", 10), bareCard("a", 10), bareCard("z", 5)]
        let given = draftIDs(drafts)
        let rows = Conversation.chatRows(m, drafts: drafts)
        #expect(chatShape(rows) == ["msg:m1@left", "draft:z@right", "draft:b@right", "draft:a@right", "draft:c@right"])
        #expect(draftIDs(drafts) == given)
    }

    @Test func chatRowsJira() {
        let members = [
            issueMsg("d", 0, true, item(.description, mine: true)),
            issueMsg("c1", 10, true, item(.comment)),
            statusEvent("e1", 20, "To Do", "In Progress"),
            issueMsg("c2", 30, true, item(.comment, mine: true)),
            // The site says it is the user's, but an integration posted it.
            issueMsg("c3", 40, false, item(.comment, via: "Issue Sync", mine: true)),
            statusEvent("e2", 50, "In Progress", "Done"),
        ]
        let m = Conversation.build(jiraThread(7), members, account: jiraAccount)
        let drafts = Conversation.drafts(
            [
                Draft(
                    id: "k1", accountId: "j1", textBody: "On it", updatedAt: at(60),
                    comment: DraftComment(issue: issue, visibility: .internal))
            ], account: jiraAccount)
        let rows = Conversation.chatRows(m, drafts: drafts)
        #expect(chatShape(rows) == [
            "more@center",
            "msg:d@right", "msg:c1@left", "event:e1@center", "msg:c2@right", "msg:c3@left", "event:e2@center",
            "draft:k1@right",
        ])
        guard rows.count == 8 else { return }
        #expect(rows[5].item?.via.isEmpty == false && rows[5].item?.mine == false, "the relayed comment")
        #expect(rows[7].draft?.internal == true && rows[7].draft?.comment == true, "the comment draft")
        #expect(Conversation.chatScrollTo(rows) == 7)
    }

    @Test func chatRowsEmptyAndEventsOnly() {
        let empty = Conversation.chatRows(Conversation.Model(thread: "t1"), drafts: [])
        #expect(empty.isEmpty && Conversation.chatScrollTo(empty) == -1)
        #expect(Conversation.chatScrollTo([]) == -1)
        // An empty model with older members left out (remove took the last
        // shown one) has no rows either: the pane loads it again.
        let gone = Conversation.remove(
            Conversation.build(mailThread(3), [mail("m1", 0, true)], account: mailAccount), "m1")
        #expect(Conversation.chatRows(gone, drafts: []).isEmpty && gone.earlier == 2)

        let events = Conversation.build(
            jiraThread(2),
            [statusEvent("e2", 10, "In Progress", "Done"), statusEvent("e1", 0, "To Do", "In Progress")],
            account: jiraAccount)
        let rows = Conversation.chatRows(events, drafts: [])
        #expect(chatShape(rows) == ["event:e1@center", "event:e2@center"])
        #expect(Conversation.chatScrollTo(rows) == 1 && rows.first?.item?.eventLines.isEmpty == false)
    }

    /// The text of a bubble is cleaned as a draft card's is; these follow
    /// `Board.cleanBlock`. Invalid UTF-8 reaches Swift as U+FFFD (the
    /// daemon's JSON and Swift's decoding replace it), which goes.
    @Test func bubbleText() {
        let invalid = String(decoding: [0x6F, 0x6B, 0xFF, 0xFE, 0x21] as [UInt8], as: UTF8.self)
        let cases: [(String, String, String)] = [
            ("empty", "", ""),
            ("plain", "Hello", "Hello"),
            ("CRLF", "one\r\ntwo\r\n", "one\ntwo"),
            ("lone CR", "one\rtwo", "one\ntwo"),
            ("NEL and line separator", "one\u{85}two\u{2028}three\u{2029}four", "one\ntwo\nthree\nfour"),
            ("control characters", "a\u{0}b\u{7}c\u{1B}d\u{7F}e", "abcde"),
            ("bidi and invisible", "Hi" + rlo + " there" + zwsp, "Hi there"),
            ("blank lines", "a\n\n\n\n\nb", "a\n\nb"),
            ("blank lines with spaces", "a\n \t\n  \n\nb  \n", "a\n\nb"),
            ("only whitespace", " \n\t\r\n   \n\n", ""),
            ("markup stays text", "<b>bold</b> & <script>x</script>", "<b>bold</b> & <script>x</script>"),
            ("invalid UTF-8", invalid, "ok!"),
        ]
        for (name, input, want) in cases {
            #expect(Conversation.bubbleText(input) == want, "\(name)")
        }
    }

    @Test func bubbleTextCapAndParity() {
        let huge = String(repeating: "Řádek dlouhé zprávy.\r\n", count: (1 << 20) / 24 + 1)
        #expect(huge.utf8.count >= 1 << 20, "the input is only \(huge.utf8.count) bytes")
        let got = Conversation.bubbleText(huge)
        #expect(!got.isEmpty && got.utf8.count <= Conversation.draftTextBytes, "1 MiB in: \(got.utf8.count) bytes out")
        #expect(got.hasPrefix("Řádek dlouhé zprávy.\nŘádek"))

        // A message bubble and a draft bubble read alike.
        for s in [huge, "Hi" + rlo + "\r\n\r\n\r\nBye\u{0}", " \n ", "plain"] {
            let card = Conversation.drafts(
                [Draft(id: "d", accountId: "", textBody: s, updatedAt: at(0))], account: mailAccount)[0]
            #expect(Conversation.bubbleText(s) == card.text, "bubble and draft differ for \(s.prefix(20))…")
        }
    }

    @Test func chatTexts() {
        #expect(Conversation.chatToggleLabel(on: false) == "Show as Chat")
        #expect(
            Conversation.chatToggleTooltip(on: false)
                == "Others on the left, your messages on the right; double-click opens the message")
        #expect(Conversation.chatToggleLabel(on: true) == "Show as Thread")
        #expect(
            Conversation.chatToggleTooltip(on: true) == "Back to the timeline: the messages as cards, newest first")
        #expect(Conversation.chatHTMLRowTitle() == "Render HTML in Chat")
        #expect(
            Conversation.chatHTMLRowSubtitle()
                == "Messages in the chat layout as they were sent, in a locked web view, instead of their plain text")
    }

    /// chat_test.go: the shortened bubbles' rule, the expanded set and
    /// the texts.
    @Test func bubbleShortenedAndExpanded() {
        #expect(Conversation.chatPreviewLines == 3)
        // chat_test.go TestBubbleShortened: all 16 rows; the side's own
        // setting, never once expanded.
        for mineOn in [false, true] {
            for othersOn in [false, true] {
                for mine in [false, true] {
                    for expanded in [false, true] {
                        #expect(
                            Conversation.bubbleShortened(mineOn: mineOn, othersOn: othersOn, mine: mine, expanded: expanded)
                                == (!expanded && (mine ? mineOn : othersOn)),
                            "mineOn \(mineOn) othersOn \(othersOn) mine \(mine) expanded \(expanded)")
                    }
                }
            }
        }
        #expect(Conversation.bubbleShortened(mineOn: true, othersOn: false, mine: true, expanded: false))
        #expect(!Conversation.bubbleShortened(mineOn: true, othersOn: false, mine: false, expanded: false))
        var e = Conversation.Expanded()
        #expect(!e.is(MessageID("m1")) && !e.isDraft(DraftID("d1")), "zero value usable")
        e.expand(MessageID("m1"))
        e.expand(MessageID(""))
        e.expandDraft(DraftID("d1"))
        e.expandDraft(DraftID(""))
        #expect(e.is(MessageID("m1")) && !e.is(MessageID("m2")) && !e.is(MessageID("")))
        #expect(e.isDraft(DraftID("d1")) && !e.isDraft(DraftID("m1")) && !e.isDraft(DraftID("")))
        #expect(!e.is(MessageID("d1")), "a draft's id is not a message's")
        e.expand(MessageID("m1"))
        #expect(e.is(MessageID("m1")), "expanding again keeps it")
        e.reset()
        #expect(!e.is(MessageID("m1")) && !e.isDraft(DraftID("d1")))
        #expect(e == Conversation.Expanded())
        #expect(Conversation.chatShowMore() == "Show More")
        #expect(Conversation.chatPreferencesGroupTitle() == "Conversations")
        #expect(Conversation.chatTruncateMineRowTitle() == "Shorten My Messages in Chat")
        #expect(Conversation.chatTruncateOthersRowTitle() == "Shorten Others' Messages in Chat")
        #expect(Conversation.chatTruncateRowSubtitle()
            == "About three lines of each message in its bubble; a click shows the whole message")
    }

    /// chat_test.go TestBubbleRendersHTML: only the preference on, HTML
    /// present and not withheld.
    @Test func bubbleRendersHTML() {
        for setting in [false, true] {
            for hasHTML in [false, true] {
                for withheld in [false, true] {
                    #expect(
                        Conversation.bubbleRendersHTML(setting: setting, hasHTML: hasHTML, withheld: withheld)
                            == (setting && hasHTML && !withheld),
                        "setting \(setting), hasHTML \(hasHTML), withheld \(withheld)")
                }
            }
        }
    }
}

/// The macOS chat's own rules (ConversationChatBody.swift): which variant
/// of a body a bubble reads, its text in every state, the end of the chat.
@MainActor
struct ConversationChatBodyTests {
    private func body(_ text: String, _ state: BodyState = .fetched, trimmed: Bool = false) -> MessageBodyResult {
        MessageBodyResult(
            messageId: "m1", bodyState: state, hasHtml: false, text: text, remoteContent: .block, sanitizerVersion: "1",
            quotedTrimmed: trimmed)
    }

    @Test func chatBodyReadsTheTrimmedVariant() {
        // Trimmed and shown: the body.
        let lm = LoadedMessage()
        lm.store(body("Hi", trimmed: true), quoted: false)
        #expect(Conversation.chatBody(of: lm, quotedRevealed: lm.quotedShown)?.text == "Hi")

        // The user revealed the quoted history: the trimmed variant kept
        // aside, not the whole body shown.
        lm.showQuoted(true)
        lm.store(body("Hi\n> quoted"), quoted: true)
        #expect(lm.quotedShown)
        #expect(Conversation.chatBody(of: lm, quotedRevealed: lm.quotedShown)?.text == "Hi")
        #expect(Conversation.chatBody(of: lm, quotedRevealed: false)?.text == "Hi\n> quoted")

        // Revealed before the trimmed variant was ever fetched: the whole
        // body is all there is.
        let whole = LoadedMessage()
        whole.showQuoted(true)
        whole.store(body("Hi\n> quoted"), quoted: true)
        #expect(Conversation.chatBody(of: whole, quotedRevealed: true)?.text == "Hi\n> quoted")

        // Nothing yet.
        #expect(Conversation.chatBody(of: LoadedMessage(), quotedRevealed: false) == nil)
    }

    @Test func bubbleBodyStates() {
        let snippet = "  See the\r\nfigures  "
        #expect(Conversation.bubbleBody(nil, snippet: snippet) == .placeholder("See the\nfigures"))
        #expect(Conversation.bubbleBody(LoadedMessage(), snippet: snippet) == .placeholder("See the\nfigures"))

        let lm = LoadedMessage()
        lm.store(body("Hello\r\n\r\n\r\nBye\r\n"), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("Hello\n\nBye"))

        lm.store(body(" \n\t "), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("(Empty message)"))
        lm.store(body("", .pending), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("Downloading…"))
        lm.store(body("", .tooBig), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("This message is too large to download."))
        lm.store(body("", .failed), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("This message could not be read."))

        // The call failed: the cards' sentence.
        let failed = LoadedMessage()
        failed.err = RPCClient.ClientError.timeout(method: "message.body")
        #expect(Conversation.bubbleBody(failed, snippet: snippet)
            == .text(rpcErrorText("Loading the message", RPCClient.ClientError.timeout(method: "message.body"))))

        // The whole body failed after a reveal: the trimmed variant kept
        // aside still reads.
        let aside = LoadedMessage()
        aside.store(body("Short"), quoted: false)
        aside.showQuoted(true)
        aside.err = RPCClient.ClientError.timeout(method: "message.body")
        #expect(Conversation.bubbleBody(aside, snippet: snippet) == .text("Short"))
    }

    @Test func bubbleBodyHTML() {
        func html(_ state: BodyState = .fetched, withheld: Bool = false, html: String? = "<p>Hi</p>") -> MessageBodyResult {
            MessageBodyResult(
                messageId: "m1", bodyState: state, hasHtml: true, html: withheld ? nil : html,
                htmlWithheld: withheld ? true : nil, text: "Hi", remoteContent: .block, sanitizerVersion: "1",
                quotedTrimmed: true)
        }
        let snippet = "Hi there"
        let lm = LoadedMessage()
        lm.store(html(), quoted: false)
        // The preference off (the default): the text, as before.
        #expect(Conversation.bubbleBody(lm, snippet: snippet) == .text("Hi"))
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: false) == .text("Hi"))
        // On: the body the card shows, and the text for a bubble without a web view.
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: true) == .html(body: html(), text: "Hi"))

        // Withheld, empty HTML, a text-only body: the text.
        lm.store(html(withheld: true), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: true) == .text("Hi"))
        lm.store(html(html: ""), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: true) == .text("Hi"))
        lm.store(body("Plain"), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: true) == .text("Plain"))
        // On its way: the cards' sentence, never a web view.
        lm.store(html(.pending), quoted: false)
        #expect(Conversation.bubbleBody(lm, snippet: snippet, html: true) == .text("Downloading…"))

        // Nothing loaded yet, or the call failed: the placeholder, the error.
        #expect(Conversation.bubbleBody(nil, snippet: snippet, html: true) == .placeholder("Hi there"))
        let failed = LoadedMessage()
        failed.err = RPCClient.ClientError.timeout(method: "message.body")
        #expect(Conversation.bubbleBody(failed, snippet: snippet, html: true)
            == .text(rpcErrorText("Loading the message", RPCClient.ClientError.timeout(method: "message.body"))))

        // The quoted history revealed in the timeline: the trimmed variant
        // kept aside is the document, as for the text.
        let aside = LoadedMessage()
        aside.store(html(), quoted: false)
        aside.showQuoted(true)
        aside.store(MessageBodyResult(
            messageId: "m1", bodyState: .fetched, hasHtml: true, html: "<p>Hi</p><blockquote>q</blockquote>",
            text: "Hi\n> q", remoteContent: .block, sanitizerVersion: "1"), quoted: true)
        #expect(Conversation.bubbleBody(aside, snippet: snippet, html: true) == .html(body: html(), text: "Hi"))
        #expect(!Conversation.bubbleMonospace(aside, setting: true))
    }

    @Test func bubbleAttachmentBadge() {
        func summary(_ has: Bool) -> MessageSummary {
            MessageSummary(
                id: "m1", accountId: "a1", folderId: "f1", from: [], subject: "", date: .goZero, snippet: "", flags: [],
                hasAttachments: has, size: 0)
        }
        func file(_ part: String, cid: String? = nil) -> MalachiCore.Attachment {
            MalachiCore.Attachment(
                partId: part, filename: part + ".pdf", contentType: "application/pdf", size: 1000, inline: cid != nil,
                contentId: cid)
        }
        // Nothing known but the summary: the pill alone, or none.
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(true), loaded: nil) == .some)
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(false), loaded: nil) == .none)
        #expect(Conversation.bubbleAttachmentBadge(summary: nil, loaded: nil) == .none)
        // The body alone (message.get not answered, or failed): still the
        // summary's word.
        let bodyOnly = LoadedMessage()
        bodyOnly.store(body("Hi"), quoted: false)
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(true), loaded: bodyOnly) == .some)
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(false), loaded: bodyOnly) == .none)

        // message.get answered: the number of the timeline's chips.
        let two = LoadedMessage()
        two.store(body("Hi"), quoted: false)
        two.msg = Message(summary: summary(true), attachments: [file("2"), file("3")])
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(true), loaded: two) == .count(2))
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(true), loaded: two)
            == .count(ChipPlan(two.msg, two.body).chips.count))

        // A part the daemon calls inline (a Content-ID) leaves the
        // summary's flag false, and the timeline still shows its chip
        // (a plain-text body shows no part itself): so does the bubble.
        let inline = LoadedMessage()
        inline.store(body("See the attached"), quoted: false)
        inline.msg = Message(summary: summary(false), attachments: [file("2", cid: "scan@x")])
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(false), loaded: inline) == .count(1))

        // An image the HTML body shows through cid: no chip, no pill.
        let shown = LoadedMessage()
        shown.store(
            MessageBodyResult(
                messageId: "m1", bodyState: .fetched, hasHtml: true, html: "<img src=\"cid:logo@x\">", text: "",
                inlineParts: ["logo@x": "2"], remoteContent: .block, sanitizerVersion: "1"),
            quoted: false)
        shown.msg = Message(summary: summary(false), attachments: [file("2", cid: "logo@x")])
        #expect(Conversation.bubbleAttachmentBadge(summary: summary(true), loaded: shown) == .none)
    }

    @Test func chatEnd() {
        #expect(ConversationLayout.chatAtEnd(top: 600, viewportHeight: 400, documentHeight: 1000))
        #expect(ConversationLayout.chatAtEnd(top: 597, viewportHeight: 400, documentHeight: 1000))
        #expect(!ConversationLayout.chatAtEnd(top: 590, viewportHeight: 400, documentHeight: 1000))
        #expect(ConversationLayout.chatAtEnd(top: 0, viewportHeight: 400, documentHeight: 300), "shorter than the view")
        #expect(ConversationLayout.chatEndTop(viewportHeight: 400, documentHeight: 1000) == 600)
        #expect(ConversationLayout.chatEndTop(viewportHeight: 400, documentHeight: 300) == 0)
        #expect(ConversationLayout.chatColumn(pane: 1200) == 868)
        #expect(ConversationLayout.chatColumn(pane: 500) == 468)
        #expect(ConversationLayout.chatColumn(pane: 20) == 0)
    }

    @Test func chatCompactDates() {
        // The bubble's widest is column - 40 - max(24, column / 5).
        let wide = ConversationLayout.chatColumn(pane: 1200) // 868: bubble 654.4
        #expect(!ConversationLayout.chatCompactDates(column: wide))
        // 600: bubble 440, less 28 of padding: 412 < 420.
        #expect(ConversationLayout.chatCompactDates(column: 600))
        // The cards of the same pane (632) still show the full date.
        #expect(!ConversationLayout.compactDates(pane: 632))
        // The boundary: 0.8 * c - 40 - 28 == 420 at c = 610.
        #expect(!ConversationLayout.chatCompactDates(column: 610))
        #expect(ConversationLayout.chatCompactDates(column: 609))
        #expect(ConversationLayout.chatCompactDates(column: 0))
    }

    @Test func chatResize() {
        #expect(ConversationLayout.chatResize(
            from: (500, 400), to: (500.2, 400.3), pinned: true, wasAtEnd: true) == nil)
        // Only the height: no reflow, the end stays in view when it was.
        #expect(ConversationLayout.chatResize(from: (500, 400), to: (500, 300), pinned: false, wasAtEnd: true)
            == .init(width: false, toEnd: true))
        #expect(ConversationLayout.chatResize(from: (500, 400), to: (500, 300), pinned: false, wasAtEnd: false)
            == .init(width: false, toEnd: false))
        #expect(ConversationLayout.chatResize(from: (500, 400), to: (500, 500), pinned: true, wasAtEnd: false)
            == .init(width: false, toEnd: true))
        // The width: a reflow.
        #expect(ConversationLayout.chatResize(from: (500, 400), to: (700, 400), pinned: false, wasAtEnd: false)
            == .init(width: true, toEnd: false))
    }

    @Test func bubbleMonospace() {
        func body(_ state: BodyState, html: Bool) -> MessageBodyResult {
            MessageBodyResult(
                messageId: "m1", bodyState: state, hasHtml: html, text: "x", remoteContent: .block,
                sanitizerVersion: "1")
        }
        let plain = LoadedMessage()
        plain.store(body(.fetched, html: false), quoted: false)
        #expect(Conversation.bubbleMonospace(plain, setting: true))
        #expect(!Conversation.bubbleMonospace(plain, setting: false))
        let html = LoadedMessage()
        html.store(body(.fetched, html: true), quoted: false)
        #expect(!Conversation.bubbleMonospace(html, setting: true), "text derived from HTML")
        let pending = LoadedMessage()
        pending.store(body(.pending, html: false), quoted: false)
        #expect(!Conversation.bubbleMonospace(pending, setting: true))
        #expect(!Conversation.bubbleMonospace(nil, setting: true), "the placeholder")
        #expect(!Conversation.bubbleMonospace(LoadedMessage(), setting: true))
        let failed = LoadedMessage()
        failed.err = RPCClient.ClientError.timeout(method: "message.body")
        #expect(!Conversation.bubbleMonospace(failed, setting: true))
    }
}

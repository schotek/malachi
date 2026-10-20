// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/conversation/reply_test.go (English catalogue: the msgids come
// back verbatim), with the builders of conversation_test.go as
// ConversationChatTests.swift has them. Go's (Item, bool) is an optional.

/// Characters the text keeps as typed, as scalar constants: the source stays
/// free of invisible characters.
private let rlo = jiraScalar(0x202E) // RIGHT-TO-LEFT OVERRIDE
private let lri = jiraScalar(0x2066) // LEFT-TO-RIGHT ISOLATE
private let zwsp = jiraScalar(0x200B) // ZERO WIDTH SPACE
private let lrm = jiraScalar(0x200E) // LEFT-TO-RIGHT MARK
private let bom = jiraScalar(0xFEFF) // ZERO WIDTH NO-BREAK SPACE
private let nbsp = jiraScalar(0x00A0)
private let ideographicSpace = jiraScalar(0x3000)

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

private func mailThread(_ count: Int) -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "a1", subject: "Quarterly report", participants: [], messageCount: count, unreadCount: 0,
        latestDate: .goZero, latest: mail("latest", 0, true), snippet: "", flags: [], hasAttachments: false,
        folderIds: [])
}

private let issueInfo = IssueInfo(key: "ITSD-42", url: "", summary: "Printer jams", status: "In Progress")

/// A member of the issue ITSD-42 (thread tj of account j1), of item `item`.
private func issueMsg(_ id: String, _ min: Int, _ seen: Bool, _ item: MessageIssue) -> MessageSummary {
    MessageSummary(
        id: MessageID(id), accountId: "j1", folderId: "space-itsd", threadId: "tj",
        from: [Address(name: "Jana Dvořáková", address: "")], subject: "ITSD-42: Printer jams",
        date: at(min), snippet: "", flags: seen ? [.seen] : [], hasAttachments: false, size: 0, issue: item)
}

private func statusEvent(_ id: String, _ min: Int, _ from: String, _ to: String) -> MessageSummary {
    issueMsg(id, min, false, MessageIssue(
        info: issueInfo, item: .event, changes: [IssueChange(field: .status, from: from, to: to)]))
}

/// reply_test.go msgItem.
private func msgItem(_ id: String, mine: Bool, sent: Bool) -> Conversation.Item {
    var it = Conversation.Item(kind: .message, message: mail(id, 0, true))
    it.mine = mine
    it.sent = sent
    return it
}

struct ConversationReplyTests {
    @Test func quickReplyTarget() {
        let event = Conversation.Item(kind: .event, message: mail("e", 0, true))
        var more = Conversation.Item(kind: .truncated)
        more.text = "2 earlier messages"
        let cases: [(name: String, items: [Conversation.Item], want: String?)] = [
            ("empty", [], nil),
            ("events and the row of older messages only", [more, event], nil),
            ("the newest of someone else",
             [more, msgItem("a", mine: false, sent: false), msgItem("b", mine: false, sent: false),
              msgItem("c", mine: true, sent: false)], "b"),
            ("a sent card is the user's", [msgItem("a", mine: false, sent: false), msgItem("s", mine: false, sent: true)], "a"),
            ("an event after the message is skipped", [msgItem("a", mine: false, sent: false), event], "a"),
            ("all the user's: the newest",
             [msgItem("a", mine: true, sent: false), msgItem("s", mine: false, sent: true),
              msgItem("c", mine: true, sent: false), event], "c"),
            ("a sent card as the newest of the user's",
             [msgItem("a", mine: true, sent: false), msgItem("s", mine: false, sent: true)], "s"),
        ]
        for c in cases {
            var m = Conversation.Model(thread: "t1")
            m.items = c.items
            let got = Conversation.quickReplyTarget(m)
            #expect(got?.message?.id.rawValue == c.want, "\(c.name)")
            if let got {
                #expect(got.kind == .message, "\(c.name)")
            }
        }
    }

    /// The target of a model `build` made: the user's reply in Sent and their
    /// own member are passed over for the newest message of someone else.
    @Test func quickReplyTargetOfABuiltModel() {
        var own = mail("m3", 20, true)
        own.from = [petr]
        let members = [mail("m1", 0, true), mail("m2", 10, false), own]
        let m = Conversation.build(mailThread(3), members, sent: [reply("s1", 30)], account: mailAccount)
        #expect(Conversation.quickReplyTarget(m)?.message?.id == "m2")
    }

    @Test func quickReplyModes() {
        let cases: [(name: String, a: Capabilities.Actions, want: [Conversation.QuickReplyMode])] = [
            ("none", Capabilities.Actions(), []),
            ("mail", Capabilities.Actions(reply: true, replyAll: true, forward: true, trash: true), [.reply, .replyAll, .forward]),
            ("reply only", Capabilities.Actions(reply: true), [.reply]),
            ("forward only", Capabilities.Actions(forward: true), [.forward]),
            ("comment", Capabilities.Actions(reply: true, forward: true, comment: true), [.reply]),
        ]
        for c in cases {
            #expect(Conversation.quickReplyModes(c.a) == c.want, "\(c.name)")
        }
        let modes: [Conversation.QuickReplyMode: ComposeMode] = [.reply: .reply, .replyAll: .replyAll, .forward: .forward]
        for (mode, want) in modes {
            #expect(mode.composeMode == want, "\(mode)")
        }
        let kinds: [Conversation.QuickReplyMode: ComposeKind] = [.reply: .reply, .replyAll: .replyAll, .forward: .forward]
        for (mode, want) in kinds {
            #expect(mode.composeKind == want, "\(mode)")
        }
    }

    @Test func quickReplyHTML() {
        let cases: [(name: String, in: String, want: String)] = [
            ("empty", "", ""),
            ("blank", " \n\t\n\r\n", ""),
            ("one line", "Thanks", "<p>Thanks</p>"),
            ("lines", "Hi,\nsee you", "<p>Hi,<br>see you</p>"),
            ("paragraphs", "One\n\n\n\nTwo\nthree", "<p>One</p><p>Two<br>three</p>"),
            ("blank lines around", "\n\n  \nOne\n\n", "<p>One</p>"),
            ("a line of spaces splits", "One\n   \nTwo", "<p>One</p><p>Two</p>"),
            ("CRLF and CR", "a\r\nb\rc\r\n\r\nd", "<p>a<br>b<br>c</p><p>d</p>"),
            ("escaped", "<script>alert(\"x\")</script> & 'y'",
             "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; &#39;y&#39;</p>"),
            ("markup stays text", "<p>a</p><br><img src=x onerror=y>",
             "<p>&lt;p&gt;a&lt;/p&gt;&lt;br&gt;&lt;img src=x onerror=y&gt;</p>"),
            ("markdown is not read", "**bold** _x_\n- item", "<p>**bold** _x_<br>- item</p>"),
            // The user's own text: bidi and invisible characters pass as
            // typed (the daemon's sanitiser and the plain-text rules see
            // them).
            ("bidi", "a\(rlo)b\(lri)c\(zwsp)", "<p>a\(rlo)b\(lri)c\(zwsp)</p>"),
            ("leading spaces kept", "  indented", "<p>  indented</p>"),
        ]
        for c in cases {
            #expect(Conversation.quickReplyHTML(c.in) == c.want, "\(c.name)")
        }
        // Never cut, even over the daemon's cap.
        let big = String(repeating: "x", count: API.Limits.maxDraftBodyBytes + 10)
        let got = Conversation.quickReplyHTML(big)
        #expect(got == "<p>" + big + "</p>", "1 MiB: \(got.utf8.count) bytes")
        let many = String(repeating: "<\n", count: 1 << 16)
        let lines = String(repeating: "&lt;<br>", count: 1 << 16)
        let want = "<p>" + lines.dropLast("<br>".count) + "</p>"
        let gotMany = Conversation.quickReplyHTML(many)
        #expect(gotMany == want, "many lines: \(gotMany.utf8.count) bytes, want \(want.utf8.count)")
    }

    @Test func quickReplyBlank() {
        let cases: [String: Bool] = [
            "": true,
            " \t\r\n": true,
            "\(nbsp)\(ideographicSpace)": true,
            "\(zwsp)\(lrm)\(bom)": true,
            "\u{0}\u{7}": true,
            "\(rlo) \(lri)": true,
            "x": false,
            "  .  ": false,
            "\(zwsp)x": false,
        ]
        for (text, want) in cases {
            #expect(Conversation.quickReplyBlank(text) == want, "\(text.unicodeScalars.map { $0.value })")
        }
    }

    /// The body is byte for byte what the compose window saves with
    /// "Include" checked for the same own HTML.
    @Test func quickReplyBody() {
        let original = "<p><br></p><div>A wrote:</div><blockquote type=\"cite\"><p>Hi &amp; bye</p><img src=\"cid:i1\"></blockquote>"
        let d = Draft(id: "d1", accountId: "", textBody: "A wrote: Hi & bye", htmlBody: original)
        let own = "Thanks!\n\nP."
        for (mode, kind) in [(ComposeMode.reply, ComposeKind.reply), (.replyAll, .replyAll), (.forward, .forward)] {
            let got = Conversation.quickReplyBody(mode: mode, own: own, draft: d)
            #expect(Array(got.utf8) == Array(("<p>Thanks!</p><p>P.</p>\n" + original).utf8), "\(mode.rawValue)")
            let window = ComposeBodyLayout(mode: mode, comment: false, bodyHTML: ComposeBodyLayout.draftBody(d))
                .html(own: Conversation.quickReplyHTML(own), includeQuote: true)
            #expect(Array(got.utf8) == Array(window.utf8), "\(mode.rawValue): differs from the window's")
            // The window's own layout of fromDraft's params, too.
            let params = fromDraft(kind: kind, draft: d, blocked: BlockedContent())
            let fromWindow = ComposeBodyLayout(params).html(own: Conversation.quickReplyHTML(own), includeQuote: true)
            #expect(Array(got.utf8) == Array(fromWindow.utf8), "\(mode.rawValue): differs from fromDraft's window")
        }
        // A draft without HTML quotes its text, escaped.
        let text = Draft(accountId: "", textBody: "> <x>\r\n> y")
        #expect(Conversation.quickReplyBody(mode: .reply, own: "ok", draft: text) == "<p>ok</p>\n&gt; &lt;x&gt;<br>&gt; y")
        // A comment has no original apart: the own HTML alone.
        let comment = Draft(
            accountId: "", htmlBody: "<p>ignored</p>", comment: DraftComment(issue: IssueInfo(key: "", url: "", summary: "", status: "", statusCategory: .todo), visibility: .public))
        #expect(Conversation.quickReplyBody(mode: .reply, own: "a <b>", draft: comment) == "<p>a &lt;b&gt;</p>")
        // No original at all.
        #expect(Conversation.quickReplyBody(mode: .reply, own: "ok", draft: Draft(accountId: "")) == "<p>ok</p>")
    }

    @Test func quickReplyTexts() {
        #expect(Conversation.quickReplyPlaceholder(comment: false) == "Write a reply…")
        #expect(Conversation.quickReplyPlaceholder(comment: true) == "Write a comment…")
        #expect(Conversation.quickReplyTargetTooltip(.reply, sender: "Jana Nováková") == "Reply to: Jana Nováková")
        #expect(Conversation.quickReplyTargetTooltip(.replyAll, sender: "Jana") == "Reply all to: Jana")
        // A sender with a percent sign stays as it is (a format argument).
        #expect(Conversation.quickReplyTargetTooltip(.reply, sender: "100%s") == "Reply to: 100%s")
        for (mode, sender) in [(Conversation.QuickReplyMode.forward, "Jana"), (.reply, ""), (.replyAll, "")] {
            #expect(Conversation.quickReplyTargetTooltip(mode, sender: sender) == "", "\(mode) \(sender)")
        }
        #expect(Conversation.quickReplyOpenWindowLabel() == "Open in a Window")
        #expect(Conversation.quickReplyForwardLabel() == "Forward…")
    }

    @Test func quickReplyIconOnly() {
        // The bubbles' short-date rule of the pane's column (pane - 32 up
        // to 900): compact below a column of 610.
        #expect(!ConversationLayout.quickReplyIconOnly(pane: 1200))
        #expect(!ConversationLayout.quickReplyIconOnly(pane: 642))
        #expect(ConversationLayout.quickReplyIconOnly(pane: 641))
        #expect(ConversationLayout.quickReplyIconOnly(pane: 400))
        #expect(ConversationLayout.quickReplyIconOnly(pane: 0))
    }

    /// reply_test.go TestSingleModel: one message shown alone is a model of
    /// one message card, which is the quick reply's target whoever wrote
    /// it; an issue's event has none.
    @Test func singleModel() {
        var s = mail("m1", 0, false)
        s.from = [Address(name: "Jana" + rlo + " Dvořáková\n", address: "jana@acme.example")]
        var m = Conversation.singleModel(s, mine: false)
        #expect(m.thread == "t1" && m.items.count == 1 && m.scrollTo == 0 && m.markRead == nil && m.issue == nil
            && m.earlier == 0)
        let it = m.items[0]
        #expect(it.kind == .message && it.message?.id == "m1" && !it.mine && it.unread && !it.sent)
        #expect(it.sender == Conversation.sender(s.from) && !it.sender.contains(rlo) && !it.sender.contains("\n"))
        #expect(Conversation.quickReplyTarget(m)?.message?.id == "m1")

        // The user's own message (Sent): still the target.
        var own = mail("m2", 5, true)
        own.from = [petr]
        m = Conversation.singleModel(own, mine: true)
        let target = Conversation.quickReplyTarget(m)
        #expect(target?.message?.id == "m2" && target?.mine == true && target?.unread == false)
        // `isMine` is the rule of `Item.mine` (the account's own address).
        #expect(Conversation.isMine(own, account: mailAccount) && !Conversation.isMine(s, account: mailAccount))
        #expect(!Conversation.isMine(own, account: nil))

        // An issue's description is a message; an event is none.
        let desc = issueMsg("d", 0, true, MessageIssue(info: issueInfo, item: .description))
        #expect(Conversation.quickReplyTarget(Conversation.singleModel(desc, mine: false))?.message?.id == "d")
        let ev = Conversation.singleModel(statusEvent("e", 1, "Open", "Done"), mine: false)
        #expect(ev.items.count == 1 && ev.items.first?.kind == .event && ev.items.first?.unread == false
            && ev.thread == "tj")
        #expect(Conversation.quickReplyTarget(ev) == nil, "an event is a target")

        // No id: an empty model, no target.
        let empty = Conversation.singleModel(MessageSummary(
            id: "", accountId: "a1", folderId: "f1", threadId: "t9", from: [], to: [], subject: "", date: at(0),
            snippet: "", flags: [], hasAttachments: false, size: 0), mine: false)
        #expect(empty.items.isEmpty && empty.thread == "t9" && empty.scrollTo == -1)
        #expect(Conversation.quickReplyTarget(empty) == nil, "an empty model has a target")
        // No thread: the model's thread is "".
        var orphan = mail("m3", 0, true)
        orphan.threadId = nil
        #expect(Conversation.singleModel(orphan, mine: false).thread == "")
    }
}

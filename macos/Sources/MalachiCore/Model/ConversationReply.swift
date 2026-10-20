// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/reply.go: the chat layout of a conversation in
// Mail ends with a quick-reply bar, as a messaging app does: a fixed row
// under the bubbles with a button for the mode (Reply, Reply All, and
// Forward…, which opens the compose window straight away), a field of one
// to five lines for plain text, a button that opens the compose window with
// what was typed, and Send. The timeline, the board's chat and the message
// window have no bar. The same bar stands under the reading pane's page of
// a single message (docs/plans/chat-view.md §12), which comes to it as a
// model of one member (`singleModel`).
//
// The reply goes to the newest message of someone else in the conversation
// (the newest message when the user wrote all of them; `quickReplyTarget`),
// in a mode the account offers for it, as the card's buttons do
// (`quickReplyModes`); an issue's conversation offers only a public
// comment. Send takes the daemon's usual way, as the compose window does:
// draft.create on the target, one draft.save whose body is the typed text
// above the original draft.create quoted (`quickReplyBody`: what the window
// saves with "Include" checked), message.send. The text is the user's own,
// never mail: it becomes HTML here, paragraphs and line breaks with
// everything escaped (`quickReplyHTML`), no Markdown. Nothing is cut: a
// text over `API.Limits.maxDraftBodyBytes` is refused by the daemon's
// draft.save, and the client keeps it in the bar. The client holds the text
// typed per conversation in memory until it is sent, moved into a window or
// cleared (`QuickReplyController`); it is no daemon draft, so that the chat
// does not show it twice (as a draft bubble next to the bar). Go's (Item,
// bool) is an optional here.

import Foundation

extension Conversation {
    /// conversation.QuickReplyTarget: the message a quick reply answers: the
    /// newest message card (`Model.items` are oldest first) that is neither
    /// the user's own nor a sent card; when every message is the user's, the
    /// newest message card. Events and the row of older messages never are.
    /// nil when `m` has no message card.
    public static func quickReplyTarget(_ m: Model) -> Item? {
        var newest: Item?
        for it in m.items.reversed() where it.kind == .message {
            if !it.mine, !it.sent {
                return it
            }
            if newest == nil {
                newest = it
            }
        }
        return newest
    }

    /// conversation.QuickReplyMode: what the bar's mode button chose.
    public enum QuickReplyMode: Sendable, Hashable, CaseIterable {
        /// Answers the sender (on an issue: a public comment, which the view
        /// labels with `Jira.replyLabel`).
        case reply
        /// Answers everyone.
        case replyAll
        /// The menu's Forward…: it never sends from the bar but opens the
        /// compose window with a forward of the target.
        case forward

        /// QuickReplyMode.ComposeMode: the draft.create mode.
        public var composeMode: ComposeMode {
            switch self {
            case .reply: return .reply
            case .replyAll: return .replyAll
            case .forward: return .forward
            }
        }

        /// The compose window's kind for the mode (the GTK window's
        /// quick_reply.go `quickReplyKind`), for the host that opens it.
        public var composeKind: ComposeKind {
            switch self {
            case .reply: return .reply
            case .replyAll: return .replyAll
            case .forward: return .forward
            }
        }
    }

    /// conversation.QuickReplyModes: the modes the bar offers for `actions`,
    /// the target's card actions (`cardActions`), in the menu's order:
    /// Reply, Reply All, Forward, each when `actions` has it. An account
    /// that comments (`actions.comment`) offers Reply only: the comment;
    /// Reply All and Forward are the window's. Empty when `actions` offers
    /// none of them (the bar is hidden then).
    public static func quickReplyModes(_ actions: Capabilities.Actions) -> [QuickReplyMode] {
        if actions.comment {
            return [.reply]
        }
        var out: [QuickReplyMode] = []
        if actions.reply {
            out.append(.reply)
        }
        if actions.replyAll {
            out.append(.replyAll)
        }
        if actions.forward {
            out.append(.forward)
        }
        return out
    }

    /// conversation.QuickReplyHTML: the HTML of the user's own text typed in
    /// the bar: line ends normalised (CRLF and CR to LF), paragraphs split
    /// on one or more blank lines (a line of spaces only is blank), each
    /// paragraph a <p> with its lines joined by <br>, every line escaped (as
    /// html.EscapeString); blank lines before the first and after the last
    /// paragraph are dropped. "" for text with no line that is not blank. It
    /// never cuts: the daemon refuses a body over
    /// `API.Limits.maxDraftBodyBytes` in draft.save, and the client keeps
    /// the text. Lines are cut on scalars, not characters: "\r\n" is one
    /// Swift character.
    public static func quickReplyHTML(_ text: String) -> String {
        var out = ""
        var para: [String] = []
        func flush() {
            guard !para.isEmpty else { return }
            out += "<p>"
            for (i, line) in para.enumerated() {
                if i > 0 {
                    out += "<br>"
                }
                // A line holds no line end: `escapeText` is
                // html.EscapeString here.
                out += escapeText(line)
            }
            out += "</p>"
            para.removeAll(keepingCapacity: true)
        }
        func take(_ line: String.UnicodeScalarView) {
            if line.allSatisfy({ $0.properties.isWhitespace }) {
                flush()
            } else {
                para.append(String(line))
            }
        }
        var line = String.UnicodeScalarView()
        var afterCR = false
        for u in text.unicodeScalars {
            if afterCR, u == "\n" {
                afterCR = false
                continue
            }
            afterCR = false
            if u == "\r" || u == "\n" {
                take(line)
                line = String.UnicodeScalarView()
                afterCR = u == "\r"
                continue
            }
            line.append(u)
        }
        take(line)
        flush()
        return out
    }

    /// conversation.QuickReplyBlank: a text with nothing to send: spaces,
    /// control and format (Cf) characters only. Send is disabled then. The
    /// same rule as `Jira.sendProblem`'s, mirrored here exactly.
    public static func quickReplyBlank(_ text: String) -> Bool {
        text.unicodeScalars.allSatisfy { r in
            if r.properties.isWhitespace {
                return true
            }
            switch r.properties.generalCategory {
            case .control, .format:
                return true
            default:
                return false
            }
        }
    }

    /// conversation.QuickReplyBody: the body draft.save gets for a quick
    /// reply in `mode` with the user's own text `own` on `draft`
    /// (draft.create's result): byte for byte what the compose window saves
    /// with "Include" checked, the own HTML (`quickReplyHTML`), a line break
    /// and the original draft.create quoted (`ComposeBodyLayout.draftBody`);
    /// a comment draft (and a draft without a body) is the own HTML alone.
    public static func quickReplyBody(mode: ComposeMode, own: String, draft: Draft) -> String {
        ComposeBodyLayout(mode: mode, comment: draft.comment != nil, bodyHTML: ComposeBodyLayout.draftBody(draft))
            .html(own: quickReplyHTML(own), includeQuote: true)
    }

    /// conversation.QuickReplyPlaceholder: the placeholder and accessible
    /// name of the bar's field: `comment` says the reply is a comment on an
    /// issue.
    public static func quickReplyPlaceholder(comment: Bool) -> String {
        if comment {
            // TRANSLATORS: the placeholder of the quick-reply field under an issue's conversation shown as chat; what is typed becomes a comment.
            return L10n.T("Write a comment…")
        }
        // TRANSLATORS: the placeholder of the quick-reply field under a conversation shown as chat.
        return L10n.T("Write a reply…")
    }

    /// conversation.QuickReplyTargetTooltip: the tooltip of the bar's mode
    /// button, which names the target's sender (`Item.sender`, already
    /// cleaned for one line). "" for `.forward` and when the target has no
    /// sender: the button keeps only its own label then (Reply, Reply All).
    public static func quickReplyTargetTooltip(_ mode: QuickReplyMode, sender: String) -> String {
        if sender.isEmpty {
            return ""
        }
        switch mode {
        case .reply:
            // TRANSLATORS: the tooltip of the quick reply's mode button; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
            return L10n.T("Reply to: %s", sender)
        case .replyAll:
            // TRANSLATORS: the tooltip of the quick reply's mode button set to reply to all; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
            return L10n.T("Reply all to: %s", sender)
        case .forward:
            return ""
        }
    }

    /// conversation.QuickReplyOpenWindowLabel: the tooltip and accessible
    /// name of the bar's button that opens the compose window with what was
    /// typed.
    public static func quickReplyOpenWindowLabel() -> String {
        // TRANSLATORS: the tooltip of the quick-reply bar's button that moves the typed reply into the compose window.
        L10n.T("Open in a Window")
    }

    /// conversation.QuickReplyForwardLabel: the item of the mode button's
    /// menu that opens the compose window with a forward of the target (and
    /// the typed text above it).
    public static func quickReplyForwardLabel() -> String {
        // TRANSLATORS: an item of the quick-reply bar's mode menu; opens the compose window with the message forwarded.
        L10n.T("Forward…")
    }

    /// conversation.SingleModel: the model of one message for the
    /// quick-reply bar under the reading pane's page of a single message
    /// (docs/plans/chat-view.md §12, S2, S4): the bar answers that message,
    /// whoever wrote it (in Sent a reply to the user's own message), so
    /// `QuickReplyController.update` takes the same model shape as for a
    /// conversation and `quickReplyTarget` needs no rule of its own. The
    /// model's thread is the message's (the bar keeps the typed text per
    /// thread, shared with the message's conversation; "" without one); its
    /// one item is a `.message` with the cleaned sender (`Item.sender`,
    /// which names the target in the mode button's tooltip) and `mine` as
    /// the caller says (the rule of `Item.mine`: `isMine(_:account:)`). An
    /// event of an issue (a row without a body) is an `.event` without its
    /// sentences, so that the bar finds no target and hides; a message
    /// without an id makes an empty model. The model is for the bar only:
    /// no issue card, nothing to mark read, never rendered.
    public static func singleModel(_ s: MessageSummary, mine: Bool) -> Model {
        var m = Model(thread: s.threadId ?? "")
        if s.id.rawValue.isEmpty {
            return m
        }
        var it = Item(kind: .message, message: s)
        it.sender = sender(s.from)
        it.mine = mine
        it.unread = !hasFlag(s.flags, .seen)
        if Jira.isEvent(s.issue) {
            it.kind = .event
            it.unread = false
        }
        m.items = [it]
        m.scrollTo = 0
        return m
    }
}

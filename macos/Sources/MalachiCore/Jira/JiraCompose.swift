// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/compose.go: the comment mode of the compose window. A
// comment is written in the compose window for a draft with
// `Draft.comment` (draft.create reply on an account with the `comment`
// capability): the title names the issue, there are no recipients, subject
// or attachments and no Save Draft (a comment draft stays on this
// computer), the toolbar keeps only `commentFormats`, and a service-desk
// issue offers the choice between a reply to the customer and an internal
// note. Sending queues the comment like a message.

import Foundation

extension Jira {
    /// jira.Format: a formatting control of the compose window's toolbar.
    public struct Format: RawRepresentable, Hashable, Sendable, ExpressibleByStringLiteral,
        CustomStringConvertible {
        public let rawValue: String
        public init(rawValue: String) { self.rawValue = rawValue }
        public init(stringLiteral value: String) { self.rawValue = value }
        public var description: String { rawValue }

        public static let bold: Format = "bold"
        public static let italic: Format = "italic"
        public static let underline: Format = "underline"
        public static let code: Format = "code"
        /// The paragraph style menu.
        public static let heading: Format = "heading"
        public static let alignment: Format = "alignment"
        public static let bulletList: Format = "bulletList"
        public static let numberedList: Format = "numberedList"
        public static let quote: Format = "quote"
        public static let link: Format = "link"
        public static let colour: Format = "colour"
        public static let image: Format = "image"
        /// Removes formatting, so it stays within the others.
        public static let clear: Format = "clear"
    }

    /// jira.CommentFormats: the controls of the comment mode, in toolbar
    /// order: what both Jira Cloud (ADF) and Data Center (wiki markup) keep.
    public static let commentFormats: [Format] = [
        .bold, .italic, .code, .link,
        .bulletList, .numberedList, .quote, .clear,
    ]

    /// jira.CommentAllows: a control the comment mode keeps.
    public static func commentAllows(_ f: Format) -> Bool {
        commentFormats.contains(f)
    }

    /// jira.VisibilityOption: one choice of who reads a comment.
    public struct VisibilityOption: Sendable, Equatable {
        public var visibility: CommentVisibility
        public var label: String
    }

    /// jira.VisibilityOptions: the choices of a comment on `issue`: a reply
    /// to the customer and an internal note when the issue allows both (a
    /// service-desk request), none otherwise (the comment is public).
    public static func visibilityOptions(_ issue: IssueInfo) -> [VisibilityOption] {
        guard allowsBoth(issue) else { return [] }
        return [
            // TRANSLATORS: a comment on a service-desk request that the customer reads too.
            VisibilityOption(visibility: .public, label: L10n.T("Reply to Customer")),
            // TRANSLATORS: a comment on a service-desk request that only the team reads.
            VisibilityOption(visibility: .internal, label: L10n.T("Internal Note")),
        ]
    }

    /// jira.allowsBoth: an issue whose comments may be public or internal:
    /// its `commentVisibilities` are exactly those two.
    private static func allowsBoth(_ issue: IssueInfo) -> Bool {
        var isPublic = false
        var isInternal = false
        for v in issue.commentVisibilities {
            switch v {
            case .public:
                isPublic = true
            case .internal:
                isInternal = true
            default:
                return false
            }
        }
        return isPublic && isInternal
    }

    /// jira.SelectedVisibility: the visibility the comment mode shows as
    /// chosen: internal only when the draft asks for it and the issue
    /// allows it, public otherwise.
    public static func selectedVisibility(_ c: DraftComment) -> CommentVisibility {
        c.visibility == .internal && allowsBoth(c.issue) ? .internal : .public
    }

    /// jira.CommentTitle: the compose window's title for a comment on the
    /// issue with `key`.
    public static func commentTitle(_ key: String) -> String {
        // TRANSLATORS: title of the window that writes a comment; %s is an issue key such as "ITSD-42".
        L10n.T("Comment on %s", clean(key))
    }

    /// jira.CommentWindow: how the compose window presents a comment draft.
    public struct CommentWindow: Sendable, Equatable {
        public var title: String
        /// The choices shown (none: no choice, the comment is public) and
        /// the chosen one.
        public var visibilities: [VisibilityOption]
        public var visibility: CommentVisibility
        /// The toolbar controls kept, `commentFormats`.
        public var formats: [Format]
    }

    /// jira.CommentCompose: the comment mode of draft `d`; nil for a draft
    /// of an e-mail.
    public static func commentCompose(_ d: Draft) -> CommentWindow? {
        guard let c = d.comment else { return nil }
        return CommentWindow(
            title: commentTitle(c.issue.key),
            visibilities: visibilityOptions(c.issue),
            visibility: selectedVisibility(c),
            formats: commentFormats
        )
    }

    /// jira.SendProblem: why a comment with the editor's plain text cannot
    /// be sent; "" when it can. A comment of spaces and invisible
    /// characters only is empty.
    public static func sendProblem(_ text: String) -> String {
        let blank = text.unicodeScalars.allSatisfy { r in
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
        if blank {
            return L10n.T("Write a comment first")
        }
        return ""
    }

    /// jira.CommentQueued: the toast after a comment was sent to the outbox.
    public static func commentQueued() -> String {
        L10n.T("Comment queued")
    }

    /// jira.ReplyLabel: the label of the Reply action: "Comment" when
    /// replying to the selected message writes a comment (the `comment`
    /// capability).
    public static func replyLabel(comment: Bool) -> String {
        if comment {
            // TRANSLATORS: the Reply action of an issue: writes a comment.
            return L10n.T("Comment")
        }
        return L10n.T("Reply")
    }
}

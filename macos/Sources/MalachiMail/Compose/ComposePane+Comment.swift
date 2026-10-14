// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// The comment mode of a compose pane (ui/internal/jira compose.go,
// `Jira.commentCompose`): a pane opened for a comment draft
// (`ComposeParams.comment`, draft.create reply on an account that
// comments) writes a comment on an issue. The header fields give way to
// `CommentHeaderView` (the issue and, on a service-desk request, public or
// internal), the title names the issue, the formatting bar and the Format
// menu keep `Jira.commentFormats`, and nothing attaches: no Attach button,
// no Attach Files or Insert Image, files dropped on the editor are
// refused. There is no Save Draft either: no Drafts folder keeps a
// comment (the autosave is only against a crash, and the saved copy goes
// with the window, `ComposeDraftController`). The pane is pinned to the
// issue's account, which writes no mail and so is not in the From list.
// The window's part (its toolbar) is ComposeWindowController+Comment.swift.

extension ComposePane {
    /// The pane writes a comment (`ComposeForm.isComment`).
    var isComment: Bool { params.comment != nil }

    /// The visibility chosen in the header (`ComposeForm.commentVisibility`);
    /// public for an e-mail and for an issue without the choice.
    var commentVisibility: CommentVisibility {
        commentHeader?.visibility ?? .public
    }

    /// The issue's account a comment pane is pinned to, as the compose
    /// manager lists it, or one that carries its id until the list is
    /// there; nil for an e-mail. Only its id reaches the daemon.
    var commentAccount: Account? {
        guard isComment, let id = params.accountID else { return nil }
        if let known = composer.knownAccounts.first(where: { $0.id == id }) {
            return known
        }
        return Account(
            id: id, config: AccountConfig(name: "", email: "", kind: .jira), enabled: true,
            state: SyncState(accountId: id, status: .idle), capabilities: [.comment]
        )
    }

    /// Sets the pane up for a comment; nothing for an e-mail. Called
    /// once from `init`, after the editor's callbacks are wired.
    func applyCommentMode() {
        guard isComment else { return }
        // A dropped file is refused (the view shows no copy cursor), not
        // imported.
        editor.onDropFiles = nil
        formatToolbar.restrict(to: Jira.commentFormats)
        commentHeader?.onVisibilityChanged = { [weak self] in
            self?.draft.markDirty()
        }
        // Inline: the footer has no Attach.
        attachButton?.isHidden = true
    }

    /// Whether the compose or Format action `action` works in comment
    /// mode: no Save Draft, no attachments, and of the formats only
    /// `Jira.commentFormats`. Actions that are not about formats or
    /// attachments (Send, Discard) are left alone.
    static func commentAllows(_ action: Selector) -> Bool {
        switch action {
        case Action.saveDraft, Action.attachFiles:
            return false
        case Action.insertImage:
            return Jira.commentAllows(.image)
        case Action.formatBold:
            return Jira.commentAllows(.bold)
        case Action.formatItalic:
            return Jira.commentAllows(.italic)
        case Action.formatUnderline:
            return Jira.commentAllows(.underline)
        case Action.formatParagraph, Action.formatHeading1, Action.formatHeading2, Action.formatHeading3:
            return Jira.commentAllows(.heading)
        case Action.alignLeft, Action.alignCenter, Action.alignRight:
            return Jira.commentAllows(.alignment)
        case Action.bulletedList:
            return Jira.commentAllows(.bulletList)
        case Action.numberedList:
            return Jira.commentAllows(.numberedList)
        case Action.quoteBlock:
            return Jira.commentAllows(.quote)
        case Action.insertLink:
            return Jira.commentAllows(.link)
        case Action.clearFormatting:
            return Jira.commentAllows(.clear)
        default:
            return true
        }
    }
}

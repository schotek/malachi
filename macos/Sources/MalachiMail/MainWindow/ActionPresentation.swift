// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// How the per-message actions show for the account's capabilities
/// (`ActionFlags.unsupported` and `comment`, from `Capabilities.supported`):
/// an action the account does not offer at all leaves the toolbar (macOS 15
/// and later hide the item; macOS 14 has no hidden toolbar items, the
/// validator's `allows` disables it there) and stays in the menus,
/// disabled; Reply is labelled "Comment" (`Jira.replyLabel`) on an account
/// that comments on issues instead. The validators of the main window and
/// of a message window call it once per validated item; whether an action
/// is enabled stays their `allows`.
@MainActor
enum ActionPresentation {
    /// The capability-bound action a selector stands for, nil for the
    /// others (flags, images, the window's own actions).
    static func kind(_ action: Selector) -> MessageActionKind? {
        switch action {
        case Action.reply: return .reply
        case Action.replyAll: return .replyAll
        case Action.forward: return .forward
        case Action.moveToTrash: return .trash
        case Action.archive: return .archive
        case Action.markAsJunk: return .junk
        default: return nil
        }
    }

    /// A menu item of the menu bar or the More Actions menu: Reply is
    /// retitled for comments; nothing is hidden.
    static func present(_ item: NSMenuItem, _ action: Selector, _ f: ActionFlags) {
        guard action == Action.reply else { return }
        let title = Jira.replyLabel(comment: f.comment)
        if item.title != title {
            item.title = title
        }
    }

    /// A toolbar item: every message action of its toolbar is hidden or
    /// shown again for `f` and Reply is relabelled. The whole toolbar goes
    /// at once because a hidden item is not validated, so it comes back
    /// when any item of its toolbar is.
    static func present(_ item: NSToolbarItem, _ f: ActionFlags) {
        for it in item.toolbar?.items ?? [item] {
            apply(it, f)
        }
    }

    private static func apply(_ it: NSToolbarItem, _ f: ActionFlags) {
        guard let action = it.action, let kind = kind(action) else { return }
        if #available(macOS 15, *) {
            let hidden = f.unsupported.contains(kind)
            if it.isHidden != hidden {
                it.isHidden = hidden
            }
        }
        guard kind == .reply else { return }
        let label = Jira.replyLabel(comment: f.comment)
        guard it.label != label else { return }
        it.label = label
        it.paletteLabel = label
        it.toolTip = label
        it.image = f.comment ? Icon.symbol("text.bubble", size: .toolbar) : Icon.reply
    }
}

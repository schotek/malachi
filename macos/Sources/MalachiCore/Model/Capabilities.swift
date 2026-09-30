// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/capabilities/capabilities.go: which message actions the
// client offers for an account (`Account.capabilities`, docs/api.md): a
// mail account (imap, graph) can do everything, an issue-tracker account
// (jira) only what its capabilities list, for example comment instead of
// reply, and forward into a mail account. Seen and flagged work on every
// account and are not decided here. Which accounts write mail at all (the
// From list, New Message) is `composeAccounts` and `canComposeNew`.
//
// The rules extend those of the message toolbar (actions.go
// setMessageActionsSensitive, ActionRules.swift here): archive and junk
// need the account's role folder, and a queued message in the outbox keeps
// reply, forward and trash (which cancels the send). No texts.

import Foundation

/// The capabilities package: a namespace, so the Go names map 1:1
/// (`capabilities.Supported` → `Capabilities.supported`).
public enum Capabilities {
    /// capabilities.Can: whether account `a` has capability `c`
    /// (`Account.can`): nil capabilities (a daemon from before
    /// capabilities) mean `API.mailCapabilities`, an empty list none.
    public static func can(_ a: Account, _ c: Capability) -> Bool {
        a.can(c)
    }

    /// `can` for the account of a situation; no account (no folder
    /// listed, Go's zero Account) has the mail default.
    private static func has(_ a: Account?, _ c: Capability) -> Bool {
        a?.can(c) ?? API.mailCapabilities.contains(c)
    }

    /// capabilities.Situation: what the rules look at.
    public struct Situation: Sendable, Equatable {
        /// The selected row's account; with nothing selected, the account
        /// of the listed folder. nil (no folder listed) has the mail
        /// default.
        public var account: Account?
        /// A row is selected and the actions are on at all.
        public var selected: Bool
        /// The row is a queued message in the account's outbox (with
        /// nothing selected: the listed folder is the outbox).
        public var outbox: Bool
        /// The account has such a role folder and the row is not in it
        /// already (`canMoveToRole`).
        public var archive: Bool
        public var junk: Bool
        /// Some enabled account can compose (`forwardAccounts` is not
        /// empty): the forward of an issue goes out from a mail account.
        public var composeAccount: Bool

        public init(
            account: Account? = nil, selected: Bool = false, outbox: Bool = false, archive: Bool = false,
            junk: Bool = false, composeAccount: Bool = false
        ) {
            self.account = account
            self.selected = selected
            self.outbox = outbox
            self.archive = archive
            self.junk = junk
            self.composeAccount = composeAccount
        }
    }

    /// capabilities.Actions: the message actions, each true when offered.
    public struct Actions: Sendable, Equatable {
        public var reply = false
        public var replyAll = false
        public var forward = false
        public var move = false
        public var trash = false
        public var archive = false
        public var junk = false
        /// Reply writes a comment on the issue: the client labels it
        /// "Comment" (`Jira.replyLabel`).
        public var comment = false
    }

    /// capabilities.Supported: the actions the account offers at all,
    /// whatever is selected: a client hides the others (or disables them
    /// where it cannot hide), and labels Reply by `comment`. Reply needs
    /// reply or comment, Forward needs forward and an account to send from
    /// (the account itself, or another enabled one that composes), Trash
    /// needs delete except in the outbox (cancelling a queued message),
    /// Move, Archive and Junk need move.
    public static func supported(_ s: Situation) -> Actions {
        let a = s.account
        let move = has(a, .move)
        return Actions(
            reply: has(a, .reply) || has(a, .comment),
            replyAll: has(a, .replyAll),
            forward: has(a, .forward) && (has(a, .compose) || s.composeAccount),
            move: move,
            trash: has(a, .delete) || s.outbox,
            archive: move,
            junk: move,
            comment: has(a, .comment)
        )
    }

    /// capabilities.Available: the actions enabled now: the supported ones
    /// while a row is selected, archive and junk only with the role
    /// folder, and for a queued message only reply, reply all, forward and
    /// trash (the daemon refuses moves in the outbox). `comment` is
    /// `supported`'s, selected or not.
    public static func available(_ s: Situation) -> Actions {
        let sup = supported(s)
        let on = s.selected
        return Actions(
            reply: on && sup.reply,
            replyAll: on && sup.replyAll,
            forward: on && sup.forward,
            move: on && sup.move && !s.outbox,
            trash: on && sup.trash,
            archive: on && sup.archive && !s.outbox && s.archive,
            junk: on && sup.junk && !s.outbox && s.junk,
            comment: sup.comment
        )
    }

    /// capabilities.ForwardAccounts: the accounts a message can be
    /// forwarded from: the enabled ones that compose, in the given order.
    public static func forwardAccounts(_ accounts: [Account]) -> [Account] {
        accounts.filter { $0.enabled && can($0, .compose) }
    }

    /// capabilities.ForwardFrom: the account a message of account `from`
    /// is forwarded from: `from` itself when it is enabled and composes,
    /// else the first of `forwardAccounts`; nil when there is none. When
    /// the result is not `from`, draft.create names `from` as
    /// `DraftCreateParams.messageAccountId`.
    public static func forwardFrom(_ accounts: [Account], from: AccountID) -> Account? {
        let list = forwardAccounts(accounts)
        return list.first { $0.id == from } ?? list.first
    }

    /// capabilities.ComposeAccounts: the accounts a message is written
    /// from (the compose window's From list): those that compose, in the
    /// given order, paused ones included as before. An account that only
    /// comments (an issue tracker) writes in the comment mode of the
    /// window instead, pinned to itself.
    public static func composeAccounts(_ accounts: [Account]) -> [Account] {
        accounts.filter { can($0, .compose) }
    }

    /// capabilities.CanComposeNew: whether New Message is offered: while
    /// no account is known (the compose window writes from a placeholder
    /// identity until the list arrives) or when some account composes;
    /// never for issue-tracker accounts alone.
    public static func canComposeNew(_ accounts: [Account]) -> Bool {
        accounts.isEmpty || !composeAccounts(accounts).isEmpty
    }
}

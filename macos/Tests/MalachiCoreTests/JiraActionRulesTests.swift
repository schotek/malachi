// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The per-message actions of a Jira account (ActionRules.swift
// `messageActionState` over `Capabilities`): what the account does not
// offer is off and `unsupported`, Reply of a commenting account is
// `comment`, Forward needs an account to send from, and with nothing
// selected the listed folder's account decides.

private func account(
    _ id: AccountID, enabled: Bool = true, kind: AccountKind? = nil, capabilities: [Capability]?
) -> Account {
    Account(
        id: id, config: AccountConfig(name: "", email: "jana@acme.example", kind: kind), enabled: enabled,
        state: SyncState(accountId: id, status: .idle), capabilities: capabilities
    )
}

/// A mail account of a daemon from before capabilities.
private let oldMail = account("a", capabilities: nil)
private let mail = account("m", capabilities: API.mailCapabilities)
/// Reads and flags only (M1).
private let jiraM1 = account("j", kind: .jira, capabilities: [])
/// Comments and forwards into a mail account (M2).
private let jiraM2 = account("j", kind: .jira, capabilities: [.comment, .forward])

private let everythingJiraLacks: Set<MessageActionKind> = [.reply, .replyAll, .forward, .trash, .move, .archive, .junk]

/// A model listing `folder` of `acc` with one message `1` in it; the
/// account's folders include Archive, Junk and Outbox so that only the
/// capabilities can switch those off.
private func model(_ accounts: [Account], listing acc: AccountID, flags: [Flag] = [.seen]) -> MailModel {
    var folders: [AccountID: [Folder]] = [:]
    for a in accounts {
        folders[a.id] = [
            testFolder("in", path: "INBOX", role: .inbox),
            testFolder("arch", path: "Archive", role: .archive),
            testFolder("junk", path: "Junk", role: .junk),
            testFolder("out", path: "Outbox", role: .outbox, total: 1),
        ]
    }
    var m = MailModel(accounts: accounts, folders: folders)
    m.listFolder = FolderKey(account: acc, folder: "in")
    var s = summary("1", flags: flags)
    s.accountId = acc
    s.folderId = "in"
    m.setMessages([s], page: PageInfo(total: 1))
    return m
}

struct JiraActionRulesTests {
    @Test func mailAccountsOfferEverything() throws {
        for acc in [oldMail, mail] {
            let m = model([acc], listing: acc.id)
            let st = messageActionState(m.rowAt(0), model: m)
            #expect(st.reply && st.replyAll && st.forward && st.trash && st.archive && st.junk, "\(acc.id)")
            #expect(st.unsupported.isEmpty, "\(acc.id)")
            #expect(!st.comment, "\(acc.id)")
            #expect(messageActionState(nil, model: m) == .none, "nothing selected in a mail folder")
        }
    }

    @Test func jiraWithoutCapabilities() throws {
        let m = model([mail, jiraM1], listing: "j", flags: [])
        let st = messageActionState(m.rowAt(0), model: m)
        #expect(st.on)
        #expect(!st.reply && !st.replyAll && !st.forward && !st.trash && !st.archive && !st.junk)
        #expect(st.unsupported == everythingJiraLacks)
        #expect(!st.comment)
        // Flags are local and always allowed.
        #expect(st.star && st.toggleFlag && st.markRead && !st.markUnread)
        #expect(st.loadImages && st.trustSender)
    }

    @Test func jiraThatComments() throws {
        let m = model([mail, jiraM2], listing: "j")
        let st = messageActionState(m.rowAt(0), model: m)
        #expect(st.reply && st.comment, "Reply writes a comment")
        #expect(st.forward, "forwarded from the mail account")
        #expect(!st.replyAll && !st.trash && !st.archive && !st.junk)
        #expect(st.unsupported == [.replyAll, .trash, .move, .archive, .junk])
        #expect(st.markUnread && !st.markRead)
    }

    @Test func forwardNeedsAnAccountThatComposes() throws {
        // Only the Jira account: nothing to forward from.
        var m = model([jiraM2], listing: "j")
        var st = messageActionState(m.rowAt(0), model: m)
        #expect(!st.forward)
        #expect(st.unsupported.contains(.forward))
        #expect(st.reply && st.comment)

        // A paused mail account does not count.
        var paused = mail
        paused.enabled = false
        m = model([paused, jiraM2], listing: "j")
        st = messageActionState(m.rowAt(0), model: m)
        #expect(!st.forward && st.unsupported.contains(.forward))

        // An enabled one does, a mail account of an old daemon too.
        m = model([oldMail, jiraM2], listing: "j")
        st = messageActionState(m.rowAt(0), model: m)
        #expect(st.forward && !st.unsupported.contains(.forward))
    }

    @Test func nothingSelectedFollowsTheListedFolder() throws {
        var m = model([mail, jiraM1], listing: "j")
        var st = messageActionState(nil, model: m)
        #expect(!st.on && !st.reply && !st.trash)
        #expect(st.unsupported == everythingJiraLacks, "the toolbar of a Jira folder has no Reply")
        #expect(messageActionState(m.rowAt(0), on: false, model: m).unsupported == everythingJiraLacks)

        m = model([mail, jiraM2], listing: "j")
        st = messageActionState(nil, model: m)
        #expect(st.comment, "Reply is labelled Comment before a row is chosen")
        #expect(st.unsupported == [.replyAll, .trash, .move, .archive, .junk])

        // The Jira account's outbox: Trash cancels a queued comment.
        m.listFolder = FolderKey(account: "j", folder: "out")
        st = messageActionState(nil, model: m)
        #expect(!st.unsupported.contains(.trash))

        // A mail folder, or none (a search over every account).
        m.listFolder = FolderKey(account: "m", folder: "in")
        #expect(messageActionState(nil, model: m) == .none)
        m.listFolder = nil
        #expect(messageActionState(nil, model: m) == .none)
    }

    @Test func theRowsAccountDecidesInASearch() throws {
        // A search result of the Jira account while nothing is listed.
        var m = model([mail, jiraM1], listing: "j")
        m.listFolder = nil
        let st = messageActionState(m.rowAt(0), model: m)
        #expect(st.unsupported == everythingJiraLacks)

        // An account the window does not know (a message window outliving
        // it) has the mail default.
        var s = summary("2", flags: [.seen])
        s.accountId = "gone"
        s.folderId = "in"
        let gone = messageActionState(ListRow(key: ListKey(message: "2"), message: s), model: m)
        #expect(gone.reply && gone.trash && gone.unsupported.isEmpty)
    }

    @Test func aQueuedJiraCommentCanBeCancelled() throws {
        var m = model([mail, jiraM2], listing: "j")
        m.messages[0].folderId = "out"
        m.messages[0].outbox = OutboxInfo(state: .queued, attempts: 0)
        let st = messageActionState(m.rowAt(0), model: m)
        #expect(st.outbox && st.trash, "Trash cancels the send")
        #expect(!st.unsupported.contains(.trash))
        #expect(!st.star && !st.archive && !st.junk)
    }
}

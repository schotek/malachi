// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/capabilities/capabilities_test.go. Go's zero Account (no
// folder listed) is a nil account here; Go's (Account, bool) is an
// Optional.

private func account(
    _ id: AccountID, enabled: Bool = true, kind: AccountKind? = nil, capabilities: [Capability]?
) -> Account {
    Account(
        id: id, config: AccountConfig(name: "", email: "jana@acme.example", kind: kind), enabled: enabled,
        state: SyncState(accountId: id, status: .idle), capabilities: capabilities)
}

// Accounts as the daemon sends them.
/// A mail account of a daemon from before capabilities.
private let oldMail = account("a1", capabilities: nil)
private let mail = account("a2", capabilities: API.mailCapabilities)
/// Reads and flags only.
private let jiraM1 = account("j1", kind: .jira, capabilities: [])
/// Comments and forwards into a mail account.
private let jiraM2 = account("j2", kind: .jira, capabilities: [.comment, .forward])

private typealias Actions = Capabilities.Actions
private typealias Situation = Capabilities.Situation

struct CapabilitiesTests {
    @Test func can() {
        let all: [Capability] = [.compose, .reply, .replyAll, .forward, .comment, .move, .delete, "unknown", "transition"]
        let mailSet: Set<Capability> = [.compose, .reply, .replyAll, .forward, .move, .delete]
        let cases: [(String, Account, Set<Capability>)] = [
            ("nil means mail", oldMail, mailSet),
            ("mail", mail, mailSet),
            ("empty means none", jiraM1, []),
            ("jira comments and forwards", jiraM2, [.comment, .forward]),
            ("unknown values", account("x", capabilities: ["transition"]), ["transition"]),
        ]
        for (name, acc, want) in cases {
            for c in all {
                #expect(Capabilities.can(acc, c) == want.contains(c), "\(name): can(\(c))")
            }
        }
    }

    @Test func supported() {
        let everything = Actions(reply: true, replyAll: true, forward: true, move: true, trash: true, archive: true, junk: true)
        let cases: [(String, Situation, Actions)] = [
            ("no folder listed", Situation(), everything),
            ("old daemon", Situation(account: oldMail), everything),
            ("mail", Situation(account: mail), everything),
            ("mail without another compose account", Situation(account: mail, composeAccount: false), everything),
            ("jira M1", Situation(account: jiraM1, composeAccount: true), Actions()),
            ("jira M1 outbox", Situation(account: jiraM1, outbox: true), Actions(trash: true)),
            ("jira M2", Situation(account: jiraM2, composeAccount: true), Actions(reply: true, forward: true, comment: true)),
            ("jira M2 without a mail account", Situation(account: jiraM2), Actions(reply: true, comment: true)),
            ("reply only", Situation(account: account("x", capabilities: [.reply])), Actions(reply: true)),
            ("move without delete", Situation(account: account("x", capabilities: [.move])), Actions(move: true, archive: true, junk: true)),
            ("forward with compose of its own", Situation(account: account("x", capabilities: [.forward, .compose])), Actions(forward: true)),
        ]
        for (name, s, want) in cases {
            #expect(Capabilities.supported(s) == want, "\(name)")
        }
    }

    @Test func available() {
        let cases: [(String, Situation, Actions)] = [
            ("nothing selected", Situation(account: mail, archive: true, junk: true, composeAccount: true), Actions()),
            ("nothing selected in jira keeps the label", Situation(account: jiraM2, composeAccount: true), Actions(comment: true)),
            ("mail message", Situation(account: mail, selected: true, archive: true, junk: true, composeAccount: true),
             Actions(reply: true, replyAll: true, forward: true, move: true, trash: true, archive: true, junk: true)),
            ("mail without archive and junk folders", Situation(account: mail, selected: true),
             Actions(reply: true, replyAll: true, forward: true, move: true, trash: true)),
            ("old daemon", Situation(account: oldMail, selected: true, archive: true),
             Actions(reply: true, replyAll: true, forward: true, move: true, trash: true, archive: true)),
            ("queued message", Situation(account: mail, selected: true, outbox: true, archive: true, junk: true),
             Actions(reply: true, replyAll: true, forward: true, trash: true)),
            ("jira M1", Situation(account: jiraM1, selected: true, archive: true, junk: true, composeAccount: true), Actions()),
            ("jira M1 queued comment", Situation(account: jiraM1, selected: true, outbox: true), Actions(trash: true)),
            ("jira M2", Situation(account: jiraM2, selected: true, composeAccount: true), Actions(reply: true, forward: true, comment: true)),
            ("jira M2 without a mail account", Situation(account: jiraM2, selected: true), Actions(reply: true, comment: true)),
        ]
        for (name, s, want) in cases {
            #expect(Capabilities.available(s) == want, "\(name)")
        }
    }

    @Test func forwardAccounts() {
        let paused = account("p", enabled: false, capabilities: nil)
        let accounts = [jiraM2, paused, mail, jiraM1, oldMail]
        #expect(Capabilities.forwardAccounts(accounts).map(\.id) == [mail.id, oldMail.id])
        #expect(Capabilities.forwardAccounts([jiraM1, paused]).isEmpty, "no forward account without mail")

        let cases: [(AccountID, AccountID)] = [
            ("a1", "a1"), // a mail message goes out from its own account
            ("a2", "a2"),
            ("j2", "a2"), // an issue from the first mail account
            ("p", "a2"), // a paused account's message too
            ("missing", "a2"),
        ]
        for (from, want) in cases {
            #expect(Capabilities.forwardFrom(accounts, from: from)?.id == want, "forwardFrom(\(from))")
        }
        #expect(Capabilities.forwardFrom([jiraM2, paused], from: "j2") == nil, "forwardFrom without mail")
    }

    @Test func composeAccounts() {
        let paused = account("p", enabled: false, capabilities: nil)
        let accounts = [jiraM2, paused, mail, jiraM1, oldMail]
        #expect(Capabilities.composeAccounts(accounts).map(\.id) == [paused.id, mail.id, oldMail.id])
        #expect(Capabilities.composeAccounts([jiraM1, jiraM2]).isEmpty, "composeAccounts of issue trackers")

        let cases: [(String, [Account], Bool)] = [
            ("nothing known yet", [], true),
            ("mail", [mail], true),
            ("old daemon", [oldMail], true),
            ("a paused mail account", [jiraM2, paused], true),
            ("issue trackers alone", [jiraM2, jiraM1], false),
            ("reply only", [account("r", capabilities: [.reply])], false),
        ]
        for (name, list, want) in cases {
            #expect(Capabilities.canComposeNew(list) == want, "\(name)")
        }
    }
}

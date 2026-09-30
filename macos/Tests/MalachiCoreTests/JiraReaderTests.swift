// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// What the reading pane shows of a Jira message (IssueReading.swift): the
// card, the issue's summary as the subject, whether the key opens the
// issue, and an event's changes in place of the body.

private let site = "https://acme.atlassian.net"

private func info(_ key: String = "ITSD-42", summary: String = "VPN drops every 10 minutes", url: String? = nil) -> IssueInfo {
    IssueInfo(
        key: key, url: url ?? site + "/browse/" + key, summary: summary, status: "In Progress",
        statusCategory: .inProgress, priority: "High", assignee: "Jana Dvořáková"
    )
}

private func message(_ issue: MessageIssue?, subject: String = "ITSD-42: VPN drops every 10 minutes") -> MessageSummary {
    MessageSummary(
        id: "m1", accountId: "j", folderId: "f", from: [], subject: subject, date: .goZero, snippet: "",
        flags: [.seen], hasAttachments: false, size: 0, issue: issue
    )
}

struct JiraReaderTests {
    @Test func mailHasNone() {
        #expect(issueReading(message(nil), nil, site: site) == nil)
        #expect(!readsWithoutBody(message(nil)))
    }

    @Test func aComment() throws {
        let item = MessageIssue(info: info(), item: .comment, visibility: .internal, via: "Issue Sync", edited: true)
        let r = try #require(issueReading(message(item), nil, site: site))
        #expect(r.subject == "VPN drops every 10 minutes", "the summary, the key is on the card")
        #expect(r.card.key == "ITSD-42")
        #expect(r.card.status == "In Progress")
        #expect(r.card.statusStyle == .inProgress)
        #expect(r.card.internal && r.card.internalLabel == "Internal")
        #expect(r.card.via == "via Issue Sync")
        #expect(r.card.edited == "Edited")
        #expect(r.card.rows.map(\.label) == ["Assignee", "Priority", "Type", "Reporter"])
        #expect(r.card.rows.map(\.value) == ["Jana Dvořáková", "High", "None", "None"])
        #expect(r.openable)
        #expect(r.eventBody == nil, "a comment's body comes from message.body")
        #expect(!readsWithoutBody(message(item)))
    }

    @Test func theFullMessageWins() throws {
        let old = MessageIssue(info: info(summary: "Old summary"), item: .description)
        let fresh = MessageIssue(info: info(summary: "New summary"), item: .description)
        let full = Message(summary: message(fresh, subject: "ITSD-42: New summary"))
        let r = try #require(issueReading(message(old), full, site: site))
        #expect(r.subject == "New summary")
        // The issue only in the full message still counts.
        #expect(issueReading(message(nil), full, site: site)?.subject == "New summary")
    }

    @Test func anEmptySummaryFallsBackToTheSubject() throws {
        let item = MessageIssue(info: info(summary: " " + jiraRLO + " "), item: .description)
        let r = try #require(issueReading(message(item, subject: "ITSD-42"), nil, site: site))
        #expect(r.card.summary.isEmpty)
        #expect(r.subject == "ITSD-42")
    }

    @Test func theKeyOpensOnlyTheAccountsSite() throws {
        let cases: [(String, String, Bool)] = [
            ("own site", site + "/browse/ITSD-42", true),
            ("another site", "https://evil.example/browse/ITSD-42", false),
            ("user info", "https://acme.atlassian.net@evil.example/browse/ITSD-42", false),
            ("http on an https site", "http://acme.atlassian.net/browse/ITSD-42", false),
            ("javascript", "javascript:alert(1)", false),
            ("empty", "", false),
        ]
        for (name, url, want) in cases {
            let item = MessageIssue(info: info(url: url), item: .comment)
            let r = try #require(issueReading(message(item), nil, site: site))
            #expect(r.openable == want, "\(name)")
        }
        // No site known (the account is gone): nothing opens.
        let item = MessageIssue(info: info(), item: .comment)
        #expect(issueReading(message(item), nil, site: "")?.openable == false)
    }

    @Test func anEventIsItsChanges() throws {
        let item = MessageIssue(info: info(), item: .event, changes: [
            IssueChange(field: .status, from: "To Do", to: "In Progress"),
            IssueChange(field: "resolution", from: "", to: "Fixed"),
            IssueChange(field: .assignee, from: "Jana Dvořáková", to: ""),
        ])
        let s = message(item)
        #expect(readsWithoutBody(s))
        let r = try #require(issueReading(s, nil, site: site))
        #expect(r.eventBody == "Status: To Do → In Progress\nAssignee: Jana Dvořáková → Unassigned")
        // An event of changes this client does not know: an empty body.
        let unknown = MessageIssue(info: info(), item: .event, changes: [IssueChange(field: "labels", to: "x")])
        #expect(issueReading(message(unknown), nil, site: site)?.eventBody == "")
    }
}

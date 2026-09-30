// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// What the reading pane shows of a message of a Jira account, above and in
// place of the body: the issue card (`Jira.issueCard`), the issue's summary
// as the subject, whether the card's key may open the issue, and for an
// event (a status or assignee change) the changes as the body, which then
// is never fetched. Swift-first: the GTK reading pane follows with the Jira
// widgets (message_view.go).

/// The Jira part of a message on display.
public struct IssueReading: Sendable, Equatable {
    public var card: Jira.Card
    /// The header's subject: the issue's summary, or the message's subject
    /// ("KEY: Summary") when the site sent none.
    public var subject: String
    /// The card's key opens `card.url`: a link to an issue of the account's
    /// own site (`Jira.isIssueURL`).
    public var openable: Bool
    /// An event: its changes, one sentence a line (`Jira.eventLines`), shown
    /// as the body. nil for the description or a comment, whose body comes
    /// from message.body like mail.
    public var eventBody: String?

    public init(card: Jira.Card, subject: String, openable: Bool, eventBody: String? = nil) {
        self.card = card
        self.subject = subject
        self.openable = openable
        self.eventBody = eventBody
    }
}

/// The Jira part of message `s` (the full message `m` once message.get
/// answered, which wins), with `site` the account's `JiraConfig.siteUrl`
/// ("" when unknown: the key opens nothing). nil for a mail message.
public func issueReading(_ s: MessageSummary, _ m: Message?, site: String) -> IssueReading? {
    guard let issue = m?.summary.issue ?? s.issue else { return nil }
    let card = Jira.issueCard(issue.info, item: issue)
    var subject = card.summary
    if subject.isEmpty {
        subject = subjectText(m?.summary.subject ?? s.subject)
    }
    return IssueReading(
        card: card, subject: subject,
        openable: !card.url.isEmpty && Jira.isIssueURL(card.url, siteURL: site),
        eventBody: Jira.isEvent(issue) ? Jira.eventLines(issue.changes).joined(separator: "\n") : nil
    )
}

/// Whether message `s` is shown without a body fetch: an event of an issue
/// carries all it says in its summary.
public func readsWithoutBody(_ s: MessageSummary) -> Bool {
    Jira.isEvent(s.issue)
}

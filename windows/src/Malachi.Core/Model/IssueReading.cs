// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/IssueReading.swift; GTK:
// ui/internal/window/issue_reading.go (issueReading, readIssue,
// readsWithoutBody).
//
// What the reading pane shows of a message of a Jira account, above and in
// place of the body: the issue card (Jira.IssueCard), the issue's summary
// as the subject, whether the card's key may open the issue, and for an
// event (a status or assignee change) the changes as the body, which then
// is never fetched. Swift's free functions issueReading and
// readsWithoutBody are the static Read and ReadsWithoutBody.

using System;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>The Jira part of a message on display (issueReading).</summary>
public sealed record IssueReading
{
    /// <summary>The issue card.</summary>
    public required JiraCard Card { get; init; }

    /// <summary>The header's subject: the issue's summary, or the message's subject ("KEY: Summary") when the site sent none.</summary>
    public required string Subject { get; init; }

    /// <summary>The card's key opens the card's URL: a link to an issue of the account's own site (Jira.IsIssueUrl).</summary>
    public bool Openable { get; init; }

    /// <summary>
    /// An event: its changes, one sentence a line (Jira.EventLines), shown as
    /// the body. Null for the description or a comment, whose body comes from
    /// message.body like mail.
    /// </summary>
    public string? EventBody { get; init; }

    /// <summary>
    /// readIssue: the Jira part of message <paramref name="s"/> (the full
    /// message <paramref name="m"/> once message.get answered, which wins),
    /// with <paramref name="site"/> the account's JiraConfig.SiteUrl (""
    /// when unknown: the key opens nothing). Null for a mail message.
    /// </summary>
    public static IssueReading? Read(MessageSummary s, Message? m, string site)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(site);
        if ((m?.Summary.Issue ?? s.Issue) is not { } issue)
        {
            return null;
        }
        var card = Jira.IssueCard(issue.Info, issue);
        var subject = card.Summary.Length > 0 ? card.Summary : LoadedMessageText.SubjectText(m?.Summary.Subject ?? s.Subject);
        return new IssueReading
        {
            Card = card,
            Subject = subject,
            Openable = card.Url.Length > 0 && Jira.IsIssueUrl(card.Url, site),
            EventBody = Jira.IsEvent(issue) ? string.Join('\n', Jira.EventLines(issue.Changes)) : null,
        };
    }

    /// <summary>
    /// readsWithoutBody: whether message <paramref name="s"/> is shown without
    /// a body fetch: an event of an issue carries all it says in its summary.
    /// </summary>
    public static bool ReadsWithoutBody(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return Jira.IsEvent(s.Issue);
    }
}

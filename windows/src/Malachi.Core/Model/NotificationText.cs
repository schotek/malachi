// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/NotificationText.swift
// (notificationBodyMax, notificationText, issueNotificationLine); GTK:
// ui/internal/window/notify.go (notificationBodyMax, notificationText,
// issueNotificationLine). As on macOS the title is capped
// like the body, where GTK caps only the body (docs/windows-port.md §3.1,
// U7): a sender's display name is hostile input too.
//
// Swift's free function notificationText is NotificationText.Of: a member
// cannot share its class's name.

using System;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>The plain-text title and body of a new-message notification.</summary>
public static class NotificationText
{
    /// <summary>
    /// Caps the notification body in UTF-8 bytes; subjects are hostile input
    /// and the notification centre does not need a novel (notify.go
    /// <c>notificationBodyMax</c>). The title (a sender's display name,
    /// hostile too) is capped the same way.
    /// </summary>
    public const int NotificationBodyMax = 200;

    /// <summary>
    /// The title and body of a new-message notification (notify.go
    /// <c>notificationText</c>): the sender's display name or "New message",
    /// the subject or "(No subject)". A message of an issue (a description or
    /// a comment of a Jira account) says which issue under its author:
    /// "KEY: summary" from <see cref="MessageSummary.Issue"/>
    /// (<see cref="IssueNotificationLine"/>), not from the subject.
    /// Notifications are not markup, but the text is still
    /// attacker-controlled: both lines are trimmed and capped.
    /// Windows-only: both are cleaned first (<see cref="DisplayText.CleanTrimmed"/>),
    /// as everywhere else the app shows them: a control character would make
    /// the toast's XML invalid and Windows would drop the notification, and
    /// a bidi override would turn the subject around.
    /// </summary>
    public static (string Title, string Body) Of(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        var title = L10n.T("New message");
        if (n.Message.From.Count > 0)
        {
            var name = Capped(Format.DisplayName(n.Message.From[0]));
            if (name.Length > 0)
            {
                title = name;
            }
        }
        var body = n.Message.Issue is { } issue ? IssueNotificationLine(issue) : "";
        if (body.Length == 0)
        {
            body = DisplayText.CleanTrimmed(n.Message.Subject);
        }
        if (body.Length == 0)
        {
            body = L10n.T("(No subject)");
        }
        return (title, Capped(body));
    }

    /// <summary>
    /// The line of a notification that names an issue (notify.go
    /// <c>issueNotificationLine</c>): its key and summary, "ITSD-42: The
    /// printer is on fire", or whichever of the two it has; "" when it has
    /// neither. Both are the site's texts, hostile like a subject: cleaned to
    /// one line without invisible characters (<see cref="Jira.Clean"/>).
    /// </summary>
    public static string IssueNotificationLine(MessageIssue issue)
    {
        ArgumentNullException.ThrowIfNull(issue);
        var key = Jira.Clean(issue.Info.Key);
        var summary = Jira.Clean(issue.Info.Summary);
        if (key.Length == 0)
        {
            return summary;
        }
        return summary.Length == 0 ? key : key + ": " + summary;
    }

    // s cut to NotificationBodyMax UTF-8 bytes on a character boundary, with
    // an ellipsis when anything was cut (Go: strings.ToValidUTF8(s[:n], "")
    // + "…"). A lone surrogate counts as the three bytes of the U+FFFD the
    // encoder writes for it.
    private static string Capped(string s)
    {
        if (Encoding.UTF8.GetByteCount(s) <= NotificationBodyMax)
        {
            return s;
        }
        var bytes = 0;
        var end = 0;
        while (end < s.Length)
        {
            Rune.DecodeFromUtf16(s.AsSpan(end), out var rune, out var consumed);
            if (bytes + rune.Utf8SequenceLength > NotificationBodyMax)
            {
                break;
            }
            bytes += rune.Utf8SequenceLength;
            end += consumed;
        }
        return string.Concat(s.AsSpan(0, end), "…");
    }
}

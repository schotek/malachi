// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/MailModel+Jira.swift; GTK:
// ui/internal/window/model.go (alwaysGrouped, countsUnread).
//
// The message list of an issue-tracker (Jira) account: its folders are
// always listed as conversations, one per issue, whatever the "group by
// conversation" setting (Jira.AlwaysThreaded), and an event (a status or
// assignee change) never counts as unread.

using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>The Jira rules of the main window's model.</summary>
public sealed partial class MailModel
{
    /// <summary>
    /// Whether folder <paramref name="k"/> is listed as conversations whatever
    /// the setting: a folder of a Jira account (model.go
    /// <c>alwaysGrouped</c>). False for no folder or an unknown account. The
    /// outbox stays flat all the same (the caller's rule, as for mail).
    /// </summary>
    public bool AlwaysGrouped(FolderKey? k) =>
        k is { } key && Account(key.Account) is { } a && Jira.AlwaysThreaded(a.Config);

    /// <summary>
    /// Whether a notified message raises its conversation's unread count
    /// (model.go <c>countsUnread</c>): an unseen message that is not an event
    /// of an issue (the daemon stores events seen; this holds even when one
    /// arrives unseen).
    /// </summary>
    public static bool CountsUnread(MessageSummary s)
    {
        System.ArgumentNullException.ThrowIfNull(s);
        return !FolderTree.HasFlag(s.Flags, Flag.Seen) && !Jira.IsEvent(s.Issue);
    }
}

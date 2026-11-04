// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraCompose.swift; GTK:
// ui/internal/jira/compose.go (CommentFormats, CommentAllows,
// VisibilityOptions, allowsBoth, SelectedVisibility, CommentTitle,
// CommentCompose, SendProblem, CommentQueued, ReplyLabel).
//
// A comment is written in the compose window, in a comment mode for a draft
// with Draft.Comment (draft.create reply on an account with the comment
// capability): the title names the issue, there are no recipients, subject
// or attachments and no Save Draft (a comment draft stays on this
// computer), the toolbar keeps only CommentFormats, and a service-desk
// issue offers the choice between a reply to the customer and an internal
// note. Sending queues the comment like a message.
//
// Windows: Go's Format is the enum JiraFormat, its CommentWindow and
// VisibilityOption the records JiraCommentWindow and JiraVisibilityOption;
// CommentCompose answers null for a mail draft where Go answers false.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;

namespace Malachi.Core.IssueTrackers;

public static partial class Jira
{
    /// <summary>
    /// jira.CommentFormats: the controls of the comment mode, in toolbar
    /// order: what both Jira Cloud (ADF) and Data Center (wiki markup) keep.
    /// </summary>
    public static IReadOnlyList<JiraFormat> CommentFormats { get; } =
    [
        JiraFormat.Bold, JiraFormat.Italic, JiraFormat.Code, JiraFormat.Link,
        JiraFormat.BulletList, JiraFormat.NumberedList, JiraFormat.Quote, JiraFormat.Clear,
    ];

    /// <summary>jira.CommentAllows: a control the comment mode keeps.</summary>
    public static bool CommentAllows(JiraFormat f) => CommentFormats.Contains(f);

    /// <summary>
    /// jira.VisibilityOptions: the choices of a comment on
    /// <paramref name="issue"/>: a reply to the customer and an internal note
    /// when the issue allows both (a service-desk request), none otherwise
    /// (the comment is public).
    /// </summary>
    public static IReadOnlyList<JiraVisibilityOption> VisibilityOptions(IssueInfo issue)
    {
        if (!AllowsBoth(issue))
        {
            return [];
        }
        return
        [
            // TRANSLATORS: a comment on a service-desk request that the customer reads too.
            new JiraVisibilityOption(CommentVisibility.Public, L10n.T("Reply to Customer")),
            // TRANSLATORS: a comment on a service-desk request that only the team reads.
            new JiraVisibilityOption(CommentVisibility.Internal, L10n.T("Internal Note")),
        ];
    }

    // allowsBoth: an issue whose comments may be public or internal: its
    // CommentVisibilities are exactly those two.
    private static bool AllowsBoth(IssueInfo issue)
    {
        ArgumentNullException.ThrowIfNull(issue);
        var isPublic = false;
        var isInternal = false;
        foreach (var v in issue.CommentVisibilities ?? [])
        {
            switch (v.Value)
            {
                case CommentVisibility.Public:
                    isPublic = true;
                    break;
                case CommentVisibility.Internal:
                    isInternal = true;
                    break;
                default:
                    return false;
            }
        }
        return isPublic && isInternal;
    }

    /// <summary>
    /// jira.SelectedVisibility: the visibility the comment mode shows as
    /// chosen: internal only when the draft asks for it and the issue allows
    /// it, public otherwise.
    /// </summary>
    public static CommentVisibility SelectedVisibility(DraftComment c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.Visibility == CommentVisibility.Internal && AllowsBoth(c.Issue) ? CommentVisibility.Internal : CommentVisibility.Public;
    }

    /// <summary>jira.CommentTitle: the compose window's title for a comment on the issue with <paramref name="key"/>.</summary>
    public static string CommentTitle(string key) =>
        // TRANSLATORS: title of the window that writes a comment; %s is an issue key such as "ITSD-42".
        L10n.T("Comment on %s", Clean(key));

    /// <summary>jira.CommentCompose: the comment mode of draft <paramref name="d"/>; null for a draft of an e-mail.</summary>
    public static JiraCommentWindow? CommentCompose(Draft d)
    {
        ArgumentNullException.ThrowIfNull(d);
        if (d.Comment is not { } comment)
        {
            return null;
        }
        return new JiraCommentWindow
        {
            Title = CommentTitle(comment.Issue.Key),
            Visibilities = VisibilityOptions(comment.Issue),
            Visibility = SelectedVisibility(comment),
            Formats = [.. CommentFormats],
        };
    }

    /// <summary>
    /// jira.SendProblem: why a comment with the editor's plain text cannot
    /// be sent; "" when it can. A comment of spaces and invisible characters
    /// only is empty.
    /// </summary>
    public static string SendProblem(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        for (var i = 0; i < text.Length;)
        {
            var status = Rune.DecodeFromUtf16(text.AsSpan(i), out var r, out var used);
            i += Math.Max(used, 1);
            // A lone surrogate is Go's RuneError of invalid UTF-8, which
            // TrimFunc keeps as text.
            if (status != OperationStatus.Done || !(Assistant.IsSpace(r.Value) || Assistant.IsControl(r.Value) || IsFormat(r.Value)))
            {
                return "";
            }
        }
        return L10n.T("Write a comment first");
    }

    /// <summary>jira.CommentQueued: the toast after a comment was sent to the outbox.</summary>
    public static string CommentQueued() => L10n.T("Comment queued");

    /// <summary>
    /// jira.ReplyLabel: the label of the Reply action: "Comment" when
    /// replying to the selected message writes a comment (the comment
    /// capability).
    /// </summary>
    public static string ReplyLabel(bool comment) =>
        // TRANSLATORS: the Reply action of an issue: writes a comment.
        comment ? L10n.T("Comment") : L10n.T("Reply");
}

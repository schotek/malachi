// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/drafts.go (draftTextBytes, Drafts,
// SameDrafts, DraftBadge, DraftEditLabel, DraftDiscardLabel,
// DiscardQuestion, DraftGone, DraftSaved, DraftsFirst); macOS:
// MalachiCore/Model/ConversationDrafts.swift.
//
// The user's unsent replies to the conversation (thread.get's drafts with
// withDrafts; ThreadSummary.DraftCount counts them), on an issue the unsent
// comments, are draft cards: the pane shows them right under what opened
// the conversation, above the newest member, newest first (DraftsFirst),
// with a Draft badge, when they were saved, their plain text and Edit and
// Discard. They are no members: never unread, never marked read, never
// folded, never among the messages the conversation's actions take, and
// not in ConversationModel.Items (whose rules they would change). Edit
// opens the draft in a compose window (a comment window for a comment
// draft); Discard asks DiscardQuestion and deletes it (draft.delete).
// notify.draftsChanged naming the thread asks thread.get again.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>draftTextBytes: the most of a draft's text a draft card shows, in UTF-8 bytes; Edit shows it whole.</summary>
    public const int DraftTextBytes = 64 << 10;

    /// <summary>
    /// conversation.Drafts: the draft cards of a conversation from
    /// thread.get's drafts: those of <paramref name="account"/> only (null:
    /// a conversation whose account is not known, any account's), without an
    /// id or with a repeated one (the first kept), newest
    /// <see cref="Draft.UpdatedAt"/> first (ties by id, the greater first), at
    /// most <see cref="API.Limits.MaxThreadDrafts"/>.
    /// </summary>
    public static IReadOnlyList<ConversationDraftCard> Drafts(IReadOnlyList<Draft>? drafts, Account? account)
    {
        var own = account?.Id.Value ?? "";
        var seen = new HashSet<DraftId>();
        var list = new List<Draft>();
        foreach (var d in drafts ?? [])
        {
            if (d.Id is not { } id || string.IsNullOrEmpty(id.Value) || seen.Contains(id))
            {
                continue;
            }
            var of = d.AccountId.Value ?? "";
            if (own.Length > 0 && of.Length > 0 && of != own)
            {
                continue;
            }
            seen.Add(id);
            list.Add(d);
        }
        // A stable sort, as Go's slices.SortStableFunc.
        var sorted = list
            .Select((d, i) => (Draft: d, Index: i))
            .OrderBy(e => e, Comparer<(Draft Draft, int Index)>.Create(CompareDrafts))
            .Select(e => e.Draft)
            .Take(API.Limits.MaxThreadDrafts);
        var name = Jira.Clean(account?.Config.DisplayName);
        if (name.Length == 0)
        {
            name = Jira.Clean(account?.Config.Email);
        }
        var output = new List<ConversationDraftCard>();
        foreach (var d in sorted)
        {
            var internalComment = d.Comment is { } c && c.Visibility == CommentVisibility.Internal;
            output.Add(new ConversationDraftCard
            {
                Draft = d,
                Sender = name,
                Text = Board.CleanBlock(d.TextBody, DraftTextBytes),
                Comment = d.Comment is not null,
                Internal = internalComment,
                InternalLabel = internalComment ? Jira.InternalLabel() : "",
            });
        }
        return output;
    }

    // Newest UpdatedAt first; equal times by id, the greater first; equal
    // ids (none after the dedup) in their order.
    private static int CompareDrafts((Draft Draft, int Index) a, (Draft Draft, int Index) b)
    {
        var at = a.Draft.UpdatedAt.UtcDateTime;
        var bt = b.Draft.UpdatedAt.UtcDateTime;
        if (at > bt)
        {
            return -1;
        }
        if (bt > at)
        {
            return 1;
        }
        var ids = string.CompareOrdinal(b.Draft.Id?.Value ?? "", a.Draft.Id?.Value ?? "");
        return ids != 0 ? ids : a.Index.CompareTo(b.Index);
    }

    /// <summary>
    /// conversation.SameDrafts: whether two lists of draft cards show the
    /// same: the same drafts in the same order at the same versions.
    /// </summary>
    public static bool SameDrafts(IReadOnlyList<ConversationDraftCard>? a, IReadOnlyList<ConversationDraftCard>? b)
    {
        a ??= [];
        b ??= [];
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            var (x, y) = (a[i], b[i]);
            if (x.Draft.Id != y.Draft.Id || x.Draft.Version != y.Draft.Version || x.Text != y.Text || x.Internal != y.Internal)
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>conversation.DraftBadge: the badge of a draft card and of a conversation row with a draft.</summary>
    public static string DraftBadge()
    {
        // TRANSLATORS: a badge on a card of the user's unsent reply or comment in a conversation.
        return L10n.C("conversation", "Draft");
    }

    /// <summary>conversation.DraftEditLabel: the button of a draft card that opens it for editing.</summary>
    public static string DraftEditLabel() => L10n.T("Edit");

    /// <summary>conversation.DraftDiscardLabel: the button of a draft card that deletes it, after <see cref="DiscardQuestion"/>.</summary>
    public static string DraftDiscardLabel() => L10n.T("_Discard");

    /// <summary>
    /// conversation.DiscardQuestion: the question before a draft card's
    /// Discard deletes the draft (the compose window's own question).
    /// </summary>
    public static string DiscardQuestion() => L10n.T("Discard this message?");

    /// <summary>conversation.DraftGone: the toast of an Edit whose draft is gone (sent or deleted meanwhile).</summary>
    public static string DraftGone() => L10n.T("The draft is no longer there");

    /// <summary>
    /// conversation.DraftSaved: the line of a draft card that says when it
    /// was saved; <paramref name="when"/> is the client's own short time of
    /// <see cref="Draft.UpdatedAt"/>.
    /// </summary>
    public static string DraftSaved(string when) => L10n.T("Draft saved %s", when);

    /// <summary>
    /// conversation.DraftsFirst: where the pane puts the draft cards among
    /// the items it shows (<see cref="ConversationLayout.DisplayOrder"/>):
    /// right after the card that opened the conversation when it is first
    /// (<paramref name="root"/> is 0), else first.
    /// </summary>
    public static int DraftsFirst(int root) => root == 0 ? 1 : 0;
}

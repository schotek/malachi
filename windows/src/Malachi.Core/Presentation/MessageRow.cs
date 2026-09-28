// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/widget/message_row.go (MessageRow: SetMessage,
// SetThread, SetReserveExpander, fill, SetExpanded, SetMember, applyLead,
// SetCompact, SetShowPreview, SetShowAvatar; the margins and sizes of
// message_row.blp), threads.go (the row a listRow becomes: newMessageRow,
// syncRows) and widget/highlight.go (the matched words in bold); macOS:
// MessageList/MessageCellView.swift, which keeps this in AppKit and Windows
// in Core (docs/windows-port.md §7.4, §11.2).
//
// One row of the message list, as a view model the WinUI ListView keeps
// across snapshots: ListController publishes its rows keyed by ListKey, and
// the view hands each new row to the view model of its key (KeyedListSync's
// view overload), which raises only what changed, so a flag change never
// replaces a row and drops the selection (§7.5). Every text is hostile input
// shown as plain text; the matched words of a search result come as UTF-16
// ranges the view sets in bold, never as markup.

using System;
using System.Collections.Generic;
using System.Linq;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>One row of the message list.</summary>
public sealed partial class MessageRow : ObservableObject
{
    /// <summary>message_row.blp content_box margin-start (message_row.go <c>rowMarginStart</c>).</summary>
    public const double MarginStartPlain = 6;

    /// <summary>The tighter start of a grouped list's rows, the fold arrow in front (<c>threadRowMarginStart</c>).</summary>
    public const double MarginStartGrouped = 2;

    /// <summary>message_row.blp content_box margin-end.</summary>
    public const double MarginEnd = 6;

    /// <summary>lead_box spacing (<c>leadSpacing</c>).</summary>
    public const double LeadSpacing = 4;

    /// <summary>A member's indent without avatars (<c>memberIndent</c>).</summary>
    public const double MemberIndent = 24;

    /// <summary>The avatar of the comfortable density (<c>avatarSizeComfortable</c>).</summary>
    public const double AvatarComfortable = 40;

    /// <summary>The avatar of the compact density (<c>avatarSizeCompact</c>).</summary>
    public const double AvatarCompact = 28;

    /// <summary>Above and below the content, comfortable (<c>rowMarginComfortable</c>).</summary>
    public const double MarginComfortable = 8;

    /// <summary>Above and below the content, compact (<c>rowMarginCompact</c>).</summary>
    public const double MarginCompact = 3;

    private IReadOnlyList<Utf16Range> highlights = [];

    /// <summary>A row for <paramref name="key"/>; <see cref="Update"/> fills it.</summary>
    public MessageRow(ListKey key)
    {
        Key = key;
        AvatarText = "";
        Sender = "";
        SenderTooltip = "";
        Subject = "";
        Preview = "";
        DateText = "";
        Origin = "";
        OriginTooltip = "";
        CountText = "";
        CountTooltip = "";
        ExpanderIcon = "pan-end-symbolic";
        ExpanderTooltip = "";
        AvatarSize = AvatarComfortable;
        MarginStart = MarginStartPlain;
        MarginTop = MarginComfortable;
        MarginBottom = MarginComfortable;
        ShowPreview = true;
    }

    /// <summary>The row's key across snapshots.</summary>
    public ListKey Key { get; }

    /// <summary>A folded or unfolded conversation of two or more members.</summary>
    [ObservableProperty]
    public partial bool IsThread { get; private set; }

    /// <summary>A member of an unfolded conversation, indented under it.</summary>
    [ObservableProperty]
    public partial bool IsMember { get; private set; }

    /// <summary>What the avatar's colour and initials come from: the first sender's (or participant's) display name.</summary>
    [ObservableProperty]
    public partial string AvatarText { get; private set; }

    /// <summary>The sender, or a conversation's participants.</summary>
    [ObservableProperty]
    public partial string Sender { get; private set; }

    /// <summary>The full address, or every participant's, one per line.</summary>
    [ObservableProperty]
    public partial string SenderTooltip { get; private set; }

    /// <summary>The subject, or "(No subject)".</summary>
    [ObservableProperty]
    public partial string Subject { get; private set; }

    /// <summary>The preview line, or a search result's excerpt.</summary>
    [ObservableProperty]
    public partial string Preview { get; private set; }

    /// <summary>The matched words of a search result's excerpt, as UTF-16 ranges into <see cref="Preview"/> (at most 32).</summary>
    public IReadOnlyList<Utf16Range> Highlights
    {
        get => highlights;
        private set
        {
            if (highlights.SequenceEqual(value))
            {
                return;
            }
            highlights = value;
            OnPropertyChanged();
        }
    }

    /// <summary>The date as the list shows it (widget.FormatDate).</summary>
    [ObservableProperty]
    public partial string DateText { get; private set; }

    /// <summary>Unread: sender and subject in bold, the unread dot.</summary>
    [ObservableProperty]
    public partial bool Unread { get; private set; }

    /// <summary>Flagged: the star.</summary>
    [ObservableProperty]
    public partial bool Flagged { get; private set; }

    /// <summary>With attachments: the paper clip.</summary>
    [ObservableProperty]
    public partial bool HasAttachments { get; private set; }

    /// <summary>Where a search result lies; "" hides the label.</summary>
    [ObservableProperty]
    public partial string Origin { get; private set; }

    /// <summary>The folder's path and account of a search result.</summary>
    [ObservableProperty]
    public partial string OriginTooltip { get; private set; }

    /// <summary>A conversation's member count, "" below two (widget.ThreadCountText).</summary>
    [ObservableProperty]
    public partial string CountText { get; private set; }

    /// <summary>"%d messages" for the count.</summary>
    [ObservableProperty]
    public partial string CountTooltip { get; private set; }

    /// <summary>The fold arrow takes its place (a live arrow, or the kept place of a grouped list's plain row).</summary>
    [ObservableProperty]
    public partial bool ExpanderShown { get; private set; }

    /// <summary>The arrow is visible and folds (a conversation whose members are not loading).</summary>
    [ObservableProperty]
    public partial bool ExpanderLive { get; private set; }

    /// <summary>pan-end folded, pan-down unfolded (GTK names).</summary>
    [ObservableProperty]
    public partial string ExpanderIcon { get; private set; }

    /// <summary>Expand or Collapse on a live arrow; "" otherwise.</summary>
    [ObservableProperty]
    public partial string ExpanderTooltip { get; private set; }

    /// <summary>The conversation is unfolded.</summary>
    [ObservableProperty]
    public partial bool Expanded { get; private set; }

    /// <summary>The spinner in the arrow's place while the members load.</summary>
    [ObservableProperty]
    public partial bool Loading { get; private set; }

    /// <summary>The avatar is shown (the setting, and never on a member row).</summary>
    [ObservableProperty]
    public partial bool AvatarVisible { get; private set; }

    /// <summary>The avatar's size by density.</summary>
    [ObservableProperty]
    public partial double AvatarSize { get; private set; }

    /// <summary>The avatar in neutral grey.</summary>
    [ObservableProperty]
    public partial bool Monochrome { get; private set; }

    /// <summary>The content's start margin (applyLead).</summary>
    [ObservableProperty]
    public partial double MarginStart { get; private set; }

    /// <summary>The content's top margin by density.</summary>
    [ObservableProperty]
    public partial double MarginTop { get; private set; }

    /// <summary>The content's bottom margin by density.</summary>
    [ObservableProperty]
    public partial double MarginBottom { get; private set; }

    /// <summary>The preview line is shown.</summary>
    [ObservableProperty]
    public partial bool ShowPreview { get; private set; }

    /// <summary>The last row: no hairline under it (window.blp show-separators; style.go).</summary>
    [ObservableProperty]
    public partial bool IsLast { get; set; }

    /// <summary>
    /// Shows <paramref name="row"/>: a conversation row from its aggregates,
    /// a message row from <paramref name="message"/> (what the list's
    /// <c>RowMessage</c> makes of its summary, the excerpt and origin of a
    /// search result included), laid out by <paramref name="look"/>, the
    /// date against <paramref name="now"/>.
    /// </summary>
    public void Update(ListRow row, RowMessage message, RowAppearance look, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(row);
        ArgumentNullException.ThrowIfNull(message);
        bool thread;
        bool loading;
        if (row.Thread && row.Summary is { } summary)
        {
            // SetThread: the participants where the sender goes, the member
            // count in a badge, the fold arrow (or the spinner).
            var t = Model.MailModel.SummaryThread(summary, row.Expanded, row.Loading);
            var first = t.Participants.Count > 0 ? t.Participants[0] : new Address { Email = "" };
            AvatarText = Format.DisplayName(first);
            Sender = Format.FormatParticipants(t.Participants);
            SenderTooltip = string.Join("\n", t.Participants.Select(Format.FormatAddress));
            Fill(t.Subject, t.Snippet, t.Date, t.Unread > 0, t.Flagged, t.HasAttachments, now);
            Highlights = [];
            Origin = "";
            OriginTooltip = "";
            CountText = Format.ThreadCountText(t.Count);
            // TRANSLATORS: tooltip of the member count of a conversation row.
            CountTooltip = L10n.N("%d message", "%d messages", t.Count);
            thread = true;
            loading = t.Loading;
            Expanded = t.Expanded;
        }
        else
        {
            // SetMessage: the first sender; a message without one gets an
            // empty name.
            var from = message.From.Count > 0 ? message.From[0] : new Address { Email = "" };
            var name = Format.DisplayName(from);
            AvatarText = name;
            Sender = name;
            SenderTooltip = Format.FormatAddress(from);
            Fill(message.Subject, message.Snippet, message.Date, message.Unread, message.Flagged, message.HasAttachments, now);
            Highlights = SearchModel.HighlightRanges(message.Snippet, message.Highlights);
            Origin = message.Origin;
            OriginTooltip = message.OriginTooltip;
            CountText = "";
            CountTooltip = "";
            thread = false;
            loading = false;
            Expanded = false;
        }
        IsThread = thread;
        Loading = thread && loading;
        IsMember = row.Member;
        Monochrome = look.Monochrome;
        ShowPreview = look.ShowPreview;
        // SetCompact.
        MarginTop = look.Compact ? MarginCompact : MarginComfortable;
        MarginBottom = MarginTop;
        AvatarSize = look.Compact ? AvatarCompact : AvatarComfortable;
        ApplyLead(thread, loading, reserve: !thread && look.Grouped, look.ShowAvatars);
    }

    /// <summary>
    /// What a screen reader names the row: the texts it shows, in order,
    /// with what its unread dot, star and paper clip show said after the
    /// subject (the list filter's Unread and Flagged, the attachment chip's
    /// fallback name: the words GTK has for them).
    /// </summary>
    public override string ToString()
    {
        var parts = new List<string> { Sender };
        if (CountText.Length > 0)
        {
            parts.Add(CountTooltip);
        }
        parts.Add(Subject);
        if (Unread)
        {
            parts.Add(L10n.T("Unread"));
        }
        if (Flagged)
        {
            parts.Add(L10n.T("Flagged"));
        }
        if (HasAttachments)
        {
            parts.Add(L10n.T("Attachment"));
        }
        if (Origin.Length > 0)
        {
            parts.Add(Origin);
        }
        parts.Add(DateText);
        if (ShowPreview && Preview.Length > 0)
        {
            parts.Add(Preview);
        }
        return string.Join(", ", parts.Where(p => p.Length > 0));
    }

    // message_row.go fill: the parts a message and a conversation row share.
    private void Fill(string subject, string snippet, DateTimeOffset date, bool unread, bool flagged, bool attachments, DateTimeOffset now)
    {
        DateText = Format.FormatDate(date, now);
        Subject = LoadedMessageText.SubjectText(subject);
        Preview = snippet;
        HasAttachments = attachments;
        Flagged = flagged;
        Unread = unread;
    }

    // message_row.go applyLead: on a conversation row the arrow (or the
    // spinner while loading), on a plain row of a grouped list the arrow's
    // place kept empty (invisible and inert, so a click reaches the row), on
    // a flat list's row nothing. A member row shows no avatar and is
    // indented so that its text lines up with its conversation's, or by a
    // fixed step when avatars are off.
    private void ApplyLead(bool thread, bool loading, bool reserve, bool showAvatars)
    {
        var live = thread && !loading;
        ExpanderLive = live;
        ExpanderShown = live || (!thread && reserve);
        ExpanderIcon = Expanded ? "pan-down-symbolic" : "pan-end-symbolic";
        ExpanderTooltip = !live ? "" : Expanded ? L10n.T("Collapse") : L10n.T("Expand");
        AvatarVisible = showAvatars && !IsMember;
        var margin = thread || reserve ? MarginStartGrouped : MarginStartPlain;
        if (IsMember)
        {
            margin += showAvatars ? AvatarSize + LeadSpacing : MemberIndent;
        }
        MarginStart = margin;
    }
}

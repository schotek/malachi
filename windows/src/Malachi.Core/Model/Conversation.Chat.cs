// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/chat.go (ChatRows, ChatScrollTo,
// BubbleText, ChatToggleLabel, ChatToggleTooltip, BubbleRendersHTML,
// ChatPreviewLines, BubbleShortened, ChatShowMore, ChatTruncateMineRowTitle,
// ChatTruncateOthersRowTitle, ChatTruncateRowSubtitle,
// ChatPreferencesGroupTitle; Expanded is ConversationExpanded.cs); macOS:
// MalachiCore/Model/ConversationChat.swift. Its types are ChatSide,
// ChatRowKind and ChatRow; the rules of the view that Go leaves to the
// clients are Conversation.ChatBody.cs.
//
// The conversation shows in one of two layouts, which a toggle above it
// switches for every conversation (the settings key
// conversation-chat-layout, SettingsStore.ConversationChatLayout, which
// outlives a restart): the timeline (the cards stacked with the avatars and
// the line in the left margin, the card that opened the conversation on
// top, the others newest first, open at the top; the default) and the chat.
// The chat reads like a messaging app: the others' messages as bubbles on
// the left, the user's on the right, oldest first from the top, open at the
// end (ChatScrollTo). A bubble is plain text only: the sender, the date, the
// unread dot, a paperclip, the Jira badges and the message's text as the
// daemon derived it, with the quoted history cut (message.body's text with
// trimQuoted; BubbleText), or, with the preference conversation-chat-html
// (SettingsStore.ConversationChatHtml, BubbleRendersHtml), the sanitized HTML
// of the timeline's card in the same locked web view. It has no buttons, no attachment chips, no web
// view and folds never; its text cannot be selected, so that a double click
// anywhere in it opens the message in its own window (a draft in the
// compose window), where everything else is. Events and the row of older
// messages are centred rows without a bubble; the drafts come last.
//
// The model does not change: the chat is a second layout over the same
// conversation controller and its cache of bodies, so that switching shows
// the same conversation without asking thread.get again. A bubble shows the
// summary's snippet, dimmed, until its body is loaded, and the chat asks for
// the bodies of the bubbles near the visible part only (two heights of the
// view, counted from the end), as the timeline does for its cards. Where the
// bubbles stand and what is near are the client's measures
// (ConversationLayout.ChatBubbleMax), not this file's.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Boards;
using Malachi.Core.I18n;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>
    /// conversation.ChatRows: the rows of the chat of <paramref name="m"/>,
    /// top to bottom: the row of older messages first when there is one
    /// (centred), then the items of <paramref name="m"/> in the model's order
    /// (oldest first), a message as a bubble and an event centred, then the
    /// drafts (<see cref="Drafts"/>' cards) at the end, on the right, the
    /// oldest saved first (equal <see cref="Api.Draft.UpdatedAt"/> in the
    /// order given; <paramref name="drafts"/> itself is not reordered).
    /// </summary>
    /// <remarks>
    /// A bubble stands on the right when the user wrote it
    /// (<see cref="ConversationItem.Mine"/>) or it is the user's reply in
    /// Sent (<see cref="ConversationItem.Sent"/>): a reply sent from another
    /// identity of the user, whose address is not the account's, has Mine
    /// false and still stands on the right. A comment an integration relayed
    /// (<see cref="ConversationItem.Via"/>) is never Mine, whatever the site
    /// says, so it stands on the left. The issue card is no row: the client
    /// puts it above them, across the column.
    /// </remarks>
    public static IReadOnlyList<ChatRow> ChatRows(ConversationModel m, IReadOnlyList<ConversationDraftCard>? drafts)
    {
        ArgumentNullException.ThrowIfNull(m);
        drafts ??= [];
        var rows = new List<ChatRow>(m.Items.Count + drafts.Count);
        foreach (var it in m.Items)
        {
            if (it.Kind == ConversationItemKind.Truncated)
            {
                rows.Add(new ChatRow(ChatRowKind.Truncated, ChatSide.Center, Item: it));
            }
        }
        foreach (var it in m.Items)
        {
            switch (it.Kind)
            {
                case ConversationItemKind.Message:
                    rows.Add(new ChatRow(ChatRowKind.Bubble, it.Mine || it.Sent ? ChatSide.Right : ChatSide.Left, Item: it));
                    break;
                case ConversationItemKind.Event:
                    rows.Add(new ChatRow(ChatRowKind.Event, ChatSide.Center, Item: it));
                    break;
            }
        }
        // A stable sort, as Go's slices.SortStableFunc: OrderBy keeps the
        // order given for equal keys, and sorts a copy.
        foreach (var d in drafts.OrderBy(card => card.Draft.UpdatedAt.UtcDateTime))
        {
            rows.Add(new ChatRow(ChatRowKind.Draft, ChatSide.Right, Draft: d));
        }
        return rows;
    }

    /// <summary>
    /// conversation.ChatScrollTo: the index into <paramref name="rows"/> the
    /// chat scrolls to when it opens a conversation: the last row (the newest
    /// draft, else the newest message); -1 when there are no rows (or none
    /// given). It is not <see cref="ConversationModel.ScrollTo"/>, which
    /// indexes <see cref="ConversationModel.Items"/>.
    /// </summary>
    public static int ChatScrollTo(IReadOnlyList<ChatRow>? rows) => (rows?.Count ?? 0) - 1;

    /// <summary>
    /// conversation.BubbleText: the text of a message bubble from the plain
    /// text of its loaded body (message.body's text, the variant without the
    /// quoted history): cleaned as a draft card's text is
    /// (<see cref="Board.CleanBlock"/>, at most <see cref="DraftTextBytes"/>:
    /// line breaks kept, at most one empty line in a row, control and format
    /// characters gone, trimmed), so that a message bubble and a draft bubble
    /// read alike; "" for an empty text. The states of a body (on its way,
    /// too large, unreadable, empty) and the errors of the call are the
    /// view's to show, with the texts of the timeline's cards
    /// (<see cref="BubbleBodyFor"/>); until a body is asked for, the bubble
    /// shows the summary's snippet.
    /// </summary>
    public static string BubbleText(string? text) =>
        string.IsNullOrEmpty(text) ? "" : Board.CleanBlock(text, DraftTextBytes);

    /// <summary>
    /// conversation.ChatToggleLabel: the label of the toggle button above the
    /// conversation that switches its layout: it names the layout a click
    /// shows, so it follows the state (<paramref name="on"/>: the chat is
    /// shown, "Show as Thread" goes back to the timeline; off: "Show as
    /// Chat").
    /// </summary>
    public static string ChatToggleLabel(bool on)
    {
        if (!on)
        {
            // TRANSLATORS: a toggle button above a conversation in the reading pane, off; a click shows the messages as chat bubbles.
            return L10n.T("Show as Chat");
        }
        // TRANSLATORS: the same toggle button above a conversation, on (the messages show as chat bubbles); a click goes back to the conversation as cards.
        return L10n.T("Show as Thread");
    }

    /// <summary>
    /// conversation.ChatToggleTooltip: the tooltip of the toggle, which
    /// follows its state as the label does: off, what the chat shows; on,
    /// what a click goes back to.
    /// </summary>
    public static string ChatToggleTooltip(bool on)
    {
        if (!on)
        {
            // TRANSLATORS: the tooltip of the "Show as Chat" toggle above a conversation.
            return L10n.T("Others on the left, your messages on the right; double-click opens the message");
        }
        // TRANSLATORS: the tooltip of the "Show as Thread" toggle above a conversation shown as chat bubbles.
        return L10n.T("Back to the timeline: the messages as cards, newest first");
    }

    /// <summary>
    /// conversation.BubbleRendersHTML: whether a bubble of the chat layout
    /// shows the message's sanitized HTML in the locked web view the
    /// timeline's cards use (the same document as the card: the trimQuoted
    /// variant) instead of its plain text: the preference
    /// conversation-chat-html is on (<paramref name="setting"/>), the loaded
    /// body has HTML (<paramref name="hasHtml"/>) and it is not withheld
    /// (<paramref name="withheld"/>). A text-only message, a withheld or
    /// failed body and a body not loaded yet read as text (the snippet as
    /// placeholder) as without the preference.
    /// </summary>
    public static bool BubbleRendersHtml(bool setting, bool hasHtml, bool withheld) => setting && hasHtml && !withheld;

    /// <summary>
    /// conversation.ChatPreviewLines: how many lines of a message a shortened
    /// bubble shows (the preferences conversation-chat-truncate-mine and -others): a text
    /// bubble that many lines with an ellipsis, an HTML bubble its document
    /// clipped to that many lines' height with a fade, both with a "Show
    /// More" hint; a click anywhere in the bubble shows the whole message.
    /// </summary>
    public const int ChatPreviewLines = 3;

    /// <summary>
    /// conversation.BubbleShortened: the bubble shows the shortened preview:
    /// the preference of its side is on (<paramref name="mineOn"/> for the
    /// user's own, <paramref name="mine"/>, a draft counting as such;
    /// <paramref name="othersOn"/> for the others') and the user has not
    /// expanded it.
    /// </summary>
    public static bool BubbleShortened(bool mineOn, bool othersOn, bool mine, bool expanded) =>
        !expanded && (mine ? mineOn : othersOn);

    /// <summary>conversation.ChatShowMore: the hint under a shortened bubble.</summary>
    public static string ChatShowMore() =>
        // TRANSLATORS: the hint under a shortened message in the chat layout of a conversation; a click on the message shows all of it.
        L10n.T("Show More");

    /// <summary>conversation.ChatTruncateMineRowTitle: the row of the preference for the user's own bubbles (conversation-chat-truncate-mine).</summary>
    public static string ChatTruncateMineRowTitle() =>
        // TRANSLATORS: a switch in Preferences → Message list; on, the user's own messages in the chat layout of a conversation show only their first lines until clicked.
        L10n.T("Shorten My Messages in Chat");

    /// <summary>conversation.ChatTruncateOthersRowTitle: the row of the preference for the others' bubbles (conversation-chat-truncate-others).</summary>
    public static string ChatTruncateOthersRowTitle() =>
        // TRANSLATORS: a switch in Preferences → Message list; on, the other party's messages in the chat layout of a conversation show only their first lines until clicked.
        L10n.T("Shorten Others' Messages in Chat");

    /// <summary>conversation.ChatTruncateRowSubtitle: the subtitle of both rows.</summary>
    public static string ChatTruncateRowSubtitle() =>
        // TRANSLATORS: the subtitle of the "Shorten … Messages in Chat" switches in Preferences → Message list.
        L10n.T("About three lines of each message in its bubble; a click shows the whole message");

    /// <summary>conversation.ChatPreferencesGroupTitle: the group of the conversation's switches in Preferences → Message list.</summary>
    public static string ChatPreferencesGroupTitle() =>
        // TRANSLATORS: the title of a group of switches in Preferences → Message list about conversations (grouping, the chat layout).
        L10n.T("Conversations");
}

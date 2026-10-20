// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChatBody.swift
// (chatBody, bubbleBody, bubbleMonospace, bubbleAttachmentBadge; the HTML
// variant of bubbleBody with the preference conversation-chat-html); the
// layout half of that file (chatAtEnd, chatColumn, chatCompactDates,
// chatResize, chatEndTop) is ConversationLayout.Chat.cs. Swift-first:
// ui/internal/conversation/chat.go leaves these to the clients (BubbleText
// is the shared part, Conversation.Chat.cs), and the GTK and Windows chats
// port them from Swift. Swift's bubbleBody is BubbleBodyFor here: the type
// it returns is BubbleBody.
//
// The chat layout of the conversation view, the rules of the view that need
// no WinUI (docs/plans/chat-view.md D2, D3, D13, D17): which variant of a
// loaded body a message bubble reads, what its text is in every state of
// that body, and what its header shows of the attachments. Its one
// translated string, "Loading the message", is not new: it is the msgid of
// the timeline's cards for the error of message.body
// (conversation_card.go), as the reader uses it.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>
    /// The variant of <paramref name="lm"/>'s body a bubble reads (Swift
    /// <c>chatBody(of:quotedRevealed:)</c>): the one without the quoted
    /// history (D13), even when the user revealed it in the timeline.
    /// <paramref name="quotedRevealed"/> is whether
    /// <see cref="LoadedMessage.Body"/> is the whole body
    /// (<see cref="LoadedMessage.QuotedShown"/>): then the trimmed variant
    /// kept aside (<see cref="LoadedMessage.OtherBody"/>) when there is one,
    /// else the whole body, which is all there is; otherwise
    /// <see cref="LoadedMessage.Body"/>. Null: no body yet.
    /// </summary>
    public static MessageBodyResult? ChatBody(LoadedMessage lm, bool quotedRevealed)
    {
        ArgumentNullException.ThrowIfNull(lm);
        if (quotedRevealed && lm.OtherBody is { } trimmed)
        {
            return trimmed;
        }
        return lm.Body;
    }

    /// <summary>
    /// The text of the bubble of a message whose cache entry is
    /// <paramref name="lm"/> (null: nothing asked for yet, or let go) and
    /// whose summary's snippet is <paramref name="snippet"/> (Swift
    /// <c>bubbleBody(_:snippet:)</c>): the snippet as a placeholder until a
    /// body or its error is there; the error of message.body as the cards say
    /// it; a body in a state other than fetched as the cards say it
    /// (<see cref="LoadedMessageText.BodyText"/>); else the body's text
    /// cleaned (<see cref="BubbleText"/>), "(Empty message)" when nothing is
    /// left. The error belongs to <see cref="LoadedMessage.Body"/>, so it does
    /// not hide the trimmed variant kept aside.
    /// </summary>
    public static BubbleBody BubbleBodyFor(LoadedMessage? lm, string? snippet) => BubbleBodyFor(lm, snippet, renderHtml: false);

    /// <summary>
    /// <see cref="BubbleBodyFor(LoadedMessage?, string?)"/> with the
    /// preference conversation-chat-html (<paramref name="renderHtml"/>,
    /// <see cref="BubbleRendersHtml"/>): a fetched body with HTML that is not
    /// withheld is <see cref="BubbleBody.Html"/> (its fragment, its links and
    /// the text the bubble shows without a web view); every other state reads
    /// as without the preference. The variant is the one the bubble reads
    /// (<see cref="ChatBody"/>): the timeline card's document of the trimmed
    /// body.
    /// </summary>
    public static BubbleBody BubbleBodyFor(LoadedMessage? lm, string? snippet, bool renderHtml)
    {
        var placeholder = new BubbleBody.Placeholder(BubbleText(snippet));
        if (lm is null)
        {
            return placeholder;
        }
        var aside = lm.QuotedShown && lm.OtherBody is not null;
        if (!aside && lm.Err is { } err)
        {
            return new BubbleBody.Text(RpcErrorText.Text(L10n.T("Loading the message"), err));
        }
        if (ChatBody(lm, lm.QuotedShown) is not { } b)
        {
            return placeholder;
        }
        switch (b.BodyState.Value)
        {
            case BodyState.Pending:
            case BodyState.TooBig:
            case BodyState.Failed:
                return new BubbleBody.Text(LoadedMessageText.BodyText(b));
        }
        var text = BubbleText(b.Text);
        var shown = text.Length == 0 ? LoadedMessageText.BodyText(null) : text;
        if (BubbleRendersHtml(renderHtml, b.HasHtml, b.HtmlWithheld == true) && LoadedMessageText.ShowsHtml(b))
        {
            return new BubbleBody.Html(b.Html!, b.Links, shown);
        }
        return new BubbleBody.Text(shown);
    }

    /// <summary>
    /// Whether the bubble of a message whose cache entry is
    /// <paramref name="lm"/> shows its text in the monospaced font (Swift
    /// <c>bubbleMonospace(_:setting:)</c>): only with the setting for plain
    /// text (<paramref name="monospace"/>) and only for a body that is plain
    /// text, not the text the daemon derived from HTML
    /// (<see cref="MessageBodyResult.HasHtml"/>), as the timeline's cards keep
    /// the setting to plain-text bodies. A placeholder, an error and a body
    /// in another state than fetched use the normal font.
    /// </summary>
    public static bool BubbleMonospace(LoadedMessage? lm, bool monospace)
    {
        if (!monospace || lm is null)
        {
            return false;
        }
        var aside = lm.QuotedShown && lm.OtherBody is not null;
        if ((!aside && lm.Err is not null) || ChatBody(lm, lm.QuotedShown) is not { } b)
        {
            return false;
        }
        return b.BodyState == BodyState.Fetched && !b.HasHtml;
    }

    /// <summary>
    /// The attachments pill of the bubble of a message whose summary is
    /// <paramref name="summary"/> and whose cache entry is
    /// <paramref name="loaded"/> (null: nothing asked for yet, or let go;
    /// Swift <c>bubbleAttachmentBadge(summary:loaded:)</c>). Once message.get
    /// answered (<see cref="LoadedMessage.Msg"/>), the number of chips the
    /// timeline's card shows (<see cref="AttachmentChips.ChipAttachments"/>
    /// of the message and the body held, Swift's <c>ChipPlan</c>), whatever
    /// the summary says: the summary's flag counts only the attachments the
    /// daemon calls not inline (a part with a Content-ID is inline to IMAP
    /// and the MIME parser, Microsoft Graph's hasAttachments leaves out the
    /// inline ones), while a chip shows every part the body does not show
    /// itself; none when there are no chips. Before that, the pill alone when
    /// the summary has <see cref="MessageSummary.HasAttachments"/>, so that
    /// the bubble shows it from the first moment; else none. A failed
    /// message.get leaves the summary's word.
    /// </summary>
    public static BubbleAttachments BubbleAttachmentBadge(MessageSummary? summary, LoadedMessage? loaded)
    {
        if (loaded?.Msg is { } m)
        {
            var n = AttachmentChips.ChipAttachments(m.Attachments, loaded.Body).Count;
            return n > 0 ? new BubbleAttachments.Count(n) : new BubbleAttachments.None();
        }
        return summary?.HasAttachments == true ? new BubbleAttachments.Some() : new BubbleAttachments.None();
    }
}

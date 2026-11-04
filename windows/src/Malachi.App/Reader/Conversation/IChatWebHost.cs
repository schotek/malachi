// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What a bubble of the chat layout that renders its message's HTML (the
// preference conversation-chat-html, Conversation.BubbleRendersHtml) needs
// from the chat around it: the web views of the timeline's cards from the
// chat's pool (CardWebView: the same locked view, document builder, CSP,
// resolver rule and request gate), the card's link path, the pane's status
// for the link under the pointer, the wheel handed on over a document that
// fits, and the chat's scroll anchoring for a change of the bubble's height.
// macOS: the chat's role for ConversationBubbleView's web view; GTK:
// conversationChat for chatBubble's htmlview.Card. Two hosts: Mail's chat
// (ConversationChatView) and the board's (BoardConversationBlock).

using System;
using System.Collections.Generic;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;

namespace Malachi.App.Reader.Conversation;

/// <summary>The chat's side of a bubble's web view (<see cref="ConversationChatView"/>).</summary>
internal interface IChatWebHost
{
    /// <summary>A web view for a bubble going live: from the pool, its zoom up to date, or a new one.</summary>
    CardWebView TakeWebView();

    /// <summary>Takes a web view back (already out of its bubble).</summary>
    void ReturnWebView(CardWebView web);

    /// <summary>A link the user activated in a bubble's document, with the body's links (the cards' confirmation).</summary>
    void OpenLink(ActivatedLink link, IReadOnlyList<Link> links);

    /// <summary>The link under the pointer ("" hides it), in the pane's status.</summary>
    void Hover(string link);

    /// <summary>The wheel over a document that fits, in the wheel's units, for the chat to scroll.</summary>
    void ScrollBy(double wheelDelta);

    /// <summary>Runs <paramref name="change"/> (the height of <paramref name="bubble"/>) keeping what the user reads in place.</summary>
    void Reflow(FrameworkElement bubble, Action change);

    /// <summary>A bubble's want of a web view changed (the view failed): the live ones are decided anew.</summary>
    void LiveChanged();
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what the board detail gives its conversation block
// (BoardConversationBlock), the counterpart of the closures macOS sets on
// BoardConversationBlock (onShowInMail, onRetry, onHeightChanging, the
// actions' canShowInMail) and of what GTK's boardConversation reads from its
// boardPage (win.board-show-in-mail, ctl.RetryMessages,
// boardReplyScroller).

using Malachi.Core.Api;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Board;

/// <summary>The board detail that shows a <see cref="BoardConversationBlock"/>.</summary>
public interface IBoardConversationHost
{
    /// <summary>The window the detail is in: where a link's questions and toasts go.</summary>
    Window? HostWindow { get; }

    /// <summary>
    /// The detail's scrolling column the block sits in: the wheel over a card
    /// whose document fits scrolls it, and a card above the viewport that
    /// changes its height moves it by as much. Null: neither.
    /// </summary>
    ScrollViewer? Scroller { get; }

    /// <summary>Whether Show in Mail can show case <paramref name="id"/> (the samples cannot).</summary>
    bool CanShowInMail(BoardCaseId id);

    /// <summary>The heading's Show in Mail (macOS onShowInMail, GTK win.board-show-in-mail).</summary>
    void ShowInMail();

    /// <summary>The note's Try Again when the conversation could not be loaded (GTK ctl.RetryMessages).</summary>
    void RetryMessages();
}

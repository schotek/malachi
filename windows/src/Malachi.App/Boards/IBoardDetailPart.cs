// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the slots of the detail that its rebuild never touches
// (BoardDetailViewController.swift's replySlot and lower, GTK's reply_slot
// and board_detail_conversation_box). A part follows the detail in place
// and decides itself what changed: the conversation's cards keep their web
// views' documents and the inline reply editor its caret across the board's
// refreshes. BoardDetailView installs its defaults (the plain-text excerpts
// of the conversation, the samples' static suggested reply); the
// conversation cards and the inline reply editor replace them
// (BoardDetailView.ConversationPart, ReplyPart).

using Malachi.Core.Boards;
using Microsoft.UI.Xaml;

namespace Malachi.App.Boards;

/// <summary>A part of the board's detail that follows the selected case in place.</summary>
public interface IBoardDetailPart
{
    /// <summary>The element the detail puts into its slot.</summary>
    UIElement View { get; }

    /// <summary>
    /// The selected case changed, or the same case did (null: none). Called
    /// on every change of the detail, also when nothing the part shows
    /// differs: it compares itself.
    /// </summary>
    void Apply(Board.Detail? detail);
}

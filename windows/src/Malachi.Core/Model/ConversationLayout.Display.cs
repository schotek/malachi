// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift
// (Display); GTK: ui/internal/window/conversation_layout.go (convDisplay).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>
    /// convDisplay: the stack as the pane shows it (<see cref="DisplayOrder"/>):
    /// the items in order, which of them opened the conversation
    /// (<paramref name="Root"/>, an index into <paramref name="Items"/>; -1
    /// when it is not shown), whether that card starts folded to its header,
    /// and how every card starts (<paramref name="Folded"/>, by message id:
    /// <see cref="Conversation.DefaultFolds"/>).
    /// </summary>
    public sealed record Display(IReadOnlyList<ConversationItem> Items, int Root, bool RootFolded, IReadOnlyDictionary<MessageId, bool> Folded)
    {
        /// <summary>The id of the card that opened the conversation; null when it is not shown.</summary>
        public MessageId? Opening => Root >= 0 && Root < Items.Count ? Items[Root].Id : null;
    }
}

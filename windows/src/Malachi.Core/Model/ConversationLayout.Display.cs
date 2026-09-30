// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift
// (Display); GTK: ui/internal/window/conversation_layout.go (convDisplay).

using System.Collections.Generic;

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>
    /// convDisplay: the stack as the pane shows it (<see cref="DisplayOrder"/>):
    /// the items in order, which of them opened the conversation
    /// (<paramref name="Root"/>, an index into <paramref name="Items"/>; -1
    /// when it is not shown), and whether that card starts folded to its
    /// header.
    /// </summary>
    public sealed record Display(IReadOnlyList<ConversationItem> Items, int Root, bool RootFolded);
}

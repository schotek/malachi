// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationFold.swift
// (Conversation.FoldAll); GTK: ui/internal/conversation/fold.go (FoldAll).

namespace Malachi.Core.Model;

/// <summary>conversation.FoldAll: what the button above the conversation offers.</summary>
public enum ConversationFoldAll
{
    /// <summary>No button (fewer than two cards fold).</summary>
    None,

    /// <summary>Folds every card ("Collapse All").</summary>
    Collapse,

    /// <summary>Opens every card ("Expand All").</summary>
    Expand,
}

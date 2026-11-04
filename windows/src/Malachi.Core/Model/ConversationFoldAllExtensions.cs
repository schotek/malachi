// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationFold.swift
// (Conversation.FoldAll.folded, label); GTK: ui/internal/conversation/fold.go
// (FoldAll.Folded, FoldAll.Label).

using Malachi.Core.I18n;

namespace Malachi.Core.Model;

/// <summary>Swift's computed properties of <see cref="ConversationFoldAll"/>.</summary>
public static class ConversationFoldAllExtensions
{
    extension(ConversationFoldAll offer)
    {
        /// <summary>What the offer sets every card to (<see cref="ConversationFolds.SetAll"/>): true for Collapse All.</summary>
        public bool Folded => offer == ConversationFoldAll.Collapse;

        /// <summary>The button's text; "" for None.</summary>
        public string Label => offer switch
        {
            // TRANSLATORS: a button above a conversation in the reading pane; folds every message to its header.
            ConversationFoldAll.Collapse => L10n.T("Collapse All"),
            // TRANSLATORS: a button above a conversation in the reading pane; shows every message whole.
            ConversationFoldAll.Expand => L10n.T("Expand All"),
            _ => "",
        };
    }
}

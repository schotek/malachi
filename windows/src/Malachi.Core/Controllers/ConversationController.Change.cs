// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ConversationController.swift
// (Change); GTK: ui/internal/window/conversation_controller.go (convChange).

namespace Malachi.Core.Controllers;

/// <content>What changed, for the pane.</content>
public sealed partial class ConversationController
{
    /// <summary>What changed, for the pane.</summary>
    public enum Change
    {
        /// <summary>A conversation was selected; its members are on their way (<see cref="Model"/> is null).</summary>
        Loading,

        /// <summary>
        /// The model was built anew: the pane lays the stack out in the order
        /// it shows (ConversationLayout.DisplayOrder) and opens at its top.
        /// </summary>
        Opened,

        /// <summary>
        /// The shown conversation changed (a member arrived or went, its flags
        /// or issue changed, the daemon rebuilt its messages): the pane
        /// reconciles its cards by id and keeps what the user reads in place.
        /// </summary>
        Updated,

        /// <summary>Nothing is shown any more.</summary>
        Cleared,
    }
}

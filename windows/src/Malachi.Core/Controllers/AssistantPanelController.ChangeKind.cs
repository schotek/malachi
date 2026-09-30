// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (the cases of AssistantPanelController.Change); GTK:
// ui/internal/assistantpanel/controller.go (ChangeKind).

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>What a <see cref="Change"/> is.</summary>
    public enum ChangeKind
    {
        /// <summary>The transcript was emptied (New Conversation).</summary>
        Reset,

        /// <summary>An item was appended at the index.</summary>
        Appended,

        /// <summary>The item at the index changed its content.</summary>
        Updated,
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Phase); GTK: ui/internal/assistantpanel/
// controller.go (Phase). The controller's property is CurrentPhase, as the
// type takes the name.

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>Where a question is.</summary>
    public enum Phase
    {
        /// <summary>Nothing under way.</summary>
        Idle,

        /// <summary>Consent, the context, locating and checking Claude Code, the process starting.</summary>
        Preparing,

        /// <summary>A turn is under way.</summary>
        Running,
    }
}

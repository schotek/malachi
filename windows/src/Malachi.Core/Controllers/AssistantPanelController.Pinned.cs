// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Pinned); GTK: ui/internal/assistantpanel/
// controller.go (Pinned). Immutable: the controller replaces an entry whose
// context is resolved or whose model was told, where Swift mutates it; the
// key, internal in Swift and GTK, is public here.

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>
    /// One context of a conversation that keeps its context: what the chip
    /// showed at its first question, or a selection added since.
    /// </summary>
    /// <param name="Context">
    /// Null for all mail (nothing was selected, or the chip's context was
    /// removed, when the conversation began).
    /// </param>
    /// <param name="Announced">The model was told about it since its Claude Code started.</param>
    /// <param name="Key">Its identity while its members are resolved; never reused.</param>
    public sealed record Pinned(Context? Context, bool Announced, int Key);
}

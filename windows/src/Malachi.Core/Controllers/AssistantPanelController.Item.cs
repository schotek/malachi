// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Item); GTK: ui/internal/assistantpanel/
// controller.go (Item). Immutable: the controller replaces an item whose
// content changes, under the same id, where Swift mutates it.

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>One entry of the transcript.</summary>
    /// <param name="Id">Its identity, which stays with it while its content changes; never reused.</param>
    /// <param name="Content">What it shows.</param>
    public sealed record Item(int Id, AssistantPanelContent Content);
}

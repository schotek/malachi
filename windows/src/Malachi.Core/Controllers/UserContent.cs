// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.user); GTK: ui/internal/assistantpanel/
// controller.go (ContentUser).

namespace Malachi.Core.Controllers;

/// <summary>The user's question: the action's label and the typed text.</summary>
/// <param name="Label">The action's label ("Summarize"); "" for a free question.</param>
/// <param name="Text">The typed text; "" for an action that sends at once.</param>
public sealed record UserContent(string Label, string Text) : AssistantPanelContent;

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.activity); GTK:
// ui/internal/assistantpanel/controller.go (ContentActivity).

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>A tool at work (<see cref="Assistant.ActivityLabel"/>), <paramref name="Done"/> at its result.</summary>
/// <param name="Label">The line ("Reading a message…").</param>
/// <param name="Done">Whether the tool answered (or the turn ended).</param>
public sealed record ActivityContent(string Label, bool Done) : AssistantPanelContent;

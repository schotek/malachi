// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.assistant); GTK:
// ui/internal/assistantpanel/controller.go (ContentAssistant).

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>The answer as Markdown source (<see cref="Assistant.Markdown"/>), still arriving while <paramref name="Streaming"/>.</summary>
/// <param name="Text">The Markdown source: model text, shown as data.</param>
/// <param name="Streaming">Whether more of it is on its way.</param>
public sealed record AnswerContent(string Text, bool Streaming) : AssistantPanelContent;

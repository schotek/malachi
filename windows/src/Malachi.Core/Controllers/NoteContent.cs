// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.note); GTK:
// ui/internal/assistantpanel/controller.go (ContentNote).

namespace Malachi.Core.Controllers;

/// <summary>A remark of the panel's own ("The conversation was stopped").</summary>
/// <param name="Text">The line, translated.</param>
public sealed record NoteContent(string Text) : AssistantPanelContent;

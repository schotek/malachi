// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content); GTK: ui/internal/assistantpanel/
// controller.go (Content, ContentKind). Swift's enum with associated values
// is a closed record hierarchy at the namespace's top level (UserContent,
// AnswerContent, ActivityContent, DraftContent, ErrorContent, NoteContent),
// compared by value as the enum is; Swift's case assistant is AnswerContent.

namespace Malachi.Core.Controllers;

/// <summary>What one entry of the assistant panel's transcript shows (<see cref="AssistantPanelController.Item"/>).</summary>
public abstract record AssistantPanelContent
{
    // Only the cases of this assembly derive from it.
    private protected AssistantPanelContent()
    {
    }
}

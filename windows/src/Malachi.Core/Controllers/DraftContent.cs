// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Content.draft); GTK:
// ui/internal/assistantpanel/controller.go (ContentDraft).

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>A draft the bridge saved, with Open Draft (<see cref="AssistantPanelController.OpenDraftItem"/>).</summary>
/// <param name="Draft">The draft, as the head of the create_draft result names it.</param>
public sealed record DraftContent(DraftRef Draft) : AssistantPanelContent;

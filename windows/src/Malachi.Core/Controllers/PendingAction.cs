// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Pending.action); GTK:
// ui/internal/assistantpanel/controller.go (PendingAction).

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

/// <summary>Draft a Reply… or Ask About This Message… on the panel's context, waiting for the user's words.</summary>
/// <param name="Action"><see cref="AssistantAction.DraftReply"/> or <see cref="AssistantAction.Ask"/>.</param>
public sealed record PendingAction(AssistantAction Action) : AssistantPanelPending;

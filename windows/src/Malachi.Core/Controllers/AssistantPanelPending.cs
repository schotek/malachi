// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Pending); GTK: ui/internal/assistantpanel/
// controller.go (Pending, PendingKind). Swift's enum with associated values
// is a closed record hierarchy at the namespace's top level (PendingAction,
// PendingAttachment), compared by value as the enum is; Swift's nil (GTK's
// PendingNone) is a null AssistantPanelController.Pending.

namespace Malachi.Core.Controllers;

/// <summary>A message action of the assistant panel that waits for the user's words (<see cref="AssistantPanelController.Pending"/>).</summary>
public abstract record AssistantPanelPending
{
    // Only the cases of this assembly derive from it.
    private protected AssistantPanelPending()
    {
    }
}

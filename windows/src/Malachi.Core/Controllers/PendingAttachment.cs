// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Pending.attachment); GTK:
// ui/internal/assistantpanel/controller.go (PendingAttachment).

namespace Malachi.Core.Controllers;

/// <summary>A question about an attachment, waiting for the user's words. Opaque ids of the API, as strings.</summary>
/// <param name="AccountId">The account id.</param>
/// <param name="MessageId">The message id.</param>
/// <param name="PartId">The attachment's part id.</param>
public sealed record PendingAttachment(string AccountId, string MessageId, string PartId) : AssistantPanelPending;

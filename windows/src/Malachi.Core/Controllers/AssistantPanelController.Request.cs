// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantPanelController.swift
// (AssistantPanelController.Target, Request, Request.Kind); GTK:
// ui/internal/assistantpanel/controller.go (target, request, requestKind).
// Swift's enums with associated values are closed record hierarchies,
// private to the controller as they are internal to Swift's.

using Malachi.Core.Assistants;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantPanelController
{
    /// <summary>
    /// What a message action or an attachment's question is about (Swift
    /// <c>Target</c>): a pinned context, or one the question pins (or adds)
    /// when it is sent.
    /// </summary>
    private abstract record Target;

    /// <summary>The pinned context of <paramref name="Key"/>.</summary>
    private sealed record PinnedTarget(int Key) : Target;

    /// <summary>A context the question pins, or adds, when it is sent.</summary>
    private sealed record ContextTarget(Context Context) : Target;

    /// <summary>What a question is (Swift <c>Request.Kind</c>).</summary>
    private abstract record RequestKind;

    /// <summary>A free question.</summary>
    private sealed record FreeQuestion : RequestKind;

    /// <summary>A message action.</summary>
    private sealed record ActionRequest(AssistantAction Action) : RequestKind;

    /// <summary>Summarize Unread in This Folder.</summary>
    private sealed record UnreadRequest(string AccountId, string FolderId) : RequestKind;

    /// <summary>A question about an attachment.</summary>
    private sealed record AttachmentRequest(string AccountId, string MessageId, string PartId) : RequestKind;

    /// <summary>One question, as sent and as retried (Swift <c>Request</c>).</summary>
    /// <param name="Kind">What it is.</param>
    /// <param name="Label">The label of its user item.</param>
    /// <param name="Text">The words of its user item, sent after the prompt.</param>
    /// <param name="InEffect">
    /// What the chip showed when it was asked: what the conversation is about
    /// when this question is its first.
    /// </param>
    /// <param name="Target">What a message action or an attachment's question is about.</param>
    private sealed record Request(RequestKind Kind, string Label, string Text, Context? InEffect, Target? Target);
}

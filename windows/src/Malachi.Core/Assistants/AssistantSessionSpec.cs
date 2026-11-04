// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider-neutral request snapshot from docs/chatgpt-integration.md §3;
// complements AssistantOptions, ported from the Go/Swift Claude adapter
// (ui/internal/assistantpanel/provider.go SessionSpec,
// macos/Sources/MalachiCore/ChatGPT/AssistantProvider.swift
// AssistantSessionSpec). Swift's tools carry the bridge and its socket; the
// Windows provider has them from its options, so only the allowed tools and
// the bridge's further arguments travel here, checked against ToolPolicy.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Assistants;

/// <summary>Instructions and authority fixed before a child can see input.</summary>
public sealed record AssistantSessionSpec
{
    /// <summary>The default <see cref="Timeout"/> of a turn.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(120);

    /// <summary>The application's instructions, separate from untrusted input.</summary>
    public required string SystemPrompt { get; init; }

    /// <summary>A JSON schema for a one-shot result, empty for ordinary text.</summary>
    public string JsonSchema { get; init; } = "";

    /// <summary>Exactly the tools that may be exposed to this session.</summary>
    public AssistantToolPolicy ToolPolicy { get; init; }

    /// <summary>
    /// The tools the session may expose (with or without the bridge's
    /// <c>mcp__malachi__</c> prefix), all within <see cref="ToolPolicy"/>;
    /// null is all of the policy's (<see cref="Assistant.PolicyTools"/>).
    /// </summary>
    public IReadOnlyList<string>? Tools { get; init; }

    /// <summary>
    /// The bridge's arguments after <c>--socket</c>, as the policy requires
    /// them (<see cref="Assistant.PolicyAllows"/>); empty for the panel.
    /// </summary>
    public IReadOnlyList<string> BridgeArgs { get; init; } = [];

    /// <summary>
    /// The provider's model; "" lets the provider choose: the in-app model
    /// setting, or for a board session (<see cref="BoardConsent"/>) the
    /// provider's default.
    /// </summary>
    public string ModelId { get; init; } = "";

    /// <summary>How long a turn may take before the session ends it.</summary>
    public TimeSpan Timeout { get; init; } = DefaultTimeout;

    /// <summary>
    /// A board session (triage, suggested reply): the provider requires its
    /// board consent (<see cref="IAssistantProvider.HasBoardConsent"/>)
    /// instead of the panel's.
    /// </summary>
    public bool BoardConsent { get; init; }
}

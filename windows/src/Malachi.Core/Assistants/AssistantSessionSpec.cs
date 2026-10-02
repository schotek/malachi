// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider-neutral request snapshot from docs/chatgpt-integration.md §3;
// complements AssistantOptions, ported from the Go/Swift Claude adapter.

namespace Malachi.Core.Assistants;

/// <summary>Instructions and authority fixed before a child can see input.</summary>
public sealed record AssistantSessionSpec
{
    /// <summary>The application's instructions, separate from untrusted input.</summary>
    public required string SystemPrompt { get; init; }

    /// <summary>A JSON schema for a one-shot result, empty for ordinary text.</summary>
    public string JsonSchema { get; init; } = "";

    /// <summary>Exactly the tools that may be exposed to this session.</summary>
    public AssistantToolPolicy ToolPolicy { get; init; }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPanel.swift
// (Assistant.Options); GTK: ui/internal/assistant/claude.go (Options).

using Malachi.Core.Settings;

namespace Malachi.Core.Assistants;

/// <summary>What <see cref="Assistant.Args"/> builds the command line of <c>claude</c> from.</summary>
public sealed record AssistantOptions
{
    /// <summary>
    /// The path of <c>malachi-mcp</c>, which Claude Code starts as its only
    /// MCP server. "" for a one-shot request that reads no mail (the compose
    /// window's rewrite, the search in the user's own words): no MCP server
    /// and no tool, neither <c>--mcp-config</c> nor <c>--allowedTools</c>,
    /// and <see cref="Socket"/> unused.
    /// </summary>
    public required string Bridge { get; init; }

    /// <summary>The daemon's socket, passed to the bridge with <c>--socket</c>; "" for the bridge's default.</summary>
    public string Socket { get; init; } = "";

    /// <summary>The <c>--model</c> alias; any value outside the enum is Sonnet, as <see cref="Assistant.ParseModel"/> reads it.</summary>
    public AssistantModel Model { get; init; } = AssistantModel.Sonnet;

    /// <summary>The whole system prompt (<see cref="Assistant.SystemPrompt"/>, or a one-shot request's).</summary>
    public required string SystemPrompt { get; init; }

    /// <summary>
    /// When set, the answer's shape (<c>--json-schema</c>, after everything
    /// else): the result event's <c>structured_output</c>, as for the search
    /// in the user's own words.
    /// </summary>
    public string JsonSchema { get; init; } = "";
}

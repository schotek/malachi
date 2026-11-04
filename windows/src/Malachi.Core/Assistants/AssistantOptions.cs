// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPanel.swift
// (Assistant.Options); GTK: ui/internal/assistant/claude.go (Options).
// A record compares its lists by reference, so the equality here is by
// value, as Swift's Equatable struct compares its arrays.

using System;
using System.Collections.Generic;
using System.Linq;
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

    /// <summary>
    /// Further arguments of the bridge, after <c>--socket</c>: the board
    /// triage's <see cref="Assistant.TriageBridgeArgs"/> or the suggested
    /// reply's <see cref="Assistant.SuggestReplyBridgeArgs"/>; empty for the
    /// panel. Unused without a bridge.
    /// </summary>
    public IReadOnlyList<string> BridgeArgs { get; init; } = [];

    /// <summary>
    /// The tools Claude Code may run (<c>--allowedTools</c>); null is the
    /// panel's <see cref="Assistant.AllowedTools"/>. Unused without a bridge.
    /// </summary>
    public IReadOnlyList<string>? Tools { get; init; }

    /// <inheritdoc/>
    public bool Equals(AssistantOptions? other) =>
        other is not null
        && string.Equals(Bridge, other.Bridge, StringComparison.Ordinal)
        && string.Equals(Socket, other.Socket, StringComparison.Ordinal)
        && Model == other.Model
        && string.Equals(SystemPrompt, other.SystemPrompt, StringComparison.Ordinal)
        && string.Equals(JsonSchema, other.JsonSchema, StringComparison.Ordinal)
        && BridgeArgs.SequenceEqual(other.BridgeArgs, StringComparer.Ordinal)
        && (Tools is null ? other.Tools is null : other.Tools is not null && Tools.SequenceEqual(other.Tools, StringComparer.Ordinal));

    /// <inheritdoc/>
    public override int GetHashCode()
    {
        var hash = new HashCode();
        hash.Add(Bridge, StringComparer.Ordinal);
        hash.Add(Socket, StringComparer.Ordinal);
        hash.Add(Model);
        hash.Add(SystemPrompt, StringComparer.Ordinal);
        hash.Add(JsonSchema, StringComparer.Ordinal);
        foreach (var a in BridgeArgs)
        {
            hash.Add(a, StringComparer.Ordinal);
        }
        hash.Add(Tools is null);
        foreach (var t in Tools ?? [])
        {
            hash.Add(t, StringComparer.Ordinal);
        }
        return hash.ToHashCode();
    }
}

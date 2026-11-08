// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPanel.swift
// (Assistant.Options); GTK: ui/internal/assistant/claude.go (Options).
// A record compares its lists by reference, so the equality here is by
// value, as Swift's Equatable struct compares its arrays (the prompt's
// members as the daemon wrote them).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Assistants;

/// <summary>What <see cref="Assistant.Args"/> builds the command line of <c>claude</c> from.</summary>
public sealed record AssistantOptions
{
    /// <summary>
    /// The path of <c>malachi-mcp</c>, which Claude Code starts as its only
    /// MCP server. "" for a one-shot request that reads no mail (the compose
    /// window's rewrite and writing bar, the search in the user's own
    /// words): no MCP server and no tool, neither <c>--mcp-config</c> nor
    /// <c>--allowedTools</c>, and <see cref="Socket"/> unused.
    /// </summary>
    public required string Bridge { get; init; }

    /// <summary>The daemon's socket, passed to the bridge with <c>--socket</c>; "" for the bridge's default.</summary>
    public string Socket { get; init; } = "";

    /// <summary>The <c>--model</c> alias; any value outside the enum is Sonnet, as <see cref="Assistant.ParseModel"/> reads it.</summary>
    public AssistantModel Model { get; init; } = AssistantModel.Sonnet;

    /// <summary>
    /// What the daemon built for the purpose (assistant.prompt, docs/api.md
    /// §4.18): its <see cref="PromptResult.SystemPrompt"/> is
    /// <c>--system-prompt</c>, its <see cref="PromptResult.Tools"/> (bare
    /// names, <see cref="Assistant.PrefixTools"/>) <c>--allowedTools</c>, its
    /// <see cref="PromptResult.BridgeArgs"/> (which start with
    /// <c>--in-app</c>) the bridge's arguments after <c>--socket</c>, and its
    /// schema, when set, <c>--json-schema</c> (after everything else): the
    /// result event's <c>structured_output</c>. The tools and the bridge's
    /// arguments are unused without a bridge.
    /// </summary>
    public required PromptResult Prompt { get; init; }

    /// <inheritdoc/>
    public bool Equals(AssistantOptions? other) =>
        other is not null
        && string.Equals(Bridge, other.Bridge, StringComparison.Ordinal)
        && string.Equals(Socket, other.Socket, StringComparison.Ordinal)
        && Model == other.Model
        && SamePrompt(Prompt, other.Prompt);

    /// <inheritdoc/>
    public override int GetHashCode()
    {
        var hash = new HashCode();
        hash.Add(Bridge, StringComparer.Ordinal);
        hash.Add(Socket, StringComparer.Ordinal);
        hash.Add(Model);
        hash.Add(Prompt.SystemPrompt, StringComparer.Ordinal);
        hash.Add(Prompt.Message ?? "", StringComparer.Ordinal);
        hash.Add(Prompt.JsonSchemaText, StringComparer.Ordinal);
        foreach (var a in Prompt.BridgeArgs)
        {
            hash.Add(a, StringComparer.Ordinal);
        }
        foreach (var t in Prompt.Tools)
        {
            hash.Add(t, StringComparer.Ordinal);
        }
        return hash.ToHashCode();
    }

    // The members of two prompts, by value.
    private static bool SamePrompt(PromptResult a, PromptResult b) =>
        string.Equals(a.SystemPrompt, b.SystemPrompt, StringComparison.Ordinal)
        && string.Equals(a.Message, b.Message, StringComparison.Ordinal)
        && string.Equals(a.JsonSchemaText, b.JsonSchemaText, StringComparison.Ordinal)
        && a.BridgeArgs.SequenceEqual(b.BridgeArgs, StringComparer.Ordinal)
        && a.Tools.SequenceEqual(b.Tools, StringComparer.Ordinal);
}

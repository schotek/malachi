// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest.Tools); GTK: ui/internal/assistantpanel/oneshot.go
// (Tools). Windows adds Policy, the authority a provider session checks the
// tools and the bridge's arguments against (Assistant.PolicyAllows); Claude
// Code itself is limited by Allowed. A record compares its lists by
// reference, so the equality here is by value, as Swift's Equatable struct.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Assistants;
using Malachi.Core.Settings;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    /// <summary>The bridge a request gives Claude Code, and what of it may run.</summary>
    public sealed record Tools
    {
        /// <summary>The path of <c>malachi-mcp</c> (a provider uses its own).</summary>
        public required string Bridge { get; init; }

        /// <summary>The daemon's socket (<c>--socket</c>); "" for the bridge's default (a provider uses its own).</summary>
        public string Socket { get; init; } = "";

        /// <summary>The bridge's further arguments (<see cref="Assistant.TriageBridgeArgs"/>, <see cref="Assistant.SuggestReplyBridgeArgs"/>).</summary>
        public IReadOnlyList<string> BridgeArgs { get; init; } = [];

        /// <summary><c>--allowedTools</c> (<see cref="Assistant.TriageTools"/>, <see cref="Assistant.SuggestReplyTools"/>).</summary>
        public required IReadOnlyList<string> Allowed { get; init; }

        /// <summary>
        /// Windows: the authority of a provider session the tools must fit in
        /// (<see cref="AssistantToolPolicy.Triage"/> and so on); the default,
        /// none, makes a provider refuse any tool.
        /// </summary>
        public AssistantToolPolicy Policy { get; init; } = AssistantToolPolicy.None;

        /// <inheritdoc/>
        public bool Equals(Tools? other) =>
            other is not null
            && string.Equals(Bridge, other.Bridge, StringComparison.Ordinal)
            && string.Equals(Socket, other.Socket, StringComparison.Ordinal)
            && BridgeArgs.SequenceEqual(other.BridgeArgs, StringComparer.Ordinal)
            && Allowed.SequenceEqual(other.Allowed, StringComparer.Ordinal)
            && Policy == other.Policy;

        /// <inheritdoc/>
        public override int GetHashCode()
        {
            var hash = new HashCode();
            hash.Add(Bridge, StringComparer.Ordinal);
            hash.Add(Socket, StringComparer.Ordinal);
            foreach (var a in BridgeArgs)
            {
                hash.Add(a, StringComparer.Ordinal);
            }
            foreach (var t in Allowed)
            {
                hash.Add(t, StringComparer.Ordinal);
            }
            hash.Add(Policy);
            return hash.ToHashCode();
        }
    }

    // What one Start asked for.
    private sealed record Call(
        string SystemPrompt,
        string Message,
        string JsonSchema,
        Tools? Tools,
        TimeSpan Timeout,
        AssistantModel Model,
        Action<string>? OnText,
        Action<AssistantEvent>? OnTool,
        Action<AssistantEvent>? OnUsage,
        Action<Outcome> Completion);
}

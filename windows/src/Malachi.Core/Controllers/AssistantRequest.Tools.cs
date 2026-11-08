// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/AssistantRequest.swift
// (AssistantRequest.Tools, AssistantRequest.Call: CallSpec here, as Call is
// a keyword of Visual Basic, CA1716); GTK:
// ui/internal/assistantpanel/oneshot.go (Tools, Call). Windows adds Policy,
// the authority a provider session checks the daemon's tools and bridge
// arguments against (Assistant.PolicyAllows); Claude Code itself is limited
// by the daemon's tools. A record compares its lists by reference, so the
// equality of Tools is by value, as Swift's Equatable struct.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Settings;
using AssistantEvent = Malachi.Core.Assistants.AssistantEvent;

namespace Malachi.Core.Controllers;

public sealed partial class AssistantRequest
{
    /// <summary>
    /// assistantpanel.Tools: the bridge a request gives the assistant, and
    /// what of it may run. A caller sets <see cref="Bridge"/>,
    /// <see cref="Socket"/> and <see cref="Policy"/>; the bridge's arguments
    /// and the tools are the daemon's prompt of the purpose.
    /// </summary>
    public sealed record Tools
    {
        /// <summary>The path of <c>malachi-mcp</c> (a provider uses its own).</summary>
        public required string Bridge { get; init; }

        /// <summary>The daemon's socket (<c>--socket</c>); "" for the bridge's default (a provider uses its own).</summary>
        public string Socket { get; init; } = "";

        /// <summary>
        /// The bridge's further arguments after <c>--socket</c>; unused by a
        /// request, which starts the bridge with the daemon's
        /// (<see cref="PromptResult.BridgeArgs"/>, <c>--in-app</c> first).
        /// </summary>
        public IReadOnlyList<string> BridgeArgs { get; init; } = [];

        /// <summary>
        /// <c>--allowedTools</c>; unused by a request, which allows the
        /// daemon's (<see cref="Assistant.PrefixTools"/> of
        /// <see cref="PromptResult.Tools"/>).
        /// </summary>
        public IReadOnlyList<string> Allowed { get; init; } = [];

        /// <summary>
        /// Windows: the authority of a provider session the daemon's tools
        /// must fit in (<see cref="AssistantToolPolicy.Triage"/> and so on);
        /// the default, none, makes a provider refuse any tool.
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

    /// <summary>assistantpanel.Call: what one <see cref="StartCall"/> asks for.</summary>
    public sealed record CallSpec
    {
        /// <summary>What the daemon builds the prompt for.</summary>
        public required PromptPurpose Purpose { get; init; }

        /// <summary>The turn's input (exactly the member of the purpose set).</summary>
        public required PromptInput Input { get; init; }

        /// <summary>
        /// The account whose profile note is appended (the compose window's
        /// account for a rewrite or the writing bar, the case's for a
        /// suggested reply); null for none.
        /// </summary>
        public AccountId? NoteAccountId { get; init; }

        /// <summary>A triage's trigger; null outside one.</summary>
        public BoardTrigger? Trigger { get; init; }

        /// <summary>A triage's board run (<c>board.runStart</c>); "" outside one.</summary>
        public string RunId { get; init; } = "";

        /// <summary>Overrides the selected provider's model for this call; "" for none.</summary>
        public string ModelId { get; init; } = "";

        /// <summary>Gives the assistant the bridge (its path and socket); null: no tool at all.</summary>
        public Tools? Tools { get; init; }

        /// <summary>Replaces the request's own timeout for this call.</summary>
        public TimeSpan? Timeout { get; init; }

        /// <summary>Replaces the <c>assistant-model</c> setting when set (read at the start).</summary>
        public AssistantModel? Model { get; init; }

        /// <summary>Gets the answer's text as it streams, all of it so far.</summary>
        public Action<string>? OnText { get; init; }

        /// <summary>Gets every tool call and tool result; it may cancel the request.</summary>
        public Action<AssistantEvent>? OnTool { get; init; }

        /// <summary>Gets every event that carries usage, before the event is handled.</summary>
        public Action<AssistantEvent>? OnUsage { get; init; }
    }

    // What one StartCall asked for, its bound and model resolved.
    private sealed record Job(CallSpec Call, TimeSpan Timeout, AssistantModel Model, Action<Outcome> Completion);
}

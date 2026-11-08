// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider-neutral request snapshot from docs/chatgpt-integration.md §3;
// complements AssistantOptions, ported from the Go/Swift Claude adapter
// (ui/internal/assistantpanel/provider.go SessionSpec,
// macos/Sources/MalachiCore/ChatGPT/AssistantProvider.swift
// AssistantSessionSpec). Swift's tools carry the bridge and its socket; the
// Windows provider has them from its options, so only the allowed tools and
// the bridge's further arguments travel here, checked against ToolPolicy.
// Purpose, Language, NoteAccountId, Trigger, RunId, BridgePath and Input
// are those of the Swift spec for a session the daemon runs over an ai
// account (docs/api.md §4.18, assistant.sessionStart); a provider that runs
// the model itself takes SystemPrompt, JsonSchema and the tools instead.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Assistants;

/// <summary>Instructions and authority fixed before a child can see input.</summary>
public sealed record AssistantSessionSpec
{
    /// <summary>The default <see cref="Timeout"/> of a turn.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(120);

    /// <summary>What the session is for (the daemon's sessions; <see cref="PromptPurpose.Panel"/> by default).</summary>
    public PromptPurpose Purpose { get; init; } = PromptPurpose.Panel;

    /// <summary>The UI language (BCP 47, "" = English) the model answers in.</summary>
    public string Language { get; init; } = "";

    /// <summary>
    /// The mail or Jira account whose profile note a one-shot purpose of a
    /// compose window appends; null for none.
    /// </summary>
    public AccountId? NoteAccountId { get; init; }

    /// <summary>The triage run's trigger; null outside a triage.</summary>
    public BoardTrigger? Trigger { get; init; }

    /// <summary>The triage run's board run (<c>board.runStart</c>); "" outside a triage.</summary>
    public string RunId { get; init; } = "";

    /// <summary>The bundled bridge's absolute path ("" without one), which the daemon starts for the tools.</summary>
    public string BridgePath { get; init; } = "";

    /// <summary>
    /// The one turn of a one-shot purpose, given with the start: the
    /// daemon's <c>assistant.sessionStart</c> takes it there and sends it at
    /// once (a one-shot purpose cannot start without it), and its session
    /// then takes the request's <see cref="IAssistantSession.SubmitAsync"/>
    /// of the same input as already sent. A provider that takes its turns by
    /// <see cref="IAssistantSession.SubmitAsync"/> alone (Codex) ignores it.
    /// Null for the panel.
    /// </summary>
    public PromptInput? Input { get; init; }

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
    /// The bridge's arguments after <c>--socket</c> as the daemon's prompt
    /// gives them (<see cref="PromptResult.BridgeArgs"/>, <c>--in-app</c>
    /// first), as the policy requires them (<see cref="Assistant.PolicyAllows"/>);
    /// <c>--in-app</c> alone, or empty, for the panel.
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

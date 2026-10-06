// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantEvents.swift
// (Assistant.Event, Equatable); GTK: ui/internal/assistant/events.go
// (Event). A record compares its lists and arrays by reference, so the
// equality here is by value, as Swift's arrays and Data compare: the tools,
// the denials and the bytes of the structured output (the usage and its
// message id compared as well, as Swift's). The texts are mail
// content and model output: never logged.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Assistants;

/// <summary>
/// One thing the assistant panel reacts to, read from a stream-json line of
/// Claude Code (<see cref="Assistant.ParseEvents"/>); only the members of its
/// <see cref="Kind"/> are set.
/// </summary>
public sealed record AssistantEvent
{
    /// <summary>An event of <paramref name="kind"/> with every other member at its zero value.</summary>
    public AssistantEvent(AssistantEventKind kind)
    {
        Kind = kind;
    }

    /// <summary>What the event is.</summary>
    public AssistantEventKind Kind { get; init; }

    /// <summary>SystemInit: whether the MCP server "malachi" reported "connected".</summary>
    public bool BridgeConnected { get; init; }

    /// <summary>SystemInit: the names of the tools Claude Code offers, as reported.</summary>
    public IReadOnlyList<string> Tools { get; init; } = [];

    /// <summary>TextDelta and Text: the text; Failure: Claude Code's own words, its message's text blocks joined with "\n".</summary>
    public string Text { get; init; } = "";

    /// <summary>ToolUse: the tool without the <c>mcp__malachi__</c> prefix (another tool keeps its name).</summary>
    public string Tool { get; init; } = "";

    /// <summary>ToolUse: the call's id; ToolResult: the id of the call it answers.</summary>
    public string ToolUseId { get; init; } = "";

    /// <summary>ToolResult: whether the tool failed; Result: <c>is_error</c>.</summary>
    public bool IsError { get; init; }

    /// <summary>
    /// ToolResult: its text blocks joined with "\n" (or its string content);
    /// Result: the turn's result text, and for a failure without one its
    /// subtype ("error_max_turns", …).
    /// </summary>
    public string ResultText { get; init; } = "";

    /// <summary>Result: subtype "success" and not <c>is_error</c>.</summary>
    public bool Success { get; init; }

    /// <summary>Result: the tools the permission mode denied (prefix stripped), in order.</summary>
    public IReadOnlyList<string> Denied { get; init; } = [];

    /// <summary>Result: the cost as Claude Code reports it (logged, never shown).</summary>
    public double CostUsd { get; init; }

    /// <summary>Result: <c>structured_output</c> as the raw JSON of the line; null when absent or null.</summary>
    public byte[]? Structured { get; init; }

    /// <summary>
    /// Failure: what Claude Code calls the failure, its message's
    /// <c>error</c> ("authentication_failed", "rate_limit", …).
    /// </summary>
    public string Failure { get; init; } = "";

    /// <summary>
    /// Result: the run's usage as the result reports it. The first event of
    /// an <c>assistant</c> message (Text, ToolUse, or an Other standing for a
    /// message that yields no other event): the usage of that API message,
    /// its id in <see cref="MessageId"/>, only for a message of the main loop
    /// (<c>parent_tool_use_id</c> null or absent) with a non-empty id. Null
    /// when absent, or when a counter is not a whole number from 0 to
    /// <see cref="long.MaxValue"/> (<see cref="AssistantUsageTally"/> adds them up).
    /// </summary>
    public AssistantUsage? Usage { get; init; }

    /// <summary>The id of the API message whose <see cref="Usage"/> the event carries; "" otherwise.</summary>
    public string MessageId { get; init; } = "";

    /// <summary>
    /// Event.NotSignedIn: whether the event is the failure of a turn for
    /// want of a sign-in the API accepts: Claude Code is signed out, or its
    /// sign-in has expired or was revoked (<c>claude auth status</c> may
    /// still say loggedIn then).
    /// </summary>
    public bool NotSignedIn => Kind == AssistantEventKind.Failure && Failure == Assistant.AuthenticationFailed;

    /// <summary>
    /// Event.RefreshFailed: whether the event is the failure of a turn whose
    /// sign-in Claude Code could not refresh just then (another Claude Code
    /// was refreshing it, or ended in the middle of that): trying again in a
    /// minute may work, signing in again works now.
    /// <see cref="NotSignedIn"/> takes precedence; the words are compared
    /// ordinally.
    /// </summary>
    public bool RefreshFailed =>
        Kind == AssistantEventKind.Failure
        && !NotSignedIn
        && Text.StartsWith(Assistant.RefreshFailedPrefix, StringComparison.Ordinal);

    /// <inheritdoc/>
    public bool Equals(AssistantEvent? other) =>
        other is not null
        && Kind == other.Kind
        && BridgeConnected == other.BridgeConnected
        && Tools.SequenceEqual(other.Tools, StringComparer.Ordinal)
        && string.Equals(Text, other.Text, StringComparison.Ordinal)
        && string.Equals(Tool, other.Tool, StringComparison.Ordinal)
        && string.Equals(ToolUseId, other.ToolUseId, StringComparison.Ordinal)
        && IsError == other.IsError
        && string.Equals(ResultText, other.ResultText, StringComparison.Ordinal)
        && Success == other.Success
        && Denied.SequenceEqual(other.Denied, StringComparer.Ordinal)
        && CostUsd.Equals(other.CostUsd)
        && (Structured is null
            ? other.Structured is null
            : other.Structured is not null && Structured.AsSpan().SequenceEqual(other.Structured))
        && string.Equals(Failure, other.Failure, StringComparison.Ordinal)
        && Usage == other.Usage
        && string.Equals(MessageId, other.MessageId, StringComparison.Ordinal);

    /// <inheritdoc/>
    public override int GetHashCode()
    {
        var hash = new HashCode();
        hash.Add(Kind);
        hash.Add(BridgeConnected);
        foreach (var t in Tools)
        {
            hash.Add(t, StringComparer.Ordinal);
        }
        hash.Add(Text, StringComparer.Ordinal);
        hash.Add(Tool, StringComparer.Ordinal);
        hash.Add(ToolUseId, StringComparer.Ordinal);
        hash.Add(IsError);
        hash.Add(ResultText, StringComparer.Ordinal);
        hash.Add(Success);
        foreach (var d in Denied)
        {
            hash.Add(d, StringComparer.Ordinal);
        }
        hash.Add(CostUsd);
        hash.Add(Structured is null);
        if (Structured is not null)
        {
            hash.AddBytes(Structured);
        }
        hash.Add(Failure, StringComparer.Ordinal);
        hash.Add(Usage);
        hash.Add(MessageId, StringComparer.Ordinal);
        return hash.ToHashCode();
    }
}

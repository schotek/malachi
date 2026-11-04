// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantEvents.swift
// (Assistant.Event.Kind); GTK: ui/internal/assistant/events.go (EventKind).

namespace Malachi.Core.Assistants;

/// <summary>What an <see cref="AssistantEvent"/> is.</summary>
public enum AssistantEventKind
{
    /// <summary>EventOther: anything else (status, rate limits, a delta that is not text, a type this client does not know).</summary>
    Other,

    /// <summary>EventInit: <c>system/init</c>, at the start of every turn.</summary>
    SystemInit,

    /// <summary>EventTextDelta: a piece of the answer as it streams.</summary>
    TextDelta,

    /// <summary>EventText: one whole text block of an <c>assistant</c> message, authoritative over the deltas that preceded it.</summary>
    Text,

    /// <summary>EventToolUse: a tool is called.</summary>
    ToolUse,

    /// <summary>EventToolResult: a tool answered.</summary>
    ToolResult,

    /// <summary>EventResult: the turn is over.</summary>
    Result,

    /// <summary>
    /// EventFailure: an <c>assistant</c> message Claude Code wrote itself,
    /// because the API refused the turn. Its text is no answer; the result
    /// that follows repeats it.
    /// </summary>
    Failure,
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantUsage.swift
// (Assistant.Usage); GTK: ui/internal/assistant/events.go (Usage). What a
// run of Claude Code used, from the usage its events carry. This file holds
// no translatable text.

namespace Malachi.Core.Assistants;

/// <summary>
/// What Claude Code reports an API message, or a run, used, in tokens
/// (assistant.Usage). A counter missing or null in the line reads as 0.
/// </summary>
/// <param name="InputTokens">input_tokens.</param>
/// <param name="OutputTokens">output_tokens.</param>
/// <param name="CacheCreationInputTokens">cache_creation_input_tokens.</param>
/// <param name="CacheReadInputTokens">cache_read_input_tokens.</param>
public readonly record struct AssistantUsage(
    long InputTokens = 0, long OutputTokens = 0, long CacheCreationInputTokens = 0, long CacheReadInputTokens = 0)
{
    /// <summary>All four counters are 0.</summary>
    internal bool IsZero => this == default;
}

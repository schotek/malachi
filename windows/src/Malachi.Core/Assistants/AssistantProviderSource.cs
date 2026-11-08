// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/api.go (ProviderSource, SourceClaude,
// SourceChatGPT, SourceAccount); macOS:
// macos/Sources/MalachiCore/Assistant/AssistantAPI.swift
// (Assistant.ProviderSource). It replaces the enum AssistantProviderID: the
// setting assistant-provider is a string (docs/plans/ai-api.md D14), and
// this is which of its three forms a value is (Assistant.SourceOf).

namespace Malachi.Core.Assistants;

/// <summary>Which of the three forms a provider setting is (assistant.ProviderSource).</summary>
public enum AssistantProviderSource
{
    /// <summary>The user's Claude Code (<see cref="Assistant.ProviderClaude"/>).</summary>
    Claude,

    /// <summary>Codex with the user's ChatGPT plan (<see cref="Assistant.ProviderChatGpt"/>).</summary>
    ChatGpt,

    /// <summary>An AI provider account of the daemon (kind <c>ai</c>), over which the daemon runs the assistant.</summary>
    Account,
}

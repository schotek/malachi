// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Provider identity from docs/chatgpt-integration.md §3. Windows-only first
// implementation; the existing Go/Swift assistant remains the Claude reference.

namespace Malachi.Core.Assistants;

/// <summary>The account/runtime used in the app, independent of its target.</summary>
public enum AssistantProviderID
{
    /// <summary>The existing Claude Code adapter.</summary>
    Claude,
    /// <summary>ChatGPT plan usage through an isolated Codex App Server.</summary>
    ChatGpt,
}

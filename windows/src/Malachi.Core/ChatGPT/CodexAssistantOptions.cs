// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Codex platform injection from docs/chatgpt-integration.md §5–6. Mirrors
// the services injected into the Go/Swift Claude process, without OS APIs.

using System;
using System.Collections.Generic;
using System.Net.Http;

namespace Malachi.Core.ChatGPT;

/// <summary>Application-owned paths and non-secret settings of the runtime.</summary>
public sealed record CodexAssistantOptions
{
    /// <summary>The absolute native executable, never a shell command.</summary>
    public required Func<string?> Executable { get; init; }

    /// <summary>The absolute bundled bridge; unused for tool-free requests.</summary>
    public required string Bridge { get; init; }

    /// <summary>The daemon socket passed only to the bridge.</summary>
    public required string Socket { get; init; }

    /// <summary>An app-owned private root; each session gets a fresh directory.</summary>
    public required string Directory { get; init; }

    /// <summary>Parent environment; an explicit allow list filters every child.</summary>
    public required IReadOnlyDictionary<string, string> Environment { get; init; }

    /// <summary>Model selection, read once at session start.</summary>
    public required Func<string> Model { get; init; }

    /// <summary>Whether version 1 consent for sending data to OpenAI was accepted.</summary>
    public required Func<bool> HasConsent { get; init; }

    /// <summary>Injected network transport for isolated protocol tests; null uses HTTPS.</summary>
    public Func<HttpMessageHandler>? InferenceHandler { get; init; }

    /// <summary>Persists version 1 consent separately from Claude's.</summary>
    public required Action AcceptConsent { get; init; }

    /// <summary>
    /// Whether version 1 of the board's consent for sending board mail to
    /// OpenAI was accepted (<c>board-triage-chatgpt-consent-version</c>);
    /// never without it.
    /// </summary>
    public Func<bool>? HasBoardConsent { get; init; }

    /// <summary>Persists version 1 of the board's consent; nothing without it.</summary>
    public Action? AcceptBoardConsent { get; init; }

    /// <summary>
    /// Whether the ChatGPT account is connected; without it, whether the
    /// token source is a connection service whose connection is.
    /// </summary>
    public Func<bool>? Connected { get; init; }
}

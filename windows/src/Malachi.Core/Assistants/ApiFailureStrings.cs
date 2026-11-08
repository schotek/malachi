// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantAPI.swift
// (Assistant.APIFailureStrings); GTK: ui/internal/assistant/apitext.go
// (APIFailureStrings). The sentences of a daemon session's failures, one for
// each api.AssistantFailureClass; Assistant.ApiFailureTexts fills it.

namespace Malachi.Core.Assistants;

/// <summary>
/// The sentences of the failures of a session the daemon runs over an ai
/// account (<see cref="Assistant.ApiFailureTexts"/>), shown where a
/// provider's failure is shown, never the daemon's technical message.
/// </summary>
public sealed record ApiFailureStrings
{
    /// <summary>The account's consent was not given.</summary>
    public required string ConsentRequired { get; init; }

    /// <summary>The key was refused.</summary>
    public required string Auth { get; init; }

    /// <summary>The key may not use this.</summary>
    public required string Permission { get; init; }

    /// <summary>Too many requests.</summary>
    public required string RateLimit { get; init; }

    /// <summary>The provider is busy.</summary>
    public required string Overloaded { get; init; }

    /// <summary>The account at the provider has no credit or used up its quota.</summary>
    public required string Quota { get; init; }

    /// <summary>The server has no such model or could not load it.</summary>
    public required string Model { get; init; }

    /// <summary>The provider refused the request.</summary>
    public required string InvalidRequest { get; init; }

    /// <summary>The model cannot call tools.</summary>
    public required string ToolsUnsupported { get; init; }

    /// <summary>The Malachi Mail tools could not be started.</summary>
    public required string Bridge { get; init; }

    /// <summary>No connection to the provider.</summary>
    public required string Network { get; init; }

    /// <summary>The answer could not be read.</summary>
    public required string Protocol { get; init; }

    /// <summary>The conversation grew too long.</summary>
    public required string ContextFull { get; init; }

    /// <summary>No answer in time.</summary>
    public required string Timeout { get; init; }

    /// <summary>Too many tool calls in one turn.</summary>
    public required string Limit { get; init; }

    /// <summary>The session ended (stopped, the connection closed).</summary>
    public required string Stopped { get; init; }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/Assistant.swift
// (Assistant.RestartStrings); GTK: ui/internal/assistant/assistant.go
// (RestartStrings).

namespace Malachi.Core.Assistants;

/// <summary>
/// The texts of the offer to restart Claude Desktop around a change of
/// "Register with Claude" (<see cref="Assistant.RestartTexts"/>). Claude
/// Desktop reads its MCP servers only when it starts and, while it runs,
/// rewrites its configuration file from memory, so an entry the bridge
/// writes or removes meanwhile is undone (docs/mcp.md): the switch asks,
/// quits Claude Desktop, writes the change and starts it again, or keeps the
/// change pending until Claude Desktop has quit.
/// </summary>
public sealed record RestartStrings
{
    /// <summary>The question's heading.</summary>
    public required string Heading { get; init; }

    /// <summary>The question's text.</summary>
    public required string Body { get; init; }

    /// <summary>The question's default button.</summary>
    public required string Restart { get; init; }

    /// <summary>The button that writes the change without the restart.</summary>
    public required string Later { get; init; }

    /// <summary>
    /// The subtitle of the row under the switch while Claude Desktop has not
    /// picked up the change (its title is <c>TargetName(Desktop)</c>).
    /// </summary>
    public required string Pending { get; init; }

    /// <summary>The button of that row.</summary>
    public required string RestartNow { get; init; }

    /// <summary>The toast when Claude Desktop did not quit in time; the change is written anyway and stays pending.</summary>
    public required string NotQuit { get; init; }
}

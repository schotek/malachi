// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MCPRegistrationController.swift
// (MCPClient); GTK: ui/internal/mcpsetup/mcpsetup.go (Client); the
// contract is the --json report of docs/mcp.md ("Claude Desktop and Claude
// Code").

using System.Text.Json.Serialization;

namespace Malachi.Core.Controllers;

/// <summary>
/// One MCP client the bridge knows about (<c>malachi-mcp status --json</c>):
/// Claude Desktop or Claude Code, whether it is installed on this computer,
/// whether Malachi Mail is in its MCP configuration, that configuration
/// file, and the command registered there when it is not this bridge.
/// Unknown keys are ignored, so a newer bridge still decodes.
/// </summary>
public sealed record McpClient
{
    /// <summary>The stable identifier: <c>claude-desktop</c> or <c>claude-code</c>.</summary>
    [JsonPropertyName("id")]
    public required string Id { get; init; }

    /// <summary>The app's display name as the bridge reports it.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Whether the app exists on this computer.</summary>
    [JsonPropertyName("present")]
    public required bool Present { get; init; }

    /// <summary>Whether this bridge is listed in its configuration.</summary>
    [JsonPropertyName("registered")]
    public required bool Registered { get; init; }

    /// <summary>The configuration file the bridge reads and writes for this app.</summary>
    [JsonPropertyName("path")]
    public string? Path { get; init; }

    /// <summary>A malachi entry the bridge left alone because it names another executable.</summary>
    [JsonPropertyName("other")]
    public string? Other { get; init; }
}

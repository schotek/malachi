// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MCPRegistrationController.swift
// (MCPStatus, isRegistered); GTK: ui/internal/mcpsetup/mcpsetup.go (Status,
// Registered, Client, RegisteredIn, ClientDesktop, ClientCode); the
// contract is the --json report of docs/mcp.md. Swift
// decodes it with JSONDecoder; here McpJsonContext does, generated, as the
// API's own context does (docs/windows-port.md §3.1).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Text.Json.Serialization;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// What <c>malachi-mcp status</c>, <c>install</c> and <c>uninstall</c>
/// print with <c>--json</c>: the command the clients are (or would be)
/// registered with, and the clients. Compared by value, as Swift's
/// Equatable does.
/// </summary>
public sealed record McpStatus
{
    /// <summary>
    /// mcpsetup.ClientDesktop: the id of Claude Desktop in the report
    /// (<see cref="McpClient.Id"/>), which <c>--client</c> of install and
    /// uninstall takes.
    /// </summary>
    public const string ClientDesktop = "claude-desktop";

    /// <summary>mcpsetup.ClientCode: the id of Claude Code in the report.</summary>
    public const string ClientCode = "claude-code";

    /// <summary>The command written into the configurations (<c>--command</c>, else the bridge itself).</summary>
    [JsonPropertyName("command")]
    public required string Command { get; init; }

    /// <summary>Every client the bridge knows about, present or not; null reads as empty.</summary>
    [JsonPropertyName("clients")]
    [JsonConverter(typeof(NullAsEmptyListConverter<McpClient>))]
    public IReadOnlyList<McpClient> Clients { get; init => field = value ?? []; } = [];

    /// <summary>
    /// Registered with at least one client (mcpsetup <c>Registered</c>): what
    /// the Assistant as a whole needs (assistant.Shown).
    /// </summary>
    [JsonIgnore]
    public bool IsRegistered => Clients.Any(c => c.Registered);

    /// <summary>
    /// mcpsetup <c>Status.Client</c>: the report's entry for client
    /// <paramref name="id"/> (<see cref="ClientDesktop"/>,
    /// <see cref="ClientCode"/>); null when the report has none.
    /// </summary>
    public McpClient? Client(string id) => Clients.FirstOrDefault(c => c.Id == id);

    /// <summary>
    /// mcpsetup <c>Status.RegisteredIn</c>: whether the bridge is registered
    /// with client <paramref name="id"/>: what that app's Register switch
    /// and its row of Preferences → Integrations show.
    /// </summary>
    public bool RegisteredIn(string id) => Client(id)?.Registered ?? false;

    /// <summary>
    /// The report in <paramref name="json"/>, or a
    /// <see cref="JsonException"/> for anything that is not one.
    /// </summary>
    public static McpStatus Decode(ReadOnlySpan<byte> json) =>
        JsonSerializer.Deserialize(json, McpJsonContext.Default.McpStatus) ?? throw new JsonException("the report is null");

    /// <summary>Whether both name the same command and the same clients, in order.</summary>
    public bool Equals(McpStatus? other) =>
        other is not null && Command == other.Command && Clients.SequenceEqual(other.Clients);

    /// <inheritdoc/>
    public override int GetHashCode() => HashCode.Combine(Command, Clients.Count);
}

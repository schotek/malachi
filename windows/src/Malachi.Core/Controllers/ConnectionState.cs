// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ConnectionController.swift
// (ConnectionController.ConnectionState); GTK: ui/internal/window/connection.go
// (how an attempt ends) and status.go (connView). Swift's enum with
// associated values is a closed record hierarchy, compared by value as the
// Swift enum is.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// Where the connection to malachid stands (Swift
/// <c>ConnectionController.ConnectionState</c>): an attempt ends connected,
/// as a protocol mismatch or as unavailable.
/// </summary>
public abstract record ConnectionState
{
    // Only the cases below derive from it.
    private ConnectionState()
    {
    }

    /// <summary>An attempt is underway, or connected and <c>system.info</c> has not answered yet.</summary>
    public sealed record Connecting : ConnectionState;

    /// <summary>Connected, and <c>system.info</c> answered with <paramref name="Info"/>.</summary>
    /// <param name="Info">The daemon's version, protocol, pid and store.</param>
    public sealed record Connected(SystemInfoResult Info) : ConnectionState;

    /// <summary>
    /// The daemon speaks another protocol version (1 for one without a
    /// handshake): the handshake refused it, so there is no connection and
    /// nothing is loaded; <c>system.info</c> disagreeing on an authenticated
    /// connection ends here too, as a defence.
    /// </summary>
    /// <param name="Daemon">The daemon's protocol version.</param>
    public sealed record ProtocolMismatch(int Daemon) : ConnectionState;

    /// <summary>Connected, but <c>system.info</c> failed for <paramref name="Reason"/>.</summary>
    /// <param name="Reason">A short description of the failure (never shown as it is).</param>
    public sealed record InfoFailed(string Reason) : ConnectionState;

    /// <summary>No daemon to use, for <paramref name="Reason"/>.</summary>
    /// <param name="Reason">A short description of why (never shown as it is).</param>
    public sealed record Unavailable(string Reason) : ConnectionState;

    /// <summary>The application is quitting; the state stays.</summary>
    public sealed record Stopping : ConnectionState;
}

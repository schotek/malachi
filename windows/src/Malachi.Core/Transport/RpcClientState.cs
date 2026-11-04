// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift
// (RPCClient.State); GTK: ui/internal/client/client.go (State and the
// error of OnStateChange).

namespace Malachi.Core.Transport;

/// <summary>
/// The connection state of an <see cref="RpcClient"/> (Swift
/// <c>RPCClient.State</c>): compared by value, as the Swift enum is.
/// </summary>
public abstract record RpcClientState
{
    // Only the cases below derive from it.
    private RpcClientState()
    {
    }

    /// <summary>No connection; <paramref name="Reason"/> says why, null after <see cref="RpcClient.Close"/>.</summary>
    /// <param name="Reason">The dial's, the handshake's or the connection's end as text; null when closed on purpose.</param>
    public sealed record Disconnected(string? Reason) : RpcClientState;

    /// <summary>Dialled; the handshake runs, and calls are refused until it succeeds.</summary>
    public sealed record Connecting : RpcClientState;

    /// <summary>Authenticated: calls go out.</summary>
    public sealed record Connected : RpcClientState;
}

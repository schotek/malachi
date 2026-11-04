// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift
// (RPCClient.ClientError); GTK: ui/internal/client/client.go
// (ErrDisconnected).
//
// Swift's enum is a value that is also thrown; in C# the value is this
// record, compared as Swift compares the enum, and RpcClientException
// carries it.

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// Why a call failed without an answer from the daemon, or why a dial did
/// (Swift <c>RPCClient.ClientError</c>). Thrown as
/// <see cref="RpcClientException"/>; its text is Swift's description.
/// </summary>
public sealed record ClientError
{
    private ClientError(ClientErrorKind kind, string? method, string? reason)
    {
        Kind = kind;
        Method = method;
        Reason = reason;
    }

    /// <summary>A call without a connection.</summary>
    public static ClientError NotConnected { get; } = new(ClientErrorKind.NotConnected, null, null);

    /// <summary>The connection ended while the call waited.</summary>
    public static ClientError Disconnected { get; } = new(ClientErrorKind.Disconnected, null, null);

    /// <summary>What it is.</summary>
    public ClientErrorKind Kind { get; }

    /// <summary>The method that timed out (<see cref="ClientErrorKind.Timeout"/>); <c>connect</c> for the dial.</summary>
    public string? Method { get; }

    /// <summary>How the socket failed (<see cref="ClientErrorKind.Transport"/>).</summary>
    public string? Reason { get; }

    /// <summary>No answer to <paramref name="method"/> in time.</summary>
    public static ClientError Timeout(string method)
    {
        ArgumentNullException.ThrowIfNull(method);
        return new(ClientErrorKind.Timeout, method, null);
    }

    /// <summary>The socket failed for <paramref name="reason"/>, fixed text.</summary>
    public static ClientError Transport(string reason)
    {
        ArgumentNullException.ThrowIfNull(reason);
        return new(ClientErrorKind.Transport, null, reason);
    }

    /// <summary>Swift's description, word for word.</summary>
    public override string ToString() => Kind switch
    {
        ClientErrorKind.NotConnected => "not connected to malachid",
        ClientErrorKind.Timeout => $"{Method} timed out",
        ClientErrorKind.Disconnected => "connection to malachid lost",
        _ => Reason ?? "",
    };
}

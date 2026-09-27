// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift
// (RPCClient.HandshakeError); Go: backend/pkg/api/handshake.go
// (HandshakeError, HandshakeReason.String), ui/internal/client/client.go
// (DaemonProtocol).
//
// Swift's enum with associated values is this record (compared as Swift
// compares the enum) and HandshakeException carries it.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

/// <summary>
/// Why <see cref="RpcClient.ConnectAsync"/> refused a daemon it reached
/// (api.HandshakeError): one it cannot or must not use. Kept apart from
/// <see cref="ClientError"/>, which is what calls fail with. No text carries
/// the key, a nonce or a proof.
/// </summary>
public sealed record HandshakeError
{
    private HandshakeError(HandshakeReason reason, int daemon, ErrorCode code, string? detail)
    {
        Reason = reason;
        Daemon = daemon;
        Code = code;
        Detail = detail;
    }

    /// <summary>Nothing proved the key (<see cref="HandshakeReason.DaemonUnproven"/>).</summary>
    public static HandshakeError DaemonUnproven { get; } = new(HandshakeReason.DaemonUnproven, 0, default, null);

    /// <summary>No complete answer in time (<see cref="HandshakeReason.TimedOut"/>).</summary>
    public static HandshakeError TimedOut { get; } = new(HandshakeReason.TimedOut, 0, default, null);

    /// <summary>How it failed.</summary>
    public HandshakeReason Reason { get; }

    /// <summary>The daemon's protocol version (<see cref="HandshakeReason.ProtocolMismatch"/>); 0 otherwise.</summary>
    public int Daemon { get; }

    /// <summary>The daemon's error code (<see cref="HandshakeReason.Rejected"/>); 0 otherwise.</summary>
    public ErrorCode Code { get; }

    /// <summary>
    /// The key file's reason (<see cref="HandshakeReason.KeyUnavailable"/>) or
    /// the broken rule (<see cref="HandshakeReason.Malformed"/>): fixed text.
    /// </summary>
    public string? Detail { get; }

    /// <summary>The daemon speaks protocol <paramref name="daemon"/>.</summary>
    public static HandshakeError ProtocolMismatch(int daemon) => new(HandshakeReason.ProtocolMismatch, daemon, default, null);

    /// <summary>The key file cannot be used for <paramref name="reason"/>.</summary>
    public static HandshakeError KeyUnavailable(string reason)
    {
        ArgumentNullException.ThrowIfNull(reason);
        return new(HandshakeReason.KeyUnavailable, 0, default, reason);
    }

    /// <summary>The daemon answered a handshake call with <paramref name="code"/>.</summary>
    public static HandshakeError Rejected(ErrorCode code) => new(HandshakeReason.Rejected, 0, code, null);

    /// <summary>An answer broke the rule <paramref name="detail"/>, fixed text.</summary>
    public static HandshakeError Malformed(string detail)
    {
        ArgumentNullException.ThrowIfNull(detail);
        return new(HandshakeReason.Malformed, 0, default, detail);
    }

    /// <summary>
    /// The protocol version of the daemon when <paramref name="error"/> is, or
    /// wraps, the handshake's report that the daemon speaks another one (1
    /// for a daemon older than the handshake), and 0 for any other error and
    /// for null (client.DaemonProtocol).
    /// </summary>
    public static int DaemonProtocol(Exception? error)
    {
        for (var e = error; e is not null; e = e.InnerException)
        {
            if (e is HandshakeException { Error: { Reason: HandshakeReason.ProtocolMismatch } mismatch })
            {
                return mismatch.Daemon;
            }
            if (e is AggregateException { InnerExceptions.Count: 1 } one)
            {
                return DaemonProtocol(one.InnerExceptions[0]);
            }
        }
        return 0;
    }

    /// <summary>The reason's stable symbolic name, as Go's <c>HandshakeReason.String</c>.</summary>
    public static string NameOf(HandshakeReason reason) => reason switch
    {
        HandshakeReason.ProtocolMismatch => "protocolMismatch",
        HandshakeReason.KeyUnavailable => "keyUnavailable",
        HandshakeReason.DaemonUnproven => "daemonUnproven",
        HandshakeReason.Rejected => "rejected",
        HandshakeReason.Malformed => "malformed",
        HandshakeReason.TimedOut => "timedOut",
        _ => $"unknown({(int)reason})",
    };

    /// <summary>Go's and Swift's text, word for word.</summary>
    public override string ToString() => Reason switch
    {
        HandshakeReason.ProtocolMismatch => $"malachid speaks protocol version {Daemon}, this client {API.ProtocolVersion}",
        HandshakeReason.KeyUnavailable => $"cannot use malachid's connection key: {Detail}",
        HandshakeReason.DaemonUnproven => "the process on the socket did not prove it holds malachid's connection key",
        HandshakeReason.Rejected => $"malachid rejected the handshake ({Code.Name})",
        HandshakeReason.Malformed => $"malformed handshake answer from malachid: {Detail}",
        HandshakeReason.TimedOut => "malachid did not complete the handshake in time",
        _ => $"rpc handshake failed ({NameOf(Reason)})",
    };
}

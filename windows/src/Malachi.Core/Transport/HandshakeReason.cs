// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of backend/pkg/api/handshake.go (HandshakeReason) and the cases of
// macos/Sources/MalachiCore/Transport/RPCClient.swift
// (RPCClient.HandshakeError).

namespace Malachi.Core.Transport;

/// <summary>
/// How a handshake failed (api.HandshakeReason, with Go's numbers; 0 is no
/// reason).
/// </summary>
public enum HandshakeReason
{
    /// <summary>
    /// The daemon speaks another protocol version (1 for one that does not
    /// know <c>system.hello</c>). The key file was not read and nothing was
    /// sent after <c>system.hello</c>.
    /// </summary>
    ProtocolMismatch = 1,

    /// <summary>
    /// The key file is missing, not a key file, or not safe to use
    /// (<see cref="DaemonKey"/>); the reason names the path, never the content.
    /// </summary>
    KeyUnavailable,

    /// <summary>
    /// Whatever answers on the socket did not prove that it holds the key of
    /// the key file. Nothing was sent after <c>system.hello</c>.
    /// </summary>
    DaemonUnproven,

    /// <summary>The daemon answered a handshake call with an error; its message is dropped.</summary>
    Rejected,

    /// <summary>An answer broke the protocol; the detail is fixed text.</summary>
    Malformed,

    /// <summary>No complete answer within the handshake timeout.</summary>
    TimedOut,
}

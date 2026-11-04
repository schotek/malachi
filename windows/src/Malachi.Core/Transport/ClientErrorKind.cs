// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the cases of macos/Sources/MalachiCore/Transport/RPCClient.swift
// (RPCClient.ClientError).

namespace Malachi.Core.Transport;

/// <summary>What a <see cref="ClientError"/> is.</summary>
public enum ClientErrorKind
{
    /// <summary>A call without a connection (Go's <c>ErrDisconnected</c> before a call).</summary>
    NotConnected,

    /// <summary>No answer within the call's timeout (Go's <c>context.DeadlineExceeded</c>).</summary>
    Timeout,

    /// <summary>The connection ended while the call waited (Go's <c>ErrDisconnected</c> after it).</summary>
    Disconnected,

    /// <summary>The socket failed; the reason says how.</summary>
    Transport,
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift (throwing
// RPCClient.HandshakeError); Go: backend/pkg/api/handshake.go
// (*HandshakeError as an error).

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// <see cref="RpcClient.ConnectAsync"/> refused the daemon it reached; the
/// <see cref="Error"/> says why, and the message is its text, which never
/// carries the key, a nonce or a proof.
/// </summary>
public sealed class HandshakeException : Exception
{
    /// <summary>The exception of <paramref name="error"/>.</summary>
    public HandshakeException(HandshakeError error)
        : base(error?.ToString())
    {
        ArgumentNullException.ThrowIfNull(error);
        Error = error;
    }

    /// <summary>The exception of <paramref name="error"/> with the exception that caused it.</summary>
    public HandshakeException(HandshakeError error, Exception? innerException)
        : base(error?.ToString(), innerException)
    {
        ArgumentNullException.ThrowIfNull(error);
        Error = error;
    }

    /// <summary>A malformed answer without detail, for the conventions of the type.</summary>
    public HandshakeException()
        : this(HandshakeError.Malformed(""))
    {
    }

    /// <summary>A malformed answer with <paramref name="message"/> as the detail, for the conventions of the type.</summary>
    public HandshakeException(string message)
        : this(HandshakeError.Malformed(message ?? ""))
    {
    }

    /// <summary>The same with its cause, for the conventions of the type.</summary>
    public HandshakeException(string message, Exception innerException)
        : this(HandshakeError.Malformed(message ?? ""), innerException)
    {
    }

    /// <summary>Why.</summary>
    public HandshakeError Error { get; }
}

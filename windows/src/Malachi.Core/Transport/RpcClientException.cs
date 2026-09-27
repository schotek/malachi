// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/RPCClient.swift (throwing
// RPCClient.ClientError).

using System;

namespace Malachi.Core.Transport;

/// <summary>
/// A call or a dial failed without an answer from the daemon; the
/// <see cref="Error"/> says why, and the message is its text.
/// </summary>
public sealed class RpcClientException : Exception
{
    /// <summary>The exception of <paramref name="error"/>.</summary>
    public RpcClientException(ClientError error)
        : base(error?.ToString())
    {
        ArgumentNullException.ThrowIfNull(error);
        Error = error;
    }

    /// <summary>The exception of <paramref name="error"/> with the exception that caused it.</summary>
    public RpcClientException(ClientError error, Exception? innerException)
        : base(error?.ToString(), innerException)
    {
        ArgumentNullException.ThrowIfNull(error);
        Error = error;
    }

    /// <summary>A transport failure without a reason, for the conventions of the type.</summary>
    public RpcClientException()
        : this(ClientError.Transport(""))
    {
    }

    /// <summary>A transport failure for <paramref name="message"/>, for the conventions of the type.</summary>
    public RpcClientException(string message)
        : this(ClientError.Transport(message ?? ""))
    {
    }

    /// <summary>A transport failure for <paramref name="message"/> with its cause, for the conventions of the type.</summary>
    public RpcClientException(string message, Exception innerException)
        : this(ClientError.Transport(message ?? ""), innerException)
    {
    }

    /// <summary>Why.</summary>
    public ClientError Error { get; }
}

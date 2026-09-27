// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (RPCError as a
// thrown Error); GTK: ui/internal/client/client.go (Call returning
// *api.Error).
//
// Swift throws the error object itself; C# throws this exception, which
// carries the RpcError record of Malachi.Core.Api unchanged.

using System;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Text;

namespace Malachi.Core.Transport;

/// <summary>
/// The daemon answered a call with an error. <see cref="Error"/> is the
/// error object as it came; the message is Swift's description of it
/// (<c>"{message} ({code})"</c>), for logs, never for the user: the UI turns
/// the code into a sentence.
/// </summary>
public sealed class RpcException : Exception, RpcErrorText.IFailure
{
    /// <summary>The exception of the daemon's <paramref name="error"/>.</summary>
    public RpcException(RpcError error)
        : base(error?.ToString())
    {
        ArgumentNullException.ThrowIfNull(error);
        Error = error;
    }

    /// <summary>An internal error without a message, for the conventions of the type.</summary>
    public RpcException()
        : this(new RpcError { Code = ErrorCode.InternalError })
    {
    }

    /// <summary>An internal error with <paramref name="message"/>, for the conventions of the type.</summary>
    public RpcException(string message)
        : this(new RpcError { Code = ErrorCode.InternalError, Message = message ?? "" })
    {
    }

    /// <summary>An internal error with <paramref name="message"/> and its cause, for the conventions of the type.</summary>
    public RpcException(string message, Exception innerException)
        : base(message, innerException)
    {
        Error = new RpcError { Code = ErrorCode.InternalError, Message = message ?? "" };
    }

    /// <summary>The daemon's error object.</summary>
    public RpcError Error { get; }

    /// <summary>A daemon error, for the error texts (Swift <c>error as? RPCError</c>).</summary>
    RpcErrorText.FailureKind RpcErrorText.IFailure.Kind => RpcErrorText.FailureKind.Daemon;

    /// <inheritdoc/>
    RpcError? RpcErrorText.IFailure.DaemonError => Error;

    /// <summary>The stable code (docs/api.md §2).</summary>
    public ErrorCode Code => Error.Code;

    /// <summary>The method-specific detail, if any (<see cref="Exception.Data"/> is the BCL's dictionary).</summary>
    public JsonElement? ErrorData => Error.Data;
}

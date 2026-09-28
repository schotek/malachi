// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A failed call as the transport throws it, for the tests of the error
// texts: the cases of Swift's RPCClient.ClientError (notConnected,
// disconnected, timeout, transport) and a thrown RPCError, told apart
// through RpcErrorText.IFailure, which the transport's exceptions implement.

using System;
using Malachi.Core.Api;
using Malachi.Core.Text;

namespace Malachi.Core.Tests.Text;

public sealed class RpcErrorTextFailureException : Exception, RpcErrorText.IFailure
{
    public RpcErrorTextFailureException()
        : this(RpcErrorText.FailureKind.Failed, null, "failed")
    {
    }

    public RpcErrorTextFailureException(string message)
        : this(RpcErrorText.FailureKind.Failed, null, message)
    {
    }

    public RpcErrorTextFailureException(string message, Exception innerException)
        : base(message, innerException)
    {
        Kind = RpcErrorText.FailureKind.Failed;
    }

    private RpcErrorTextFailureException(RpcErrorText.FailureKind kind, RpcError? error, string message)
        : base(message)
    {
        Kind = kind;
        DaemonError = error;
    }

    public RpcErrorText.FailureKind Kind { get; }

    public RpcError? DaemonError { get; }

    /// <summary>A thrown RPCError: the daemon's answer.</summary>
    public static RpcErrorTextFailureException Daemon(RpcError e) => new(RpcErrorText.FailureKind.Daemon, e, e.ToString());

    /// <summary>A thrown RPCError of <paramref name="code"/>.</summary>
    public static RpcErrorTextFailureException Daemon(ErrorCode code, string message = "x") =>
        Daemon(new RpcError { Code = code, Message = message });

    /// <summary>ClientError.notConnected.</summary>
    public static RpcErrorTextFailureException NotConnected() => new(RpcErrorText.FailureKind.NoBackend, null, "not connected to malachid");

    /// <summary>ClientError.disconnected.</summary>
    public static RpcErrorTextFailureException Disconnected() => new(RpcErrorText.FailureKind.NoBackend, null, "connection to malachid lost");

    /// <summary>ClientError.timeout(method:).</summary>
    public static RpcErrorTextFailureException Timeout(string method) => new(RpcErrorText.FailureKind.TimedOut, null, method + " timed out");

    /// <summary>ClientError.transport(reason).</summary>
    public static RpcErrorTextFailureException Transport(string reason) => new(RpcErrorText.FailureKind.Failed, null, reason);
}

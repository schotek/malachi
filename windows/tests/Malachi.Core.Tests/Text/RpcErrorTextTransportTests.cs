// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The seam between the transport and the error texts: the exceptions the
// RpcClient really throws read as macos/Sources/MalachiCore/Text/RPCErrorText.swift
// reads RPCError and RPCClient.ClientError (GTK: ui/internal/widget/rpc.go,
// RPCErrorText).

using System;
using Malachi.Core.Api;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Text;

public sealed class RpcErrorTextTransportTests
{
    [Fact]
    public void NoConnectionNeedsABackend()
    {
        Assert.Equal("Saving needs a running mail backend",
            RpcErrorText.Text("Saving", new RpcClientException(ClientError.NotConnected)));
        Assert.Equal("Saving needs a running mail backend",
            RpcErrorText.Text("Saving", new RpcClientException(ClientError.Disconnected)));
    }

    [Fact]
    public void ATimeoutTimedOut()
    {
        Assert.Equal("Saving timed out",
            RpcErrorText.Text("Saving", new RpcClientException(ClientError.Timeout("draft.save"))));
    }

    [Fact]
    public void AFailedSocketIsThePlainFailure()
    {
        Assert.Equal(RpcErrorText.Text("Saving", (Exception?)null),
            RpcErrorText.Text("Saving", new RpcClientException(ClientError.Transport("broken pipe"))));
    }

    [Fact]
    public void TheDaemonsErrorIsReadThroughItsCode()
    {
        var error = new RpcError { Code = ErrorCode.DraftNotFound, Message = "draft d_1 not found" };
        var thrown = new RpcException(error);

        Assert.Equal(RpcErrorText.Text("Saving", error), RpcErrorText.Text("Saving", thrown));
        Assert.Same(error, RpcErrorText.DaemonError(thrown));
        Assert.Null(RpcErrorText.DaemonError(new RpcClientException(ClientError.NotConnected)));
    }

    [Fact]
    public void AWrappedFailureIsFoundAsGoErrorsAsFindsIt()
    {
        var inner = new RpcClientException(ClientError.Timeout("message.body"));
        Assert.Equal("Loading timed out",
            RpcErrorText.Text("Loading", new InvalidOperationException("outer", inner)));
    }
}

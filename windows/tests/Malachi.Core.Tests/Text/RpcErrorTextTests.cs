// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/RPCErrorTextTests.swift, the
// counterpart of ui/internal/widget/rpc_test.go (TestRPCErrorText,
// TestEndpointErrorText, TestTLSErrorTexts). Swift's thrown RPCError and
// RPCClient.ClientError are RpcErrorTextFailureException (what the transport's
// exceptions tell through RpcErrorText.IFailure); the daemon's error itself
// is the RpcError record, which Text also takes directly.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Tests.Wizard;
using Malachi.Core.Text;
using Xunit;

namespace Malachi.Core.Tests.Text;

public sealed class RpcErrorTextTests
{
    [Fact]
    public void RpcErrorTextTest()
    {
        var cases = new Dictionary<int, string>
        {
            [ErrorCode.AuthFailed] = "rejected the user name or password",
            [ErrorCode.AuthRequired] = "sign-in required",
            [ErrorCode.NetworkError] = "could not be reached",
            [ErrorCode.ServerError] = "returned an error",
            [ErrorCode.TlsError] = "secure connection",
            [ErrorCode.ServerTimeout] = "did not respond",
            [ErrorCode.KeyringError] = "keyring",
            [ErrorCode.Conflict] = "conflicted",
        };
        foreach (var (code, want) in cases)
        {
            var e = new RpcError { Code = code, Message = "detail" };
            foreach (var got in new[] { RpcErrorText.Text("Testing", e), RpcErrorText.Text("Testing", RpcErrorTextFailureException.Daemon(e)) })
            {
                Assert.True(got.StartsWith("Testing ", StringComparison.Ordinal) && got.Contains(want, StringComparison.Ordinal), $"{code}: {got}");
            }
        }
        Assert.Contains("running mail backend", RpcErrorText.Text("Testing", RpcErrorTextFailureException.Disconnected()), StringComparison.Ordinal);
        Assert.Contains("running mail backend", RpcErrorText.Text("Testing", RpcErrorTextFailureException.NotConnected()), StringComparison.Ordinal);
        Assert.Contains("timed out", RpcErrorText.Text("Testing", RpcErrorTextFailureException.Timeout("x")), StringComparison.Ordinal);
        Assert.Contains("timed out", RpcErrorText.Text("Testing", new OperationCanceledException()), StringComparison.Ordinal);
        Assert.Equal("Testing failed", RpcErrorText.Text("Testing", new RpcError { Code = 9999, Message = "x" }));
        Assert.Equal("Testing failed", RpcErrorText.Text("Testing", RpcErrorTextFailureException.Transport("boom")));
        Assert.Equal("Testing failed", RpcErrorText.Text("Testing", (Exception?)null));
        Assert.Equal("Testing failed", RpcErrorText.Text("Testing", (RpcError?)null));
        Assert.Equal("Testing was rejected: port 0", RpcErrorText.Text("Testing", new RpcError { Code = ErrorCode.InvalidArgument, Message = "port 0" }));
        Assert.Equal("Testing is not available yet", RpcErrorText.Text("Testing", new RpcError { Code = ErrorCode.NotImplemented, Message = "" }));
        Assert.Equal("The draft no longer exists", RpcErrorText.Text("Testing", new RpcError { Code = ErrorCode.DraftNotFound, Message = "" }));
        Assert.Equal(
            "Testing the connection failed: sign-in required",
            RpcErrorText.Text("Testing the connection", new RpcError { Code = ErrorCode.AuthRequired, Message = "no stored password" }));
    }

    [Fact]
    public void TlsErrorTexts()
    {
        var cert = new CertificateInfo { Sha256 = new string('a', 64), Subject = "127.0.0.1" };
        RpcError TlsErr(TlsErrorReason reason) => TlsErrors.Error(new TlsErrorData { Reason = reason, Certificate = cert });
        var cases = new Dictionary<string, string>
        {
            [TlsErrorReason.Untrusted] = "The server's certificate is not from a trusted authority",
            [TlsErrorReason.HostnameMismatch] = "The server's certificate is for another name",
            [TlsErrorReason.Expired] = "The server's certificate has expired",
            [TlsErrorReason.NotYetValid] = "The server's certificate is not valid yet",
            [TlsErrorReason.Invalid] = "The server's certificate is not valid",
            [TlsErrorReason.Other] = "The system does not accept the server's certificate",
            ["brandNew"] = "The system does not accept the server's certificate",
            [TlsErrorReason.PinMismatch] = "The server presented a different certificate than the one you trust",
            [TlsErrorReason.StarttlsUnavailable] = "The server does not offer STARTTLS",
            [TlsErrorReason.TlsRequired] = "The server requires TLS before signing in",
        };
        foreach (var (reason, want) in cases)
        {
            Assert.True(RpcErrorText.EndpointErrorText(TlsErr(reason)) == want, "endpoint " + reason);
            Assert.True(RpcErrorText.Text("Sending", TlsErr(reason)) == want, "rpc " + reason);
        }
        // A handshake failure and a tlsError without details keep the general
        // sentence.
        foreach (var e in new[] { TlsErr(TlsErrorReason.Handshake), new RpcError { Code = ErrorCode.TlsError, Message = "x" } })
        {
            Assert.Equal("The secure connection could not be established", RpcErrorText.EndpointErrorText(e));
            Assert.Equal("Sending failed: the secure connection could not be established", RpcErrorText.Text("Sending", e));
        }
    }

    [Fact]
    public void EndpointErrorTextTest()
    {
        Assert.Equal("Failed", RpcErrorText.EndpointErrorText(null));
        Assert.Equal("Rejected: port 0", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.InvalidArgument, Message = "port 0" }));
        Assert.Equal("Failed: odd", RpcErrorText.EndpointErrorText(new RpcError { Code = 9999, Message = "odd" }));
        Assert.Contains("secure connection", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.TlsError, Message = "x" }), StringComparison.Ordinal);
        Assert.Equal("Sign in to this account again", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.AuthRequired, Message = "x" }));
        Assert.Equal("The sign-in service is not available", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.Unavailable, Message = "x" }));
        Assert.Equal("The system keyring is unavailable", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.KeyringError, Message = "x" }));
        Assert.Equal("No network connection", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.Offline, Message = "x" }));
    }

    // The sentences for the rest of the codes rpc.go names, and the ones it
    // leaves to the plain "failed".
    [Fact]
    public void EveryCode()
    {
        (int Code, string Want)[] cases =
        [
            (ErrorCode.AttachmentNotFound, "The attachment no longer exists"),
            (ErrorCode.AttachmentTooBig, "The attachment is too big"),
            (ErrorCode.SanitizeFailed, "Saving failed: formatted text cannot be saved yet"),
            (ErrorCode.AccountNotFound, "Saving failed: unknown account"),
            (ErrorCode.KeyringError, "Saving failed: the system keyring is unavailable"),
            (ErrorCode.MessageNotFound, "Saving failed"),
            (ErrorCode.Offline, "Saving failed"),
            (ErrorCode.Unauthenticated, "Saving failed"),
        ];
        foreach (var (code, want) in cases)
        {
            Assert.Equal(want, RpcErrorText.Text("Saving", new RpcError { Code = code, Message = "technical" }));
        }
        Assert.Equal("The server did not respond in time", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.ServerTimeout }));
        Assert.Equal("Not supported yet", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.NotImplemented }));
        Assert.Equal("The server returned an error", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.ServerError }));
        Assert.Equal("The server could not be reached", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.NetworkError }));
        Assert.Equal("The server rejected the user name or password", RpcErrorText.EndpointErrorText(new RpcError { Code = ErrorCode.AuthFailed }));
        Assert.Null(RpcErrorText.TlsReasonText(new RpcError { Code = ErrorCode.NetworkError }));
    }

    // How a failed call is told apart (Go's errors.Is/As, Swift's type
    // checks): through the InnerException chain, the first that says
    // anything deciding.
    [Fact]
    public void Classify()
    {
        var daemon = new RpcError { Code = ErrorCode.Conflict, Message = "x" };
        Assert.Equal((RpcErrorText.FailureKind.Daemon, daemon), RpcErrorText.Classify(RpcErrorTextFailureException.Daemon(daemon)));
        Assert.Equal((RpcErrorText.FailureKind.Daemon, daemon), RpcErrorText.Classify(new InvalidOperationException("save", RpcErrorTextFailureException.Daemon(daemon))));
        Assert.Equal((RpcErrorText.FailureKind.NoBackend, (RpcError?)null), RpcErrorText.Classify(RpcErrorTextFailureException.NotConnected()));
        Assert.Equal((RpcErrorText.FailureKind.TimedOut, (RpcError?)null), RpcErrorText.Classify(new TimeoutException()));
        Assert.Equal((RpcErrorText.FailureKind.TimedOut, (RpcError?)null), RpcErrorText.Classify(new System.Threading.Tasks.TaskCanceledException()));
        Assert.Equal((RpcErrorText.FailureKind.TimedOut, (RpcError?)null), RpcErrorText.Classify(new AggregateException(new TimeoutException())));
        Assert.Equal((RpcErrorText.FailureKind.Failed, (RpcError?)null), RpcErrorText.Classify(new InvalidOperationException("boom")));
        Assert.Equal((RpcErrorText.FailureKind.Failed, (RpcError?)null), RpcErrorText.Classify(null));
        Assert.Equal((RpcErrorText.FailureKind.Failed, (RpcError?)null), RpcErrorText.Classify(RpcErrorTextFailureException.Transport("reset")));
        Assert.Same(daemon, RpcErrorText.DaemonError(new InvalidOperationException("x", RpcErrorTextFailureException.Daemon(daemon))));
        Assert.Null(RpcErrorText.DaemonError(RpcErrorTextFailureException.Disconnected()));
        Assert.Equal("Saving conflicted with another change", RpcErrorText.Text("Saving", new InvalidOperationException("save", RpcErrorTextFailureException.Daemon(daemon))));
    }

    // widget/rpc_test.go TestRPCErrorText: the Go cases, over the transport's
    // failures.
    [Fact]
    public void TestRpcErrorText()
    {
        Assert.Contains("running mail backend", RpcErrorText.Text("Testing", RpcErrorTextFailureException.Disconnected()), StringComparison.Ordinal);
        Assert.Contains("timed out", RpcErrorText.Text("Testing", new TimeoutException()), StringComparison.Ordinal);
        Assert.Equal("Testing failed", RpcErrorText.Text("Testing", RpcErrorTextFailureException.Daemon(9999)));
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/WizardResultsTests.swift, the
// counterpart of ui/internal/accountwizard/results_test.go (English
// catalogue), with Go's wrapped case of TestPasswordMissing.

using System;
using Malachi.Core.Api;
using Malachi.Core.Tests.Text;
using Malachi.Core.Wizard;
using Xunit;

namespace Malachi.Core.Tests.Wizard;

public sealed class WizardResultsTests
{
    private static readonly EndpointTestResult Ok = new() { Ok = true, LatencyMs = 0 };
    private static readonly EndpointTestResult Auth = new() { Ok = false, Error = new RpcError { Code = ErrorCode.AuthFailed, Message = "x" }, LatencyMs = 0 };
    private static readonly EndpointTestResult Tls = new() { Ok = false, Error = new RpcError { Code = ErrorCode.TlsError, Message = "x" }, LatencyMs = 0 };

    [Fact]
    public void ClassifyOutcomes()
    {
        (AccountTestResult Res, Outcome Want)[] cases =
        [
            (new AccountTestResult { Imap = Ok, Smtp = Ok }, Outcome.Ok),
            (new AccountTestResult { Imap = Ok, Smtp = Auth }, Outcome.AuthFailed),
            (new AccountTestResult { Imap = Tls, Smtp = Auth }, Outcome.AuthFailed),
            (new AccountTestResult { Imap = Tls, Smtp = Ok }, Outcome.Failed),
            (new AccountTestResult(), Outcome.Failed),
        ];
        for (var i = 0; i < cases.Length; i++)
        {
            Assert.True(Results.Classify(cases[i].Res) == cases[i].Want, $"case {i}");
        }
        // An endpoint that failed without an error is a failure, not ok.
        Assert.Equal(Outcome.Failed, Results.Classify(new AccountTestResult { Imap = new EndpointTestResult { Ok = false, LatencyMs = 0 }, Smtp = Ok }));
    }

    [Fact]
    public void EndpointSummaries()
    {
        var (icon, text) = Results.EndpointSummary(new EndpointTestResult { Ok = true, Capabilities = ["<b>x</b>"], LatencyMs = 42 });
        Assert.Equal("emblem-ok-symbolic", icon);
        Assert.Equal("Connected in 42 ms", text);

        var timeout = Results.EndpointSummary(new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.ServerTimeout, Message = "x" }, LatencyMs = 0 });
        Assert.Equal("dialog-error-symbolic", timeout.Icon);
        Assert.Contains("did not respond", timeout.Text, StringComparison.Ordinal);

        Assert.Equal("Failed", Results.EndpointSummary(new EndpointTestResult { Ok = false, LatencyMs = 0 }).Text);

        var unknown = Results.EndpointSummary(new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.StorageError, Message = "disk full" }, LatencyMs = 0 });
        Assert.Equal("Failed: disk full", unknown.Text);
        var rejected = Results.EndpointSummary(new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.InvalidArgument, Message = "bad port" }, LatencyMs = 0 });
        Assert.Equal("Rejected: bad port", rejected.Text);
        Assert.Equal("The server rejected the user name or password", Results.EndpointSummary(Auth).Text);
    }

    [Fact]
    public void ClassifyGraph()
    {
        Assert.Equal(Outcome.Ok, Results.Classify(new AccountTestResult { Graph = Ok }));
        Assert.Equal(Outcome.AuthFailed, Results.Classify(new AccountTestResult { Graph = Auth }));
        var (icon, text) = Results.EndpointSummary(null);
        Assert.Equal("dialog-question-symbolic", icon);
        Assert.Equal("Not tested", text);
    }

    // results_test.go TestPasswordMissing.
    [Fact]
    public void PasswordMissingTest()
    {
        var authRequired = RpcErrorTextFailureException.Daemon(ErrorCode.AuthRequired, "no stored password");
        (string Name, Exception? Error, bool Linked, bool Want)[] cases =
        [
            ("password account without a stored password", authRequired, false, true),
            ("wrapped", new InvalidOperationException("account.test", authRequired), false, true),
            ("linked account", authRequired, true, false),
            ("refused password", RpcErrorTextFailureException.Daemon(ErrorCode.AuthFailed), false, false),
            ("other error", RpcErrorTextFailureException.Daemon(ErrorCode.NetworkError), false, false),
            ("not an api error", new InvalidOperationException("boom"), false, false),
            ("no error", null, false, false),
        ];
        foreach (var (name, error, linked, want) in cases)
        {
            Assert.True(Results.PasswordMissing(error, linked) == want, name);
        }
        Assert.Equal("No password is stored for this account. Enter it to continue.", Results.PasswordBannerText(ErrorCode.AuthRequired));
        foreach (var reason in new ErrorCode[] { ErrorCode.AuthFailed, ErrorCode.KeyringError, 0 })
        {
            Assert.Equal("The server rejected the user name or password", Results.PasswordBannerText(reason));
        }
    }
}

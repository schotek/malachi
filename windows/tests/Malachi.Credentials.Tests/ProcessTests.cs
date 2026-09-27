// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only: the helper as a process, the way the daemon runs it. A GUI
// (WinExe) program has no console, yet the pipes it was started with are
// its standard handles: these tests prove that, and that it reads and
// writes plain UTF-8 bytes there. The round trip touches Credential Manager
// and runs only with MALACHI_CREDENTIALS_TEST=1.

using System;
using System.Text;
using System.Threading.Tasks;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class ProcessTests
{
    private const string Usage = "malachi-credentials: usage: malachi-credentials get|set|delete (one JSON line on stdin)\n";

    [Fact]
    public async Task AnUnknownOperationIsAUsageErrorOnStderr()
    {
        var run = await HelperProcess.RunAsync(["fetch"], [], TestContext.Current.CancellationToken);
        Assert.Equal(3, run.Exit);
        Assert.Empty(run.Stdout);
        Assert.Equal(Encoding.UTF8.GetBytes(Usage), run.Stderr);
    }

    [Fact]
    public async Task AMalformedRequestIsRefusedWithoutAWordAboutIt()
    {
        var stdin = """{"account":"malachi-test-x","key":"password","value":17,"note":"s3cret"}"""u8.ToArray();
        var run = await HelperProcess.RunAsync(["set"], stdin, TestContext.Current.CancellationToken);
        Assert.Equal(3, run.Exit);
        Assert.Empty(run.Stdout);
        Assert.Equal("malachi-credentials: malformed set request\n"u8.ToArray(), run.Stderr);
    }

    [Fact]
    public async Task AnOversizedRequestIsRefused()
    {
        var stdin = new byte[Request.MaxInput + 4096];
        stdin.AsSpan().Fill((byte)' ');
        var run = await HelperProcess.RunAsync(["get"], stdin, TestContext.Current.CancellationToken);
        Assert.Equal(3, run.Exit);
        Assert.Empty(run.Stdout);
        Assert.Equal("malachi-credentials: request exceeds 1048576 bytes\n"u8.ToArray(), run.Stderr);
    }

    [Fact]
    public async Task RoundTripsThroughThePipes()
    {
        CredentialRoundTripTests.SkipUnlessEnabled();
        var account = CredentialRoundTripTests.TestAccount();
        var cancellationToken = TestContext.Current.CancellationToken;
        byte[] Line(string json) => Encoding.UTF8.GetBytes(json + "\n");
        var password = $$"""{"account":"{{account}}","key":"password"}""";
        var token = $$"""{"account":"{{account}}","key":"oauth2.refresh_token"}""";
        var longValue = new string('r', 6000) + "ö";
        try
        {
            var run = await HelperProcess.RunAsync(["get"], Line(password), cancellationToken);
            Assert.Equal((2, "", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));

            run = await HelperProcess.RunAsync(["set"], Line(password[..^1] + ""","value":"pa\"ss wörd\n"}"""), cancellationToken);
            Assert.Equal((0, "{}\n", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));

            run = await HelperProcess.RunAsync(["get"], Line(password), cancellationToken);
            Assert.Equal(0, run.Exit);
            // UTF-8 as it is, no byte order mark, one line.
            Assert.Equal("{\"value\":\"pa\\\"ss wörd\\n\"}\n"u8.ToArray(), run.Stdout);

            run = await HelperProcess.RunAsync(["set"], Line(token[..^1] + $$""","value":"{{longValue}}"}"""), cancellationToken);
            Assert.Equal((0, "{}\n", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));
            run = await HelperProcess.RunAsync(["get"], Line(token), cancellationToken);
            Assert.Equal((0, $$"""{"value":"{{longValue}}"}""" + "\n", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));

            foreach (var request in new[] { password, token })
            {
                run = await HelperProcess.RunAsync(["delete"], Line(request), cancellationToken);
                Assert.Equal((0, "{}\n", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));
                run = await HelperProcess.RunAsync(["get"], Line(request), cancellationToken);
                Assert.Equal((2, "", ""), (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));
            }
        }
        finally
        {
            CredentialRoundTripTests.RemoveAll(account);
        }
    }

    private static string Utf8(byte[] bytes) => Encoding.UTF8.GetString(bytes);
}

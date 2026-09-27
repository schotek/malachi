// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only: the helper as a process, the way the daemon runs it. A GUI
// (WinExe) program has no console, yet the pipes it was started with are
// its standard handles: these tests prove that, and that it reads and
// writes plain UTF-8 bytes there. The tests that touch Credential Manager
// run only with MALACHI_CREDENTIALS_TEST=1.

using System;
using System.Diagnostics;
using System.IO;
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

    [Fact]
    public async Task AnItemWrittenByCmdkeyIsNotHandedOver()
    {
        CredentialRoundTripTests.SkipUnlessEnabled();
        var account = CredentialRoundTripTests.TestAccount();
        var cancellationToken = TestContext.Current.CancellationToken;
        var target = $"{Request.Service}/{account}/password";
        try
        {
            // cmdkey stores the password as UTF-16LE, without the digest.
            var item = WriteWithCmdkey(target, "abc");
            Assert.Equal(Encoding.Unicode.GetBytes("abc"), item.Blob);
            Assert.Null(item.Digest);

            var run = await HelperProcess.RunAsync(["get"], Encoding.UTF8.GetBytes($$"""{"account":"{{account}}","key":"password"}""" + "\n"), cancellationToken);
            Assert.Equal(
                (1, "", "malachi-credentials: get: the stored item is corrupt: the item was not written by malachi-credentials\n"),
                (run.Exit, Utf8(run.Stdout), Utf8(run.Stderr)));
        }
        finally
        {
            CredentialRoundTripTests.RemoveAll(account);
        }
    }

    private static string Utf8(byte[] bytes) => Encoding.UTF8.GetString(bytes);

    // Stores a generic credential with the system's cmdkey.exe and reads it
    // back.
    private static GenericCredential WriteWithCmdkey(string target, string password)
    {
        var start = new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, "cmdkey.exe"))
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var arg in new[] { "/generic:" + target, "/user:dummy", "/pass:" + password })
        {
            start.ArgumentList.Add(arg);
        }
        using (var process = Process.Start(start) ?? throw new InvalidOperationException("cmdkey did not start"))
        {
            var output = process.StandardOutput.ReadToEndAsync();
            var error = process.StandardError.ReadToEndAsync();
            if (!process.WaitForExit(TimeSpan.FromSeconds(30)))
            {
                process.Kill();
                Assert.Fail("cmdkey did not finish");
            }
            Assert.Equal(0, process.ExitCode);
            Task.WaitAll(output, error);
        }
        Assert.Equal(0, new Win32CredentialManager().Read(target, out var item));
        Assert.NotNull(item);
        return item;
    }
}

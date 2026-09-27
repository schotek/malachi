// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only (main.swift has no tests): Program.Run, the port of
// macos/Sources/MalachiKeychain/main.swift, over streams in memory and
// Credential Manager in memory: what the daemon's internal/auth/helper
// sees on stdout, stderr and in the exit status.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class ProgramTests
{
    private const string Usage = "malachi-credentials: usage: malachi-credentials get|set|delete (one JSON line on stdin)\n";

    private readonly MemoryCredentialManager manager = new();

    [Fact]
    public void AnswersAsTheDaemonExpects()
    {
        // The transcript the daemon's side was specified with.
        const string account = "acc_0f";
        Expect(Run(["get"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.NotFound, "", "");
        Expect(Run(["set"], $$"""{"account":"{{account}}","key":"password","value":"pa\"ss wörd\n"}""" + "\n"), HelperExit.Ok, "{}\n", "");
        Expect(Run(["get"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.Ok, "{\"value\":\"pa\\\"ss wörd\\n\"}\n", "");
        Expect(Run(["get"], $$"""{"account":"{{account}}","key":"oauth2.refresh_token"}""" + "\n"), HelperExit.NotFound, "", "");
        Expect(Run(["set"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.BadRequest, "", "malachi-credentials: malformed set request\n");
        Expect(Run(["delete"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.Ok, "{}\n", "");
        Expect(Run(["delete"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.Ok, "{}\n", "");
        Expect(Run(["get"], $$"""{"account":"{{account}}","key":"password"}""" + "\n"), HelperExit.NotFound, "", "");
        Expect(Run(["fetch"], ""), HelperExit.BadRequest, "", Usage);
    }

    [Theory]
    [InlineData]
    [InlineData("fetch")]
    [InlineData("GET")]
    [InlineData("get", "extra")]
    [InlineData("--help")]
    public void AnythingButOneOperationIsAUsageError(params string[] args)
    {
        Expect(Run(args, """{"account":"acc_1","key":"password"}"""), HelperExit.BadRequest, "", Usage);
    }

    [Fact]
    public void AnOversizedRequestIsRefused()
    {
        var json = """{"account":"acc_1","key":"password","value":"v"}""" + new string(' ', Request.MaxInput);
        Expect(Run(["set"], json), HelperExit.BadRequest, "", "malachi-credentials: request exceeds 1048576 bytes\n");
        Assert.Empty(manager.Names);
    }

    [Fact]
    public void AMalformedRequestIsNotEchoed()
    {
        var result = Run(["set"], """{"account":"acc_1","key":"password","value":17,"note":"s3cret"}""");
        Expect(result, HelperExit.BadRequest, "", "malachi-credentials: malformed set request\n");
    }

    [Fact]
    public void ALongValueGoesThroughTheChunks()
    {
        var value = new string('t', 6000) + "ö";
        var json = Encoding.UTF8.GetString(Request.ValueLine(Encoding.UTF8.GetBytes(value)))[..^2];
        Expect(Run(["set"], json + ",\"account\":\"acc_1\",\"key\":\"oauth2.refresh_token\"}"), HelperExit.Ok, "{}\n", "");
        Assert.Equal(4, manager.Names.Count);
        Expect(Run(["get"], """{"account":"acc_1","key":"oauth2.refresh_token"}"""), HelperExit.Ok, $"{{\"value\":\"{value}\"}}\n", "");
    }

    [Fact]
    public void AStoreFailureIsExitOneWithoutTheValue()
    {
        manager.Inject = (call, _) => call == "write" ? MemoryCredentialManager.ErrorNoSuchLogonSession : 0;
        var result = Run(["set"], """{"account":"acc_1","key":"password","value":"s3cret"}""");
        Assert.Equal(HelperExit.Failure, result.Exit);
        Assert.Equal("", result.Stdout);
        Assert.StartsWith("malachi-credentials: set: CredWriteW failed: ", result.Stderr, StringComparison.Ordinal);
        Assert.EndsWith(" (Win32 error 1312)\n", result.Stderr, StringComparison.Ordinal);
        Assert.DoesNotContain("s3cret", result.Stderr, StringComparison.Ordinal);
    }

    [Fact]
    public void ACorruptItemIsExitOneAndNoValue()
    {
        var store = new CredentialStore(manager);
        Assert.Null(store.Set(new Request("acc_1", "password"), Encoding.UTF8.GetBytes(new string('p', 6000))));
        manager.Remove("io.github.schotek.Malachi/acc_1/password#2");
        Expect(
            Run(["get"], """{"account":"acc_1","key":"password"}"""),
            HelperExit.Failure,
            "",
            "malachi-credentials: get: the stored item is corrupt: chunk 2 of 3 is missing\n");
    }

    [Fact]
    public void AnItemWrittenByAnotherProgramIsExitOneAndNoValue()
    {
        // What `cmdkey /generic:… /pass:abc` leaves: UTF-16LE, no digest.
        manager.Put("io.github.schotek.Malachi/acc_1/password", Encoding.Unicode.GetBytes("abc"));
        Expect(
            Run(["get"], """{"account":"acc_1","key":"password"}"""),
            HelperExit.Failure,
            "",
            "malachi-credentials: get: the stored item is corrupt: the item was not written by malachi-credentials\n");

        // set replaces it.
        Expect(Run(["set"], """{"account":"acc_1","key":"password","value":"abc"}"""), HelperExit.Ok, "{}\n", "");
        Expect(Run(["get"], """{"account":"acc_1","key":"password"}"""), HelperExit.Ok, "{\"value\":\"abc\"}\n", "");
    }

    [Fact]
    public void AValueWhoseAnswerWouldBeCutIsExitOne()
    {
        // 32 KiB of backslashes: 64 KiB and 13 bytes as an answer.
        var json = $$"""{"account":"acc_1","key":"password","value":"{{new string('\\', 2 * 32 * 1024)}}"}""";
        Expect(
            Run(["set"], json),
            HelperExit.Failure,
            "",
            "malachi-credentials: set: the value's get answer would be longer than the 65536 bytes the daemon reads\n");
        Assert.Empty(manager.Names);
    }

    [Fact]
    public void AnAnswerTheDaemonKeepsWhole()
    {
        var value = new string('\\', 32761) + "x";
        var json = Encoding.UTF8.GetString(Request.ValueLine(Encoding.UTF8.GetBytes(value)))[..^2];
        Expect(Run(["set"], json + ",\"account\":\"acc_1\",\"key\":\"password\"}"), HelperExit.Ok, "{}\n", "");
        var result = Run(["get"], """{"account":"acc_1","key":"password"}""");
        Assert.Equal(HelperExit.Ok, result.Exit);
        Assert.Equal(Request.MaxAnswer, result.StdoutBytes.Length);
        using var answer = JsonDocument.Parse(result.StdoutBytes);
        Assert.Equal(value, answer.RootElement.GetProperty("value").GetString());
    }

    [Fact]
    public void ATooLongValueIsExitOne()
    {
        var json = $$"""{"account":"acc_1","key":"password","value":"{{new string('v', ChunkHeader.MaxLength + 1)}}"}""";
        Expect(Run(["set"], json), HelperExit.Failure, "", "malachi-credentials: set: the value is longer than the 40960 bytes an item can hold\n");
        Assert.Empty(manager.Names);
    }

    [Fact]
    public void AnUnexpectedExceptionIsExitOneWithoutItsMessage()
    {
        var store = new CredentialStore(new ThrowingCredentialManager());
        var output = new MemoryStream();
        var error = new MemoryStream();
        var exit = Program.Run(["set"], Input("""{"account":"acc_1","key":"password","value":"s3cret"}"""), output, error, store);
        Assert.Equal(HelperExit.Failure, exit);
        Assert.Empty(output.ToArray());
        Assert.Equal("malachi-credentials: internal error (InvalidOperationException)\n", Encoding.UTF8.GetString(error.ToArray()));
    }

    [Fact]
    public void AStdoutThatThrowsIsAFailure()
    {
        // The stream Main passes takes a broken pipe as written (the helper
        // then exits 0 to nobody); a Stream that throws is a failure.
        Assert.Null(new CredentialStore(manager).Set(new Request("acc_1", "password"), "hunter2"u8));
        var error = new MemoryStream();
        var exit = Program.Run(
            ["get"],
            Input("""{"account":"acc_1","key":"password"}"""),
            new BrokenStream(),
            error,
            new CredentialStore(manager));
        Assert.Equal(HelperExit.Failure, exit);
        Assert.Empty(error.ToArray());
    }

    [Fact]
    public void ARunThatDoesNotGetItsTurnIsExitOne()
    {
        var turns = CredentialLockTests.Unique(TimeSpan.FromMilliseconds(50));
        var store = new CredentialStore(manager);
        using (CredentialLockTests.Holder.Hold(turns))
        {
            var output = new MemoryStream();
            var error = new MemoryStream();
            var exit = Program.Run(["get"], Input("""{"account":"acc_1","key":"password"}"""), output, error, store, turns);
            Assert.Equal(HelperExit.Failure, exit);
            Assert.Empty(output.ToArray());
            Assert.Equal("malachi-credentials: get: another malachi-credentials did not finish in time\n", Encoding.UTF8.GetString(error.ToArray()));
            Assert.Empty(manager.Calls);
        }
        Assert.Equal(
            HelperExit.NotFound,
            Program.Run(["get"], Input("""{"account":"acc_1","key":"password"}"""), new MemoryStream(), new MemoryStream(), store, turns));
    }

    [Fact]
    public void AMalformedRequestDoesNotWaitForItsTurn()
    {
        var turns = CredentialLockTests.Unique(TimeSpan.FromSeconds(30));
        using (CredentialLockTests.Holder.Hold(turns))
        {
            var error = new MemoryStream();
            var exit = Program.Run(["get"], Input("not json"), new MemoryStream(), error, new CredentialStore(manager), turns);
            Assert.Equal(HelperExit.BadRequest, exit);
            Assert.Equal("malachi-credentials: malformed get request\n", Encoding.UTF8.GetString(error.ToArray()));
        }
    }

    [Fact]
    public void TheAnswerIsUtf8WithoutAByteOrderMark()
    {
        Assert.Null(new CredentialStore(manager).Set(new Request("acc_1", "password"), "wörd"u8));
        var result = Run(["get"], """{"account":"acc_1","key":"password"}""");
        Assert.Equal((byte)'{', result.StdoutBytes[0]);
        Assert.Equal("{\"value\":\"wörd\"}\n"u8.ToArray(), result.StdoutBytes);
    }

    private static MemoryStream Input(string text) => new(Encoding.UTF8.GetBytes(text));

    private static void Expect(RunResult result, HelperExit exit, string stdout, string stderr)
    {
        Assert.Equal(exit, result.Exit);
        Assert.Equal(stdout, result.Stdout);
        Assert.Equal(stderr, result.Stderr);
    }

    private RunResult Run(IReadOnlyList<string> args, string stdin)
    {
        var output = new MemoryStream();
        var error = new MemoryStream();
        var exit = Program.Run(args, Input(stdin), output, error, new CredentialStore(manager));
        return new RunResult(exit, output.ToArray(), Encoding.UTF8.GetString(error.ToArray()));
    }

    private sealed record RunResult(HelperExit Exit, byte[] StdoutBytes, string Stderr)
    {
        public string Stdout => Encoding.UTF8.GetString(StdoutBytes);
    }

    private sealed class ThrowingCredentialManager : ICredentialManager
    {
        public int Read(string targetName, out GenericCredential? credential) => throw new InvalidOperationException("s3cret");

        public int Write(string targetName, string userName, string comment, ReadOnlySpan<byte> blob, ReadOnlySpan<byte> digest) =>
            throw new InvalidOperationException(Encoding.UTF8.GetString(blob));

        public int Delete(string targetName) => throw new InvalidOperationException("s3cret");

        public int List(string prefix, out IReadOnlyList<string> targetNames) => throw new InvalidOperationException("s3cret");
    }

    private sealed class BrokenStream : MemoryStream
    {
        public override void Write(byte[] buffer, int offset, int count) => throw new IOException("the pipe is broken");

        public override void Write(ReadOnlySpan<byte> buffer) => throw new IOException("the pipe is broken");
    }
}

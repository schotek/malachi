// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/HandshakeTests.swift and of
// backend/pkg/api/handshake_test.go: RpcClient's handshake against a
// scripted daemon (FakeDaemon's HandshakeMode.Raw), the whole failure table
// (failCases, droppedScripts and the two timeouts) with Go's outcomes,
// including that the client sends nothing after the line Go stops at; that
// no text of a failure shows a key, a nonce or a proof
// (TestHandshakeErrorsCarryNoSecrets); the texts themselves
// (TestHandshakeErrorText); and TestHandshakeSuccess, SkipsNotifications,
// FreshNonce, Cancelled, ClearsDeadline and LinesFitBudget, which Swift did
// not port. Swift's table has the 46 cases it could script; the Go cases it
// left out (another key, a reflected proof, swapped nonces, a zero proof,
// the upper-case and short nonces and proofs, a missing proof, an empty key
// file, a connection closed at once) are here, with the exact details
// Swift's reader gives.
//
// The handshake deadline runs on a FakeTimeProvider: only the timeout cases
// advance it, so a slow machine cannot turn another case into a timeout.
//
// DecodesAsGo is the C# client's own: lines Go's table does not have, on
// which the decoding of encoding/json into Go's structs decides (folded
// member names, null members, repeated members, deep nesting, bytes that
// are not UTF-8), each with the outcome Go's client has.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class HandshakeTests
{
    /// <summary>A notification as a daemon of protocol 1 broadcasts it to everyone.</summary>
    private static readonly string TestNotification = Line("""{"jsonrpc":"2.0","method":"notify.accountsChanged","params":{}}""");

    /// <summary>Another daemon run's key.</summary>
    private static readonly byte[] OtherKey = RpcAuth.NewNonce();

    private static readonly string[] HelloOnly = [API.SystemHello.Name];
    private static readonly string[] Both = [API.SystemHello.Name, API.SystemAuthenticate.Name];

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    /// <summary>What is done to the key file before the client dials.</summary>
    private enum KeyFile
    {
        Keep,
        Remove,
        Empty,
        Uppercase,
        Crlf,
    }

    public static TheoryData<string> CaseNames => [.. FailCases().Keys];

    /// <summary>
    /// TestHandshakeFailures, TestHandshakeConnectionDropped and
    /// TestHandshakeTimesOut over one table: Go's outcome, nothing sent after
    /// the line Go stops at, and no key, nonce or proof in any text of the
    /// failure (TestHandshakeErrorsCarryNoSecrets).
    /// </summary>
    [Theory]
    [MemberData(nameof(CaseNames))]
    public async Task FailuresAsInGo(string name)
    {
        var fc = FailCases()[name];
        await using var fake = new FakeDaemon();
        fake.SetHandshake(fc.Mode);
        await fake.StartAsync();
        await EditAsync(fc.KeyFile, fake.KeyPath);
        var time = new FakeTimeProvider();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, timeProvider: time);
        var connect = client.ConnectAsync(Ct);
        if (fc.Outcome is Outcome.Refused { Error.Reason: HandshakeReason.TimedOut })
        {
            // The lines Go's case sends have arrived: the client waits.
            await Eventually.Holds(() => fake.Received.Count >= fc.Sent.Length);
            time.Advance(RpcTimeouts.Handshake);
        }
        var error = await Assert.ThrowsAnyAsync<Exception>(() => connect);
        Assert.True(fc.Outcome.Matches(error), $"{name}: {error}");
        // Once the client closed its connection, nothing more can come on it.
        await fake.WaitForEndedAsync(1);
        Assert.Equal(fc.Sent, fake.Received);
        Assert.IsType<RpcClientState.Disconnected>(client.State);
        ExpectNoSecrets(Texts(error, client.State), SecretsOf(fake), name);
    }

    // Go's "is" checks: the cause of a missing key file stays reachable.
    [Fact]
    public async Task AMissingKeyFileKeepsItsCause()
    {
        await using var fake = new FakeDaemon();
        fake.SetHandshake(new HandshakeMode.Raw(new HandshakeScript { Hello = ctx => Answer(1, ctx.RightResult) }));
        await fake.StartAsync();
        File.Delete(fake.KeyPath);
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        var e = await Assert.ThrowsAsync<HandshakeException>(() => client.ConnectAsync(Ct));
        var unavailable = Assert.IsType<KeyUnavailableException>(e.InnerException);
        Assert.IsType<FileNotFoundException>(unavailable.InnerException);
    }

    /// <summary>The fake daemon's own refusing modes show no secret either.</summary>
    [Fact]
    public async Task RefusalsCarryNoSecrets()
    {
        HandshakeMode[] modes =
        [
            new HandshakeMode.WrongProof(), new HandshakeMode.RejectClient(), new HandshakeMode.MalformedProof(),
            new HandshakeMode.Silent(), new HandshakeMode.OldDaemon(), new HandshakeMode.ProtocolVersion(99),
        ];
        foreach (var mode in modes)
        {
            await using var fake = new FakeDaemon();
            fake.SetHandshake(mode);
            await fake.StartAsync();
            var time = new FakeTimeProvider();
            using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, timeProvider: time);
            var connect = client.ConnectAsync(Ct);
            if (mode is HandshakeMode.Silent)
            {
                await Eventually.Holds(() => fake.Received.Count == 1);
                time.Advance(RpcTimeouts.Handshake);
            }
            var error = await Assert.ThrowsAsync<HandshakeException>(() => connect);
            ExpectNoSecrets(Texts(error, client.State), SecretsOf(fake), mode.ToString());
        }
    }

    /// <summary>TestHandshakeErrorText: Go's texts, word for word, and the reasons' names.</summary>
    [Fact]
    public void ErrorTextsAreGos()
    {
        (HandshakeError Error, string Want)[] cases =
        [
            (HandshakeError.ProtocolMismatch(1), $"malachid speaks protocol version 1, this client {API.ProtocolVersion}"),
            (HandshakeError.KeyUnavailable("rpc key unavailable"), "cannot use malachid's connection key: rpc key unavailable"),
            (HandshakeError.DaemonUnproven, "the process on the socket did not prove it holds malachid's connection key"),
            (HandshakeError.Rejected(ErrorCode.Unauthenticated), "malachid rejected the handshake (unauthenticated)"),
            (HandshakeError.Rejected(1234), "malachid rejected the handshake (unknown(1234))"),
            (HandshakeError.Malformed("a line is longer than 64 KiB"), "malformed handshake answer from malachid: a line is longer than 64 KiB"),
            (HandshakeError.TimedOut, "malachid did not complete the handshake in time"),
        ];
        foreach (var (error, want) in cases)
        {
            Assert.Equal(want, error.ToString());
            Assert.Equal(want, new HandshakeException(error).Message);
        }
        var names = new Dictionary<HandshakeReason, string>
        {
            [HandshakeReason.ProtocolMismatch] = "protocolMismatch",
            [HandshakeReason.KeyUnavailable] = "keyUnavailable",
            [HandshakeReason.DaemonUnproven] = "daemonUnproven",
            [HandshakeReason.Rejected] = "rejected",
            [HandshakeReason.Malformed] = "malformed",
            [HandshakeReason.TimedOut] = "timedOut",
            [0] = "unknown(0)",
        };
        foreach (var (reason, want) in names)
        {
            Assert.Equal(want, HandshakeError.NameOf(reason));
        }
    }

    /// <summary>
    /// TestHandshakeSuccess: the two lines exactly as Go writes them, and a
    /// notification that came in the same write as the last answer is
    /// delivered after the handshake.
    /// </summary>
    [Fact]
    public async Task Success()
    {
        await using var fake = new FakeDaemon();
        fake.SetNotificationWithAuthenticateAnswer(API.Notify.AccountsChanged);
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);
        Assert.Equal(API.Notify.AccountsChanged, (await client.Notifications.ReadAsync(Ct)).Method);
        // The fake's secrets: the key, then the client's nonce, its own nonce
        // and the two proofs of the connection.
        var secrets = fake.Secrets;
        var clientNonce = secrets[1];
        var clientProof = secrets[4];
        Assert.Equal(
            [
                $$$"""{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{"clientNonce":"{{{clientNonce}}}"}}""",
                $$$"""{"jsonrpc":"2.0","id":2,"method":"system.authenticate","params":{"clientProof":"{{{clientProof}}}"}}""",
            ],
            fake.ReceivedLines);
    }

    /// <summary>TestHandshakeSkipsNotifications: seven notifications and one with a null id before the answer.</summary>
    [Fact]
    public async Task SkipsNotifications()
    {
        var nulled = Line("""{"jsonrpc":"2.0","id":null,"method":"notify.syncState","params":{}}""");
        await using var fake = new FakeDaemon();
        fake.SetHandshake(new HandshakeMode.Raw(new HandshakeScript
        {
            Hello = ctx => string.Concat(Enumerable.Repeat(TestNotification, 7)) + nulled + Answer(1, ctx.RightResult),
            Authenticate = _ => Answer(2, "{}"),
        }));
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);
        Assert.Equal(new RpcClientState.Connected(), client.State);
    }

    /// <summary>TestHandshakeFreshNonce: system.hello as Go writes it, with a new nonce every time.</summary>
    [Fact]
    public async Task FreshNonce()
    {
        var seen = new HashSet<string>();
        for (var i = 0; i < 5; i++)
        {
            await using var fake = new FakeDaemon();
            fake.SetHandshake(new HandshakeMode.Raw(new HandshakeScript { Hello = _ => ErrorAnswer(1, ErrorCode.MethodNotFound, "unknown method") }));
            await fake.StartAsync();
            using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
            await Assert.ThrowsAsync<HandshakeException>(() => client.ConnectAsync(Ct));
            await fake.WaitForEndedAsync(1);
            var line = Assert.Single(fake.ReceivedLines);
            using var req = JsonDocument.Parse(line);
            var root = req.RootElement;
            Assert.Equal(["jsonrpc", "id", "method", "params"], root.EnumerateObject().Select(p => p.Name));
            Assert.Equal("2.0", root.GetProperty("jsonrpc").GetString());
            Assert.Equal("1", root.GetProperty("id").GetRawText());
            Assert.Equal(API.SystemHello.Name, root.GetProperty("method").GetString());
            var p = Assert.Single(root.GetProperty("params").EnumerateObject());
            Assert.Equal("clientNonce", p.Name);
            var nonce = p.Value.GetString()!;
            Assert.NotNull(RpcAuth.DecodeHex32(nonce));
            Assert.NotEqual(new string('0', 64), nonce);
            Assert.True(seen.Add(nonce), $"clientNonce {nonce} repeats");
        }
    }

    /// <summary>TestHandshakeCancelled: a cancelled dial ends at once, and a cancelled one sends nothing.</summary>
    [Fact]
    public async Task Cancelled()
    {
        await using (var fake = new FakeDaemon())
        {
            fake.SetHandshake(new HandshakeMode.Silent());
            await fake.StartAsync();
            using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
            using var cancel = new CancellationTokenSource();
            var connect = client.ConnectAsync(cancel.Token);
            await Eventually.Holds(() => fake.Received.Count == 1);
            var started = DateTime.UtcNow;
            cancel.Cancel();
            await Assert.ThrowsAnyAsync<OperationCanceledException>(() => connect);
            Assert.True(DateTime.UtcNow - started < TimeSpan.FromSeconds(1));
        }
        await using (var fake = new FakeDaemon())
        {
            await fake.StartAsync();
            using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
            using var cancel = new CancellationTokenSource();
            cancel.Cancel();
            await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.ConnectAsync(cancel.Token));
            Assert.Equal(0, fake.Accepted);
            Assert.Empty(fake.Received);
        }
    }

    /// <summary>TestHandshakeClearsDeadline: past the handshake's deadline the connection lives on.</summary>
    [Fact]
    public async Task ClearsDeadline()
    {
        await using var fake = new FakeDaemon();
        await fake.StartAsync();
        var time = new FakeTimeProvider();
        var timeout = TimeSpan.FromMilliseconds(200);
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, handshakeTimeout: timeout, timeProvider: time);
        await client.ConnectAsync(Ct);
        time.Advance(2 * timeout);
        await fake.PushNotificationAsync(API.Notify.AccountsChanged, "{}");
        Assert.Equal(API.Notify.AccountsChanged, (await client.Notifications.ReadAsync(Ct)).Method);
        Assert.Equal(new RpcClientState.Connected(), client.State);
    }

    /// <summary>TestHandshakeLinesFitBudget: the two lines take a quarter of what the daemon reads before authentication at most.</summary>
    [Fact]
    public async Task LinesFitBudget()
    {
        await using var fake = new FakeDaemon();
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);
        var lines = fake.ReceivedLines;
        Assert.Equal(2, lines.Count);
        Assert.True(lines.Sum(l => Encoding.UTF8.GetByteCount(l) + 1) <= (4 << 10) / 4);
    }

    public static TheoryData<string> DecodingCaseNames => [.. DecodingCases().Keys];

    /// <summary>
    /// Lines that encoding/json reads in its own way, and the outcome Go's
    /// client has with each: connected (null), or the refusal.
    /// </summary>
    [Theory]
    [MemberData(nameof(DecodingCaseNames))]
    public async Task DecodesAsGo(string name)
    {
        var (script, want) = DecodingCases()[name];
        await using var fake = new FakeDaemon();
        fake.SetHandshake(new HandshakeMode.Raw(script));
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, timeProvider: new FakeTimeProvider());
        if (want is null)
        {
            await client.ConnectAsync(Ct);
            Assert.Equal(new RpcClientState.Connected(), client.State);
            return;
        }
        var e = await Assert.ThrowsAsync<HandshakeException>(() => client.ConnectAsync(Ct));
        Assert.Equal(want, e.Error);
    }

    private static Dictionary<string, (HandshakeScript Script, HandshakeError? Want)> DecodingCases()
    {
        // The right result with text put in front of its members, or after them.
        static Func<HelloContext, string?> Before(string members) => c => Answer(1, "{" + members + "," + c.RightResult[1..]);
        static Func<HelloContext, string?> After(string members) => c => Answer(1, c.RightResult[..^1] + "," + members + "}");
        static HandshakeScript Connects(Func<HelloContext, string?> hello, bool latin1 = false) =>
            new() { Hello = hello, Authenticate = _ => Answer(2, "{}"), Latin1 = latin1 };
        static HandshakeScript Rejects(string authenticate) =>
            new() { Hello = c => Answer(1, c.RightResult), Authenticate = _ => authenticate };
        static string Capitals(string result) => result
            .Replace("\"protocolVersion\"", "\"PROTOCOLVERSION\"", StringComparison.Ordinal)
            .Replace("\"daemonNonce\"", "\"DaemonNonce\"", StringComparison.Ordinal);
        var noVersion = HandshakeError.Malformed("the system.hello result has no valid protocolVersion");
        var notJsonRpc = HandshakeError.Malformed("a line is not a JSON-RPC 2.0 message");
        var nested = new string('[', 100) + new string(']', 100);
        return new()
        {
            // A member of the wrong type fails the decoding, whatever follows it.
            ["protocolVersion of the wrong type, then the right one"] = (Connects(Before("\"protocolVersion\":\"2\"")), noVersion),
            ["daemonNonce of the wrong type, then the right one"] =
                (Connects(c => Answer(1, "{\"daemonNonce\":5," + c.RightResult[1..])), HandshakeError.Malformed("the system.hello result does not decode")),
            ["an error code of the wrong type, then a right one"] =
                (Rejects(Line("""{"jsonrpc":"2.0","id":2,"error":{"code":"x","code":1005}}""")), notJsonRpc),

            // Null leaves a field as it was.
            ["protocolVersion null after the right one"] = (Connects(After("\"protocolVersion\":null")), null),
            ["daemonNonce null after the right one"] = (Connects(After("\"daemonNonce\":null")), null),
            ["jsonrpc null after 2.0"] = (Connects(c => Line($$"""{"jsonrpc":"2.0","id":1,"result":{{c.RightResult}},"jsonrpc":null}""")), null),
            ["an error code null after 1005"] =
                (Rejects(Line("""{"jsonrpc":"2.0","id":2,"error":{"code":1005,"code":null,"message":"no"}}""")), HandshakeError.Rejected(ErrorCode.Unauthenticated)),
            ["a second error object without a code"] =
                (Rejects(Line("""{"jsonrpc":"2.0","id":2,"error":{"code":1005},"error":{"message":"no"}}""")), HandshakeError.Rejected(ErrorCode.Unauthenticated)),

            // Member names match as Go folds them.
            ["members in capitals"] = (Connects(c => Line("{\"JSONRPC\":\"2.0\",\"Id\":1,\"RESULT\":" + Capitals(c.RightResult) + "}")), null),
            ["a long s for an s"] = (Connects(_ => Answer(1, "{\"protocolVer\u017Fion\":99}")), HandshakeError.ProtocolMismatch(99)),
            ["a name that only looks alike"] = (Connects(_ => Answer(1, "{\"protocolVers\u0130on\":99}")), noVersion),

            // Nesting as deep as Go's decoder takes it.
            ["a member nested 100 deep"] = (Connects(After("\"x\":" + nested)), null),

            // Bytes that are not UTF-8 are read as U+FFFD.
            ["not UTF-8 in a member nobody reads"] = (Connects(After("\"x\":\"\u00ff\""), latin1: true), null),
            ["not UTF-8 in a member's name"] = (Connects(After("\"\u00ff\":1"), latin1: true), null),
            ["not UTF-8 in a skipped notification's method"] =
                (Connects(c => Line("{\"jsonrpc\":\"2.0\",\"method\":\"notify.\u00ff\"}") + Answer(1, c.RightResult), latin1: true), null),
            ["not UTF-8 in daemonNonce"] =
                (Connects(c => Answer(1, c.RightResult.Replace("\"daemonNonce\":\"", "\"daemonNonce\":\"\u00ff", StringComparison.Ordinal)), latin1: true),
                    HandshakeError.Malformed("daemonNonce is not 64 lowercase hex digits")),
            ["not UTF-8 in jsonrpc"] =
                (Connects(c => Line("{\"jsonrpc\":\"2.\u00ff\",\"id\":1,\"result\":" + c.RightResult + "}"), latin1: true), notJsonRpc),
            ["not UTF-8 in the id"] =
                (Connects(c => Line("{\"jsonrpc\":\"2.0\",\"id\":\"\u00ff\",\"result\":" + c.RightResult + "}"), latin1: true),
                    HandshakeError.Malformed("an answer without the request's id")),
        };
    }

    private static Dictionary<string, FailCase> FailCases()
    {
        static Func<HelloContext, string?> Set(string key, JsonNode? value) => HelloWith((_, r) => r[key] = value);
        static Func<HelloContext, string?> Upper(string key) => HelloWith((_, r) => r[key] = r[key]!.GetValue<string>().ToUpperInvariant());
        Func<HelloContext, string?> rightHello = ctx => Answer(1, ctx.RightResult);
        var big = Line("""{"jsonrpc":"2.0","method":"notify.big","params":{"p":""" + "\"" + new string('a', 64 << 10) + "\"}}");
        FailCase[] cases =
        [
            // Other protocol versions: nothing is sent after system.hello, and
            // the key file is not read.
            new("protocol 1", Hello(_ => ErrorAnswer(1, ErrorCode.MethodNotFound, "unknown method \\\"system.hello\\\"")),
                KeyFile.Keep, Refused(HandshakeError.ProtocolMismatch(1)), HelloOnly),
            new("protocol 1 after 8 notifications",
                Hello(_ => string.Concat(Enumerable.Repeat(TestNotification, 8)) + ErrorAnswer(1, ErrorCode.MethodNotFound, "unknown method")),
                KeyFile.Keep, Refused(HandshakeError.ProtocolMismatch(1)), HelloOnly),
            new("protocol 99 without a key file", Hello(_ => Answer(1, """{"protocolVersion":99}""")),
                KeyFile.Remove, Refused(HandshakeError.ProtocolMismatch(99)), HelloOnly),
            new("protocol 3 with another result", Hello(_ => Answer(1, """{"protocolVersion":3,"daemonNonce":{"bytes":32}}""")),
                KeyFile.Remove, Refused(HandshakeError.ProtocolMismatch(3)), HelloOnly),

            // The key file.
            new("missing key file", Hello(rightHello), KeyFile.Remove, new Outcome.KeyUnavailable("does not exist"), HelloOnly),
            new("empty key file", Hello(rightHello), KeyFile.Empty, new Outcome.KeyUnavailable("is not 65 bytes"), HelloOnly),
            new("upper-case key file", Hello(rightHello), KeyFile.Uppercase, new Outcome.KeyUnavailable("is not a key file"), HelloOnly),
            new("key file with CRLF", Hello(rightHello), KeyFile.Crlf, new Outcome.KeyUnavailable("is not 65 bytes"), HelloOnly),

            // Proofs that are not the daemon's: authenticate is never sent.
            new("another key", Hello(HelloWith((c, r) => r["daemonProof"] = RpcAuth.Hex(RpcAuth.DaemonProof(OtherKey, c.ClientNonce, c.DaemonNonce)))),
                KeyFile.Keep, Refused(HandshakeError.DaemonUnproven), HelloOnly),
            new("reflected proof", Hello(HelloWith((c, r) => r["daemonProof"] = RpcAuth.Hex(RpcAuth.ClientProof(c.Key, c.ClientNonce, c.DaemonNonce)))),
                KeyFile.Keep, Refused(HandshakeError.DaemonUnproven), HelloOnly),
            new("swapped nonces", Hello(HelloWith((c, r) => r["daemonProof"] = RpcAuth.Hex(RpcAuth.DaemonProof(c.Key, c.DaemonNonce, c.ClientNonce)))),
                KeyFile.Keep, Refused(HandshakeError.DaemonUnproven), HelloOnly),
            new("zero proof", Hello(Set("daemonProof", new string('0', 64))), KeyFile.Keep, Refused(HandshakeError.DaemonUnproven), HelloOnly),

            // Malformed system.hello answers.
            new("upper-case daemonNonce", Hello(Upper("daemonNonce")), KeyFile.Keep, Malformed("daemonNonce is not 64 lowercase hex digits"), HelloOnly),
            new("upper-case daemonProof", Hello(Upper("daemonProof")), KeyFile.Keep, Malformed("daemonProof is not 64 lowercase hex digits"), HelloOnly),
            new("short daemonNonce", Hello(HelloWith((_, r) => r["daemonNonce"] = r["daemonNonce"]!.GetValue<string>()[..62])),
                KeyFile.Keep, Malformed("daemonNonce is not 64 lowercase hex digits"), HelloOnly),
            new("missing daemonProof", Hello(HelloWith((_, r) => r.Remove("daemonProof"))),
                KeyFile.Keep, Malformed("daemonProof is not 64 lowercase hex digits"), HelloOnly),
            new("daemonProof a number", Hello(Set("daemonProof", 5)), KeyFile.Keep, Malformed("the system.hello result does not decode"), HelloOnly),
            new("missing protocolVersion", Hello(HelloWith((_, r) => r.Remove("protocolVersion"))),
                KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("protocolVersion 0", Hello(Set("protocolVersion", 0)), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("negative protocolVersion", Hello(Set("protocolVersion", -2)), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("protocolVersion a string", Hello(Set("protocolVersion", "2")), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("protocolVersion a fraction", Hello(Set("protocolVersion", 2.5)), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("result null", Hello(_ => Line("""{"jsonrpc":"2.0","id":1,"result":null}""")), KeyFile.Keep, Malformed("an answer without a result"), HelloOnly),
            new("result an array", Hello(_ => Answer(1, "[2]")), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("result a string", Hello(_ => Answer(1, "\"ok\"")), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("result a number", Hello(_ => Answer(1, "2")), KeyFile.Keep, Malformed("the system.hello result has no valid protocolVersion"), HelloOnly),
            new("result and error", Hello(c => Line($$$"""{"jsonrpc":"2.0","id":1,"result":{{{c.RightResult}}},"error":{"code":1005,"message":"no"}}""")),
                KeyFile.Keep, Malformed("an answer with both a result and an error"), HelloOnly),
            new("neither result nor error", Hello(_ => Line("""{"jsonrpc":"2.0","id":1}""")), KeyFile.Keep, Malformed("an answer without a result"), HelloOnly),
            new("wrong id", Hello(c => Line($$"""{"jsonrpc":"2.0","id":7,"result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("an answer without the request's id"), HelloOnly),
            new("id a string", Hello(c => Line($$"""{"jsonrpc":"2.0","id":"1","result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("an answer without the request's id"), HelloOnly),
            new("missing id", Hello(c => Line($$"""{"jsonrpc":"2.0","result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("an answer without the request's id"), HelloOnly),
            new("null id", Hello(c => Line($$"""{"jsonrpc":"2.0","id":null,"result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("an answer without the request's id"), HelloOnly),
            new("no jsonrpc member", Hello(c => Line($$"""{"id":1,"result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("JSON-RPC 1.0", Hello(c => Line($$"""{"jsonrpc":"1.0","id":1,"result":{{c.RightResult}}}""")),
                KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("a request", Hello(_ => Line("""{"jsonrpc":"2.0","id":1,"method":"system.hello","params":{}}""")),
                KeyFile.Keep, Malformed("a request instead of an answer"), HelloOnly),
            new("a JSON array", Hello(c => Line($$"""[{"jsonrpc":"2.0","id":1,"result":{{c.RightResult}}}]""")),
                KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("not JSON", Hello(_ => Line("hello")), KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("JSON null", Hello(_ => Line("null")), KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("an empty line", Hello(_ => Line("")), KeyFile.Keep, Malformed("a line is not a JSON-RPC 2.0 message"), HelloOnly),
            new("an empty message", Hello(_ => Line("""{"jsonrpc":"2.0"}""")), KeyFile.Keep, Malformed("an answer without the request's id"), HelloOnly),
            new("9 notifications", Hello(c => string.Concat(Enumerable.Repeat(TestNotification, 9)) + Answer(1, c.RightResult)),
                KeyFile.Keep, Malformed("an unexpected notification"), HelloOnly),
            new("a line over 64 KiB", Hello(c => big + Answer(1, c.RightResult)), KeyFile.Keep, Malformed("a line is longer than 64 KiB"), HelloOnly),

            // Answers other than success.
            new("system.hello rejected", Hello(_ => ErrorAnswer(1, ErrorCode.Unauthenticated, "no")),
                KeyFile.Keep, Refused(HandshakeError.Rejected(ErrorCode.Unauthenticated)), HelloOnly),
            new("system.hello invalidRequest", Hello(_ => ErrorAnswer(1, ErrorCode.InvalidRequest, "already authenticated")),
                KeyFile.Keep, Refused(HandshakeError.Rejected(ErrorCode.InvalidRequest)), HelloOnly),
            // The message is dropped; it quotes the client's nonce to show that.
            new("system.authenticate rejected", Authenticated(n => ErrorAnswer(2, ErrorCode.Unauthenticated, "wrong proof for " + n)),
                KeyFile.Keep, Refused(HandshakeError.Rejected(ErrorCode.Unauthenticated)), Both),
            new("notification before the system.authenticate answer", Authenticated(_ => TestNotification + Answer(2, "{}")),
                KeyFile.Keep, Malformed("an unexpected notification"), Both),
            new("system.authenticate result null", Authenticated(_ => Line("""{"jsonrpc":"2.0","id":2,"result":null}""")),
                KeyFile.Keep, Malformed("an answer without a result"), Both),
            new("system.authenticate without a result", Authenticated(_ => Line("""{"jsonrpc":"2.0","id":2}""")),
                KeyFile.Keep, Malformed("an answer without a result"), Both),
            new("system.authenticate result not an object", Authenticated(_ => Answer(2, "true")),
                KeyFile.Keep, Malformed("the system.authenticate result is not an object"), Both),
            new("system.authenticate answered with id 1", Authenticated(_ => Answer(1, "{}")),
                KeyFile.Keep, Malformed("an answer without the request's id"), Both),

            // The connection ends (droppedScripts): a plain error, handled like
            // any dropped connection.
            new("closed at once", null, KeyFile.Keep, new Outcome.Broken(), []),
            new("closed after hello", new HandshakeScript { Hello = _ => null, CloseAfterHello = true },
                KeyFile.Keep, new Outcome.Broken(), HelloOnly),
            new("closed mid-answer", new HandshakeScript { Hello = _ => """{"jsonrpc":"2.0","id":1,"res""", CloseAfterHello = true },
                KeyFile.Keep, new Outcome.Broken(), HelloOnly),
            new("closed after authenticate", new HandshakeScript { Hello = rightHello, CloseAfterAuthenticate = true },
                KeyFile.Keep, new Outcome.Broken(), Both),

            // Silence (TestHandshakeTimesOut).
            new("silent after hello", Hello(_ => null), KeyFile.Keep, Refused(HandshakeError.TimedOut), HelloOnly),
            new("silent after authenticate", Hello(rightHello), KeyFile.Keep, Refused(HandshakeError.TimedOut), Both),
        ];
        return cases.ToDictionary(c => c.Name);
    }

    private static HandshakeScript Hello(Func<HelloContext, string?> hello) => new() { Hello = hello };

    // Answers system.hello rightly; what the script writes for the right
    // system.authenticate is the case.
    private static HandshakeScript Authenticated(Func<string, string?> authenticate) =>
        new() { Hello = ctx => Answer(1, ctx.RightResult), Authenticate = authenticate };

    // The right result as edit changes it (Go's helloWith).
    private static Func<HelloContext, string?> HelloWith(Action<HelloContext, JsonObject> edit) => ctx =>
    {
        var result = JsonNode.Parse(ctx.RightResult)!.AsObject();
        edit(ctx, result);
        return Answer(1, result.ToJsonString());
    };

    private static Outcome.Refused Refused(HandshakeError error) => new(error);

    private static Outcome.Refused Malformed(string detail) => new(HandshakeError.Malformed(detail));

    /// <summary>A line as the daemon writes it.</summary>
    private static string Line(string s) => s + "\n";

    /// <summary>The answer to the request <paramref name="id"/> with this result.</summary>
    private static string Answer(int id, string result) => Line($$"""{"jsonrpc":"2.0","id":{{id}},"result":{{result}}}""");

    /// <summary>The error answer to the request <paramref name="id"/>.</summary>
    private static string ErrorAnswer(int id, int code, string message) =>
        Line($$$"""{"jsonrpc":"2.0","id":{{{id}}},"error":{"code":{{{code}}},"message":"{{{message}}}"}}""");

    /// <summary>Changes the key file as a case asks, so that only its content or its absence is the problem.</summary>
    private static async Task EditAsync(KeyFile keyFile, string path)
    {
        switch (keyFile)
        {
            case KeyFile.Remove:
                File.Delete(path);
                return;
            case KeyFile.Empty:
                await File.WriteAllBytesAsync(path, [], Ct);
                return;
            case KeyFile.Uppercase:
                await File.WriteAllTextAsync(path, (await File.ReadAllTextAsync(path, Ct)).ToUpperInvariant(), Ct);
                return;
            case KeyFile.Crlf:
                await File.WriteAllTextAsync(path, (await File.ReadAllTextAsync(path, Ct))[..64] + "\r\n", Ct);
                return;
            default:
                return;
        }
    }

    /// <summary>
    /// What no text may show: the fake's keys, nonces and proofs, and the
    /// other run's key with its proofs for this connection's nonces (Go's
    /// fakeDaemon.secrets).
    /// </summary>
    private static List<string> SecretsOf(FakeDaemon fake)
    {
        var secrets = fake.Secrets.ToList();
        secrets.Add(RpcAuth.Hex(OtherKey));
        if (secrets.Count >= 3 && RpcAuth.DecodeHex32(secrets[1]) is { } cn && RpcAuth.DecodeHex32(secrets[2]) is { } dn)
        {
            secrets.Add(RpcAuth.Hex(RpcAuth.DaemonProof(OtherKey, cn, dn)));
            secrets.Add(RpcAuth.Hex(RpcAuth.ClientProof(OtherKey, cn, dn)));
            secrets.Add(RpcAuth.Hex(RpcAuth.DaemonProof(fake.Key, dn, cn)));
        }
        return secrets;
    }

    /// <summary>Every way a failure prints: its message, its full text with its causes, and the client's state.</summary>
    private static List<string> Texts(Exception error, RpcClientState state)
    {
        List<string> texts = [state.ToString(), error.ToString()];
        for (var e = error; e is not null; e = e.InnerException)
        {
            texts.Add(e.Message);
        }
        return texts;
    }

    private static void ExpectNoSecrets(IEnumerable<string> texts, IEnumerable<string> secrets, string name)
    {
        foreach (var text in texts)
        {
            var lower = text.ToLowerInvariant();
            foreach (var secret in secrets)
            {
                Assert.False(lower.Contains(secret, StringComparison.Ordinal), $"{name}: \"{text}\" shows a key, a nonce or a proof");
            }
        }
    }

    /// <summary>One case of the table; a null script is a daemon that closes the connection at once.</summary>
    private sealed record FailCase(string Name, HandshakeScript? RawScript, KeyFile KeyFile, Outcome Outcome, string[] Sent)
    {
        public HandshakeMode Mode => RawScript is null ? new HandshakeMode.Hangup() : new HandshakeMode.Raw(RawScript);

        public HandshakeScript Script => RawScript ?? new HandshakeScript { Hello = _ => null };
    }

    /// <summary>How a case ends, as Go's table says.</summary>
    private abstract record Outcome
    {
        public abstract bool Matches(Exception error);

        /// <summary>ConnectAsync refused the daemon with this error.</summary>
        public sealed record Refused(HandshakeError Error) : Outcome
        {
            public override bool Matches(Exception error) => error is HandshakeException h && h.Error == Error;
        }

        /// <summary>KeyUnavailable, its reason ending in this text (the path comes first).</summary>
        public sealed record KeyUnavailable(string Ending) : Outcome
        {
            public override bool Matches(Exception error) =>
                error is HandshakeException { Error: { Reason: HandshakeReason.KeyUnavailable, Detail: { } detail } }
                && detail.EndsWith(Ending, StringComparison.Ordinal);
        }

        /// <summary>The connection broke: a transport error, where Go has a plain error.</summary>
        public sealed record Broken : Outcome
        {
            public override bool Matches(Exception error) => error is RpcClientException { Error.Kind: ClientErrorKind.Transport };
        }
    }
}

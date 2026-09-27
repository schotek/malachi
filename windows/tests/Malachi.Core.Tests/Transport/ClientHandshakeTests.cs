// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/client/handshake_test.go (TestConnectAuthenticates,
// TestConnectKeepsWhatFollowsTheHandshake, TestConnectRefusals,
// TestConnectRereadsTheKey, TestConnectWithoutDaemon, TestDaemonProtocol),
// which the Swift suite did not port, against the C# FakeDaemon. The GTK
// client reports Connecting before its dial; RpcClient probes first, as the
// macOS client does, so a socket without a daemon reports Disconnected only
// (TestConnectWithoutDaemon). ui/internal/client/client_test.go
// (TestDefaultSocketPath) is the daemon paths' test, not the transport's.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json.Nodes;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class ClientHandshakeTests
{
    private static readonly string[] HelloOnly = [API.SystemHello.Name];

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    public static TheoryData<string> RefusalNames => [.. Refusals().Keys];

    [Fact]
    public async Task ConnectAuthenticates()
    {
        await using var d = await StartFakeAsync(new HandshakeMode.Normal());
        using var c = new RpcClient(d.Path, PortableKeyFilePolicy.Instance);
        var events = Record(c);
        await c.ConnectAsync(Ct);
        Assert.Equal(new RpcClientState.Connected(), c.State);
        Assert.Equal([new RpcClientState.Connecting(), new RpcClientState.Connected()], Snapshot(events));

        var res = await c.CallAsync(TestMethods.EchoedSystemInfo, new EmptyParams(), Ct);
        Assert.Equal(API.SystemInfoName, res.Method);
        Assert.Equal([API.SystemHello.Name, API.SystemAuthenticate.Name, API.SystemInfoName], d.Received);

        c.Close();
        await d.WaitForEndedAsync(1);
        Assert.Equal(new RpcClientState.Disconnected(null), Snapshot(events)[^1]);
    }

    /// <summary>
    /// The daemon may send a notification right after the system.authenticate
    /// answer, in the same write; the handshake leaves it in the reader, and
    /// the read loop must deliver it.
    /// </summary>
    [Fact]
    public async Task ConnectKeepsWhatFollowsTheHandshake()
    {
        await using var d = await StartFakeAsync(new HandshakeMode.Normal());
        d.SetNotificationWithAuthenticateAnswer(API.Notify.AccountsChanged);
        using var c = new RpcClient(d.Path, PortableKeyFilePolicy.Instance);
        await c.ConnectAsync(Ct);
        var n = await c.Notifications.ReadAsync(Ct).AsTask().WaitAsync(TimeSpan.FromSeconds(5), Ct);
        Assert.Equal(API.Notify.AccountsChanged, n.Method);
    }

    [Theory]
    [MemberData(nameof(RefusalNames))]
    public async Task ConnectRefusals(string name)
    {
        var tc = Refusals()[name];
        await using var d = await StartFakeAsync(tc.Mode, tc.EarlyNotifications);
        if (tc.NoKey)
        {
            File.Delete(d.KeyPath);
        }
        var time = new FakeTimeProvider();
        using var c = new RpcClient(d.Path, PortableKeyFilePolicy.Instance, timeProvider: time);
        var events = Record(c);
        var notes = new List<string>();
        c.NotificationReceived += (_, n) =>
        {
            lock (notes)
            {
                notes.Add(n.Method);
            }
        };
        var connect = c.ConnectAsync(Ct);
        if (tc.Silent)
        {
            await Eventually.Holds(() => d.Received.Count == 1);
            time.Advance(TimeSpan.FromMilliseconds(200) + Malachi.Core.Platform.RpcTimeouts.Handshake);
        }
        var err = await Assert.ThrowsAnyAsync<Exception>(() => connect);

        Assert.IsType<RpcClientState.Disconnected>(c.State);
        var got = Snapshot(events);
        Assert.Equal(2, got.Count);
        Assert.Equal(new RpcClientState.Connecting(), got[0]);
        Assert.Equal(new RpcClientState.Disconnected(err.Message), got[1]);
        var he = Assert.IsType<HandshakeException>(err);
        Assert.Equal(tc.Reason, he.Error.Reason);
        Assert.Equal(tc.Code, he.Error.Code);
        Assert.Equal(tc.Daemon, HandshakeError.DaemonProtocol(err));
        if (tc.NoKey && tc.Reason == HandshakeReason.KeyUnavailable)
        {
            Assert.IsType<FileNotFoundException>(he.InnerException?.InnerException);
        }

        await d.WaitForEndedAsync(1);
        Assert.Equal(tc.Sent, d.Received);
        foreach (var s in d.Secrets)
        {
            Assert.DoesNotContain(s, err.Message, StringComparison.OrdinalIgnoreCase);
        }
        lock (notes)
        {
            Assert.Empty(notes); // notifications of a refused daemon never reach the UI
        }
    }

    /// <summary>A restarted daemon has a new key: ConnectAsync reads the key file again.</summary>
    [Fact]
    public async Task ConnectRereadsTheKey()
    {
        var sock = FakeDaemon.NewSocketPath();
        var first = await StartFakeAsync(new HandshakeMode.Normal(), path: sock);
        using var c = new RpcClient(sock, PortableKeyFilePolicy.Instance);
        await c.ConnectAsync(Ct);
        c.Close();
        var firstKey = first.Key;
        await first.StopAsync();

        await using var second = await StartFakeAsync(new HandshakeMode.Normal(), path: sock);
        Assert.NotEqual(firstKey, second.Key);
        await c.ConnectAsync(Ct);
        await c.CallAsync(TestMethods.EchoedSystemInfo, new EmptyParams(), Ct);
        Assert.Equal([API.SystemHello.Name, API.SystemAuthenticate.Name, API.SystemInfoName], second.Received);
    }

    /// <summary>Without a daemon the probe's refusal is reported as it is, and is no handshake's.</summary>
    [Fact]
    public async Task ConnectWithoutDaemon()
    {
        using var c = new RpcClient(FakeDaemon.NewSocketPath(), PortableKeyFilePolicy.Instance);
        var events = Record(c);
        var err = await Assert.ThrowsAsync<RpcClientException>(() => c.ConnectAsync(Ct));
        Assert.Equal(0, HandshakeError.DaemonProtocol(err));
        Assert.Equal([new RpcClientState.Disconnected(err.Message)], Snapshot(events));
    }

    [Fact]
    public void DaemonProtocol()
    {
        (Exception? Error, int Want)[] cases =
        [
            (null, 0),
            (new IOException("dial unix rpc.sock: connect: no such file or directory"), 0),
            (new HandshakeException(HandshakeError.ProtocolMismatch(1)), 1),
            (new InvalidOperationException("connect", new HandshakeException(HandshakeError.ProtocolMismatch(3))), 3),
            (new AggregateException(new HandshakeException(HandshakeError.ProtocolMismatch(4))), 4),
            (new HandshakeException(HandshakeError.DaemonUnproven), 0),
            (new HandshakeException(HandshakeError.Rejected(ErrorCode.Unauthenticated)), 0),
            (new HandshakeException(HandshakeError.TimedOut), 0), // the daemon counts for a mismatch only
            (new RpcException(new RpcError { Code = ErrorCode.MethodNotFound, Message = "unknown method" }), 0), // a call's error is no mismatch
        ];
        foreach (var (error, want) in cases)
        {
            Assert.Equal(want, HandshakeError.DaemonProtocol(error));
        }
    }

    private static Dictionary<string, RefusalCase> Refusals()
    {
        RefusalCase[] cases =
        [
            new("daemon of protocol 1", new HandshakeMode.OldDaemon(), HandshakeReason.ProtocolMismatch, HelloOnly, Daemon: 1, EarlyNotifications: 1),
            new("daemon of a later protocol", new HandshakeMode.ProtocolVersion(API.ProtocolVersion + 1), HandshakeReason.ProtocolMismatch, HelloOnly,
                Daemon: API.ProtocolVersion + 1, NoKey: true),
            new("daemon with another key", new HandshakeMode.WrongProof(), HandshakeReason.DaemonUnproven, HelloOnly),
            new("client proof rejected", new HandshakeMode.RejectClient(), HandshakeReason.Rejected,
                [API.SystemHello.Name, API.SystemAuthenticate.Name], Code: ErrorCode.Unauthenticated),
            new("no key file", new HandshakeMode.Normal(), HandshakeReason.KeyUnavailable, HelloOnly, NoKey: true),
            new("malformed answer", new HandshakeMode.Raw(new HandshakeScript
            {
                // garbles: a daemonNonce that is not one.
                Hello = ctx =>
                {
                    var r = JsonNode.Parse(ctx.RightResult)!.AsObject();
                    r["daemonNonce"] = "zz";
                    return $$"""{"jsonrpc":"2.0","id":1,"result":{{r.ToJsonString()}}}""" + "\n";
                },
            }), HandshakeReason.Malformed, HelloOnly),
            new("silent daemon", new HandshakeMode.Silent(), HandshakeReason.TimedOut, HelloOnly, Silent: true),
        ];
        return cases.ToDictionary(c => c.Name);
    }

    private static async Task<FakeDaemon> StartFakeAsync(HandshakeMode mode, int earlyNotifications = 0, string? path = null)
    {
        // authOK: every call is answered with its method.
        var d = new FakeDaemon((method, _) => Task.FromResult(FakeAnswer.Result($$"""{"method":"{{method}}"}""")), path);
        d.SetHandshake(mode);
        d.SetNotificationsBeforeHello(earlyNotifications);
        await d.StartAsync();
        return d;
    }

    private static List<RpcClientState> Record(RpcClient c)
    {
        var events = new List<RpcClientState>();
        c.StateChanged += (_, s) =>
        {
            lock (events)
            {
                events.Add(s);
            }
        };
        return events;
    }

    private static List<RpcClientState> Snapshot(List<RpcClientState> events)
    {
        lock (events)
        {
            return [.. events];
        }
    }

    private sealed record RefusalCase(
        string Name,
        HandshakeMode Mode,
        HandshakeReason Reason,
        string[] Sent,
        int Daemon = 0,
        ErrorCode Code = default,
        bool NoKey = false,
        bool Silent = false,
        int EarlyNotifications = 0);
}

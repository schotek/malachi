// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/RPCClientTests.swift, against the C#
// FakeDaemon on a real AF_UNIX socket.
//
// Where Swift sleeps, these tests wait for something that happens: the slow
// handler answers after the fast one was served (a gate, not 300 ms); the
// timeouts run on a FakeTimeProvider that the test advances; "nothing was
// sent after system.hello" is read once the client closed its connection
// (FakeDaemon.WaitForEndedAsync), not after 100 ms. The last tests are the
// C# client's own (a line over the cap, Dispose, a cancelled dial, a
// throwing handler).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class RpcClientTests
{
    private const string SystemInfoJson = """{"version":"fake","protocolVersion":2,"pid":42,"storePath":"/tmp/store.db"}""";

    private static readonly SystemInfoResult FakeInfo = new() { Version = "fake", ProtocolVersion = 2, Pid = 42, StorePath = "/tmp/store.db" };

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task SystemInfoRoundTrip()
    {
        await using var fake = await StartFakeAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(Ct);
        Assert.Equal(new RpcClientState.Connected(), client.State);
        var info = await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal(FakeInfo, info);
        Assert.True(UnixSocketProbe.Answers(fake.Path));
        client.Close();
        Assert.Equal(new RpcClientState.Disconnected(null), client.State);
    }

    [Fact]
    public async Task DaemonErrorArrivesAsRpcError()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var e = await Assert.ThrowsAsync<RpcException>(() => client.CallAsync(TestMethods.Nope, new EmptyParams(), Ct));
        Assert.Equal(new RpcError { Code = -32601, Message = "unknown method nope" }, e.Error);
    }

    [Fact]
    public async Task OutOfOrderRepliesAreMatchedById()
    {
        var fastServed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await using var fake = await StartFakeAsync(async (method, line) =>
        {
            switch (method)
            {
                case "test.slow":
                    await fastServed.Task;
                    return FakeAnswer.Result("""{"which":"slow"}""");
                case "test.fast":
                    fastServed.SetResult();
                    return FakeAnswer.Result("""{"which":"fast"}""");
                default:
                    return await Standard(method, line);
            }
        });
        using var client = await ConnectedAsync(fake);
        var slow = client.CallAsync(TestMethods.Slow, new EmptyParams(), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        var fast = client.CallAsync(TestMethods.Fast, new EmptyParams(), Ct);
        Assert.Equal(new Which("fast"), await fast);
        Assert.Equal(new Which("slow"), await slow);
    }

    [Fact]
    public async Task SilenceTimesOut()
    {
        await using var fake = await StartFakeAsync();
        var time = new FakeTimeProvider();
        using var client = await ConnectedAsync(fake, time: time);
        var call = client.CallAsync(TestMethods.Never, new EmptyParams(), TimeSpan.FromMilliseconds(200), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        time.Advance(TimeSpan.FromMilliseconds(199));
        Assert.False(call.IsCompleted);
        time.Advance(TimeSpan.FromMilliseconds(1));
        var e = await Assert.ThrowsAsync<RpcClientException>(() => call);
        Assert.Equal(ClientError.Timeout("test.never"), e.Error);
        Assert.Equal("test.never timed out", e.Message);
        // The connection itself is unharmed.
        var info = await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal(42, info.Pid);
    }

    [Fact]
    public async Task NotificationsAreDelivered()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        // The fake notifies authenticated connections only, and a connected
        // client is one it has authenticated; a round trip first all the same.
        await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        var received = new TaskCompletionSource<RpcNotification>(TaskCreationOptions.RunContinuationsAsynchronously);
        client.NotificationReceived += (_, n) => received.TrySetResult(n);
        await fake.PushNotificationAsync("notify.accountsChanged", "{}");
        var n = await received.Task.WaitAsync(TimeSpan.FromSeconds(5), Ct);
        Assert.Equal("notify.accountsChanged", n.Method);
        n.Params(TransportTestJson.Default.AnyObject);
        Assert.IsType<DaemonNotification.AccountsChanged>(n.Decode());
    }

    [Fact]
    public async Task ServerCloseFailsPendingCallAndDisconnects()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var states = new List<RpcClientState>();
        client.StateChanged += (_, s) =>
        {
            lock (states)
            {
                states.Add(s);
            }
        };
        var pending = client.CallAsync(TestMethods.Never, new EmptyParams(), TimeSpan.FromSeconds(10), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        fake.CloseAll();
        var e = await Assert.ThrowsAsync<RpcClientException>(() => pending);
        Assert.Equal(ClientError.Disconnected, e.Error);
        await Eventually.Holds(() => client.State is RpcClientState.Disconnected);
        var disconnected = Assert.IsType<RpcClientState.Disconnected>(client.State);
        Assert.NotNull(disconnected.Reason);
        lock (states)
        {
            Assert.Contains(states, s => s is RpcClientState.Disconnected);
        }
    }

    [Fact]
    public async Task StatesStreamReportsTransitionsInOrder()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        client.Close();
        // Buffered: the consumer may start late and still see everything.
        var seen = new List<RpcClientState>();
        await foreach (var s in client.States.ReadAllAsync(Ct))
        {
            seen.Add(s);
            if (seen.Count == 3)
            {
                break;
            }
        }
        Assert.Equal([new RpcClientState.Connecting(), new RpcClientState.Connected(), new RpcClientState.Disconnected(null)], seen);
    }

    [Fact]
    public async Task NotificationsStreamPreservesOrder()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        for (var i = 1; i <= 20; i++)
        {
            await fake.PushNotificationAsync($"notify.n{i}", $$"""{"i":{{i}}}""");
        }
        var methods = new List<string>();
        await foreach (var n in client.Notifications.ReadAllAsync(Ct))
        {
            methods.Add(n.Method);
            if (methods.Count == 20)
            {
                break;
            }
        }
        Assert.Equal(Enumerable.Range(1, 20).Select(i => $"notify.n{i}"), methods);
    }

    [Fact]
    public async Task PerMethodHandlersSeeParams()
    {
        await using var fake = new FakeDaemon();
        fake.On("test.echo", p =>
        {
            var x = System.Text.Json.JsonSerializer.Deserialize(p, TransportTestJson.Default.XParams)!;
            return $$"""{"which":"x={{x.X}}"}""";
        });
        fake.On("test.fail", string (string _) => throw new RpcException(new RpcError { Code = 1001, Message = "no" }));
        await fake.StartAsync();
        using var client = await ConnectedAsync(fake);
        Assert.Equal(new Which("x=5"), await client.CallAsync(TestMethods.Echo, new XParams(5), Ct));
        var failed = await Assert.ThrowsAsync<RpcException>(() => client.CallAsync(TestMethods.Fail, new EmptyParams(), Ct));
        Assert.Equal(new RpcError { Code = 1001, Message = "no" }, failed.Error);
        var unknown = await Assert.ThrowsAsync<RpcException>(() => client.CallAsync(TestMethods.Nope, new EmptyParams(), Ct));
        Assert.Equal(new RpcError { Code = FakeDaemon.MethodNotFoundCode, Message = "unknown method nope" }, unknown.Error);
        Assert.Equal(["test.echo", "test.fail", "nope"], fake.Calls);
    }

    [Fact]
    public async Task CancellationFailsThePendingCall()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        using var cancel = new CancellationTokenSource();
        var pending = client.CallAsync(TestMethods.Never, new EmptyParams(), TimeSpan.FromSeconds(10), cancel.Token);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        cancel.Cancel();
        var e = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => pending);
        Assert.Equal(cancel.Token, e.CancellationToken);
        // The connection itself is unharmed.
        var info = await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal(42, info.Pid);
        Assert.Equal(new RpcClientState.Connected(), client.State);
    }

    [Fact]
    public async Task AlreadyCancelledTaskDoesNotSend()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        using var cancel = new CancellationTokenSource();
        cancel.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.CallAsync(TestMethods.Fast, new EmptyParams(), cancel.Token));
        await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal([API.SystemInfoName], fake.Calls);
    }

    [Fact]
    public async Task DeadPathFailsPromptly()
    {
        var path = FakeDaemon.NewSocketPath();
        using var client = new RpcClient(path, PortableKeyFilePolicy.Instance);
        var started = DateTime.UtcNow;
        var e = await Assert.ThrowsAsync<RpcClientException>(() => client.ConnectAsync(Ct));
        Assert.True(DateTime.UtcNow - started < TimeSpan.FromSeconds(3), "a refused unix connect must not hang");
        Assert.Equal(ClientError.Transport($"nothing listens on {path}"), e.Error);
        Assert.False(UnixSocketProbe.Answers(path));
        Assert.IsType<RpcClientState.Disconnected>(client.State);
    }

    // MARK: Handshake (docs/api.md §1.4; api handshake_test.go)

    [Fact]
    public async Task HandshakePrecedesTheFirstCall()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        Assert.Equal(new RpcClientState.Connected(), client.State);
        Assert.Equal([API.SystemHello.Name, API.SystemAuthenticate.Name], fake.Handshakes);
        await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal([API.SystemHello.Name, API.SystemAuthenticate.Name, API.SystemInfoName], fake.Received);
        Assert.Equal([API.SystemInfoName], fake.Calls); // the handshake is not a call
    }

    [Fact]
    public async Task CallsWaitForTheHandshake()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.Silent());
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, handshakeTimeout: TimeSpan.FromSeconds(10));
        var dial = client.ConnectAsync(Ct);
        await Eventually.Holds(() => fake.Handshakes.SequenceEqual([API.SystemHello.Name]));
        Assert.Equal(new RpcClientState.Connecting(), client.State); // connecting until the handshake is done
        var refused = await Assert.ThrowsAsync<RpcClientException>(() => client.CallAsync(API.SystemInfo, new EmptyParams(), Ct));
        Assert.Equal(ClientError.NotConnected, refused.Error);
        client.Close();
        var dropped = await Assert.ThrowsAsync<RpcClientException>(() => dial);
        Assert.Equal(ClientError.Disconnected, dropped.Error);
        await fake.WaitForEndedAsync(1);
        Assert.Empty(fake.Calls);
        Assert.Equal([API.SystemHello.Name], fake.Received);
    }

    [Fact]
    public async Task NotificationsBeforeTheHelloAnswerAreDropped()
    {
        await using var fake = await StartFakeAsync();
        fake.SetNotificationsBeforeHello(8);
        using var client = await ConnectedAsync(fake);
        await fake.PushNotificationAsync("notify.late", "{}");
        var first = await client.Notifications.ReadAsync(Ct);
        // The eight notify.early before the answer never reach the consumer.
        Assert.Equal("notify.late", first.Method);
    }

    [Fact]
    public async Task ANinthNotificationBeforeTheHelloAnswerIsMalformed()
    {
        await using var fake = await StartFakeAsync();
        fake.SetNotificationsBeforeHello(9);
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        Assert.Equal(HandshakeError.Malformed("an unexpected notification"), await RefusalAsync(client));
        Assert.Equal([API.SystemHello.Name], fake.Handshakes);
    }

    /// <summary>
    /// The system.authenticate answer and a notification in one write: the
    /// notification is held and delivered right after Connected.
    /// </summary>
    [Fact]
    public async Task ANotificationWithTheAuthenticateAnswerFollowsConnected()
    {
        await using var fake = await StartFakeAsync();
        fake.SetNotificationWithAuthenticateAnswer("notify.first");
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        var events = new Events(client);
        await client.ConnectAsync(Ct);
        await Eventually.Holds(() => events.All.Count >= 3);
        Assert.Equal(["state Connecting", "state Connected", "notification notify.first"], events.All);
    }

    [Fact]
    public async Task ADaemonOfProtocolOneIsAMismatch()
    {
        foreach (var early in new[] { 0, RpcAuth.MaxSkippedNotifications })
        {
            await using var fake = await StartFakeAsync();
            fake.SetHandshake(new HandshakeMode.OldDaemon());
            fake.SetNotificationsBeforeHello(early);
            using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
            // Both ways a consumer gets notifications: the handler and the stream.
            var events = new Events(client);
            var refused = await RefusalAsync(client);
            Assert.Equal(HandshakeError.ProtocolMismatch(1), refused);
            Assert.Equal(new RpcClientState.Disconnected(refused!.ToString()), client.State);
            // Nothing after system.hello: no falling back to an
            // unauthenticated connection.
            await fake.WaitForEndedAsync(1);
            Assert.Equal([API.SystemHello.Name], fake.Received);
            // The notifications before the answer never reach the consumer.
            Assert.False(client.Notifications.TryRead(out _), $"{early} notifications first");
            Assert.DoesNotContain(events.All, e => e.StartsWith("notification", StringComparison.Ordinal));
        }
    }

    [Fact]
    public async Task ADaemonOfALaterProtocolIsAMismatchBeforeTheKeyIsRead()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.ProtocolVersion(99));
        // No key file at all: the version is compared before it is read.
        File.Delete(fake.KeyPath);
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        Assert.Equal(HandshakeError.ProtocolMismatch(99), await RefusalAsync(client));
        await fake.WaitForEndedAsync(1);
        Assert.Equal([API.SystemHello.Name], fake.Received);
    }

    [Fact]
    public async Task AProcessWithoutTheKeyIsNeverAuthenticatedTo()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.WrongProof());
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        Assert.Equal(HandshakeError.DaemonUnproven, await RefusalAsync(client));
        await fake.WaitForEndedAsync(1);
        Assert.Equal([API.SystemHello.Name], fake.Handshakes); // system.authenticate was never sent
        Assert.Equal([API.SystemHello.Name], fake.Received);
    }

    [Fact]
    public async Task ARefusedProofIsRejected()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.RejectClient());
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        Assert.Equal(HandshakeError.Rejected(ErrorCode.Unauthenticated), await RefusalAsync(client));
        Assert.Equal([API.SystemHello.Name, API.SystemAuthenticate.Name], fake.Handshakes);
        Assert.Empty(fake.Calls);
    }

    [Fact]
    public async Task AMissingKeyFileIsKeyUnavailable()
    {
        await using var fake = await StartFakeAsync();
        File.Delete(fake.KeyPath);
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        var refused = await RefusalAsync(client);
        Assert.Equal(HandshakeError.KeyUnavailable($"{fake.KeyPath} does not exist"), refused);
        await fake.WaitForEndedAsync(1);
        Assert.Equal([API.SystemHello.Name], fake.Received); // nothing was sent after system.hello
    }

    [Fact]
    public async Task AMalformedProofIsMalformed()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.MalformedProof());
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        Assert.Equal(HandshakeError.Malformed("daemonProof is not 64 lowercase hex digits"), await RefusalAsync(client));
        await fake.WaitForEndedAsync(1);
        Assert.Equal([API.SystemHello.Name], fake.Received);
    }

    [Fact]
    public async Task ASilentDaemonTimesOut()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.Silent());
        var time = new FakeTimeProvider();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, handshakeTimeout: TimeSpan.FromMilliseconds(200), timeProvider: time);
        var dial = RefusalAsync(client);
        await Eventually.Holds(() => fake.Handshakes.Count == 1);
        time.Advance(TimeSpan.FromMilliseconds(200));
        Assert.Equal(HandshakeError.TimedOut, await dial);
        Assert.Equal(new RpcClientState.Disconnected(HandshakeError.TimedOut.ToString()), client.State);
    }

    /// <summary>
    /// A restarted daemon has a new key: the client reads the key file afresh
    /// for every connection and never keeps one.
    /// </summary>
    [Fact]
    public async Task ARestartedDaemonIsAuthenticatedWithItsNewKey()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var before = await File.ReadAllBytesAsync(fake.KeyPath, Ct);
        fake.Restart();
        await Eventually.Holds(() => client.State is RpcClientState.Disconnected);
        Assert.NotEqual(before, await File.ReadAllBytesAsync(fake.KeyPath, Ct)); // a new key
        await client.ConnectAsync(Ct);
        var info = await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal(42, info.Pid);
        Assert.Equal(
            [API.SystemHello.Name, API.SystemAuthenticate.Name, API.SystemHello.Name, API.SystemAuthenticate.Name],
            fake.Handshakes);
    }

    // The C# client's own.

    [Fact]
    public async Task ALineOverTheCapTearsTheConnectionDown()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var pending = client.CallAsync(TestMethods.Never, new EmptyParams(), TimeSpan.FromSeconds(30), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        // Well over the cap: the framer bounds what waits for its newline, so
        // a line that ends in the chunk that crosses the cap still passes, as
        // in Swift (FramingTests); this one does not end before it.
        var huge = new string('a', LineFramer.DefaultMaxLine + (1 << 20));
        _ = fake.PushNotificationAsync("notify.huge", $$"""{"p":"{{huge}}"}""");
        var e = await Assert.ThrowsAsync<RpcClientException>(() => pending);
        Assert.Equal(ClientError.Disconnected, e.Error);
        Assert.Equal(new RpcClientState.Disconnected($"a line from malachid exceeds {LineFramer.DefaultMaxLine} bytes"), client.State);
        Assert.False(client.Notifications.TryRead(out _));
    }

    [Fact]
    public async Task CallsGoOutInOrderAndConcurrently()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var calls = Enumerable.Range(0, 50).Select(_ => client.CallAsync(API.SystemInfo, new EmptyParams(), Ct)).ToArray();
        foreach (var info in await Task.WhenAll(calls))
        {
            Assert.Equal(FakeInfo, info);
        }
        Assert.Equal(50, fake.Calls.Count);
    }

    [Fact]
    public async Task CloseFailsPendingCallsAndCallsAfterIt()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        var pending = client.CallAsync(TestMethods.Never, new EmptyParams(), TimeSpan.FromSeconds(30), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        client.Close();
        Assert.Equal(ClientError.Disconnected, (await Assert.ThrowsAsync<RpcClientException>(() => pending)).Error);
        var after = await Assert.ThrowsAsync<RpcClientException>(() => client.CallAsync(API.SystemInfo, new EmptyParams(), Ct));
        Assert.Equal(ClientError.NotConnected, after.Error);
        Assert.Equal("not connected to malachid", after.Message);
    }

    [Fact]
    public async Task DisposeCompletesTheStreams()
    {
        await using var fake = await StartFakeAsync();
        var client = await ConnectedAsync(fake);
        client.Dispose();
        var states = new List<RpcClientState>();
        await foreach (var s in client.States.ReadAllAsync(Ct))
        {
            states.Add(s);
        }
        Assert.Equal([new RpcClientState.Connecting(), new RpcClientState.Connected(), new RpcClientState.Disconnected(null)], states);
        Assert.False(await client.Notifications.WaitToReadAsync(Ct));
        await Assert.ThrowsAsync<ObjectDisposedException>(() => client.ConnectAsync(Ct));
    }

    [Fact]
    public async Task ACancelledDialLeavesTheClientDisconnected()
    {
        await using var fake = await StartFakeAsync();
        fake.SetHandshake(new HandshakeMode.Silent());
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        using var cancel = new CancellationTokenSource();
        var dial = client.ConnectAsync(cancel.Token);
        await Eventually.Holds(() => fake.Handshakes.Count == 1);
        cancel.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => dial);
        Assert.Equal(new RpcClientState.Disconnected(null), client.State);
        await fake.WaitForEndedAsync(1);
    }

    [Fact]
    public async Task AThrowingHandlerDoesNotBreakTheTransport()
    {
        await using var fake = await StartFakeAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        client.StateChanged += (_, _) => throw new InvalidOperationException("a broken handler");
        client.NotificationReceived += (_, _) => throw new InvalidOperationException("a broken handler");
        await client.ConnectAsync(Ct);
        await fake.PushNotificationAsync("notify.accountsChanged", "{}");
        Assert.Equal("notify.accountsChanged", (await client.Notifications.ReadAsync(Ct)).Method);
        Assert.Equal(42, (await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct)).Pid);
    }

    [Fact]
    public async Task ALateAnswerAfterATimeoutIsDropped()
    {
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await using var fake = await StartFakeAsync(async (method, line) =>
        {
            if (method == "test.slow")
            {
                await release.Task;
                return FakeAnswer.Result("""{"which":"slow"}""");
            }
            return await Standard(method, line);
        });
        var time = new FakeTimeProvider();
        using var client = await ConnectedAsync(fake, time: time);
        var slow = client.CallAsync(TestMethods.Slow, new EmptyParams(), TimeSpan.FromSeconds(1), Ct);
        await Eventually.Holds(() => fake.Calls.Count == 1);
        time.Advance(TimeSpan.FromSeconds(1));
        await Assert.ThrowsAsync<RpcClientException>(() => slow);
        release.SetResult();
        await fake.IdleAsync();
        // The late answer found nobody; the next call gets its own.
        Assert.Equal(FakeInfo, await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct));
    }

    /// <summary>
    /// A notification whose method is not UTF-8 is ignored as any line that
    /// does not decode: the connection lives on and the next one arrives.
    /// </summary>
    [Fact]
    public async Task ALineThatIsNotUtf8IsIgnored()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        byte[] broken = [.. "{\"jsonrpc\":\"2.0\",\"method\":\"notify.x"u8, 0xFF, .. "\",\"params\":{}}\n"u8];
        await fake.PushRawAsync(broken);
        await fake.PushNotificationAsync(API.Notify.AccountsChanged, "{}");
        Assert.Equal(API.Notify.AccountsChanged, (await client.Notifications.ReadAsync(Ct)).Method);
        Assert.Equal(new RpcClientState.Connected(), client.State);
        Assert.Equal(FakeInfo, await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct));
    }

    /// <summary>A timeout no timer takes is refused before the call has an id: nothing is sent.</summary>
    [Fact]
    public async Task AnImpossibleTimeoutIsRefusedBeforeTheCall()
    {
        await using var fake = await StartFakeAsync();
        using var client = await ConnectedAsync(fake);
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => client.CallAsync(API.SystemInfo, new EmptyParams(), TimeSpan.FromSeconds(-2), Ct));
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => client.CallAsync(API.SystemInfo, new EmptyParams(), TimeSpan.FromDays(100), Ct));
        Assert.Equal(FakeInfo, await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct));
        Assert.Equal([API.SystemInfo.Name], fake.Calls);
    }

    internal static async Task<FakeAnswer> Standard(string method, byte[] line) => method switch
    {
        API.SystemInfoName => FakeAnswer.Result(SystemInfoJson),
        "test.fast" => FakeAnswer.Result("""{"which":"fast"}"""),
        "test.never" => await Never(),
        _ => await FakeDaemon.MethodNotFound(method, line),
    };

    private static async Task<FakeAnswer> Never()
    {
        await Task.Delay(Timeout.Infinite, TestContext.Current.CancellationToken);
        return FakeAnswer.Result("{}");
    }

    private static async Task<FakeDaemon> StartFakeAsync(FakeDaemon.Handler? handler = null)
    {
        var fake = new FakeDaemon(handler ?? Standard);
        await fake.StartAsync();
        return fake;
    }

    private static async Task<RpcClient> ConnectedAsync(FakeDaemon fake, TimeProvider? time = null)
    {
        var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance, timeProvider: time);
        await client.ConnectAsync(Ct);
        return client;
    }

    /// <summary>What ConnectAsync threw as a handshake refusal; null (and a failed assertion) when it connected or threw anything else.</summary>
    internal static async Task<HandshakeError?> RefusalAsync(RpcClient client)
    {
        try
        {
            await client.ConnectAsync(Ct);
            Assert.Fail("ConnectAsync succeeded");
        }
        catch (HandshakeException e)
        {
            return e.Error;
        }
        return null;
    }

    /// <summary>What a client reported through its handlers, in the order it happened.</summary>
    private sealed class Events
    {
        private readonly List<string> list = [];

        public Events(RpcClient client)
        {
            client.StateChanged += (_, s) => Add($"state {s.GetType().Name}");
            client.NotificationReceived += (_, n) => Add($"notification {n.Method}");
        }

        public IReadOnlyList<string> All
        {
            get
            {
                lock (list)
                {
                    return [.. list];
                }
            }
        }

        private void Add(string e)
        {
            lock (list)
            {
                list.Add(e);
            }
        }
    }
}

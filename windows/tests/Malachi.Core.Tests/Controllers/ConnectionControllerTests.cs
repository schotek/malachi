// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConnectionControllerTests.swift;
// GTK: ui/internal/window/connection_test.go (TestNextConnView,
// TestBackendProtocol, TestLogConnection), which test the pure functions
// the controller folds in: StatesFollowHowEachAttemptEnds replays
// TestNextConnView's attempts against a fake daemon, and
// RefusalsAreLoggedOnceUntilTheNextConnection TestLogConnection's levels;
// TestBackendProtocol's system.info cases are ProtocolMismatchIsReported
// (a mismatch without a connection is the handshake's, above).
//
// Swift waits with waitUntil and a 100 ms reconnect interval; here the
// controller runs on a fake clock and the tests wait for quiescence, then
// advance the clock by the default 5 s to make the loop try again. The
// client keeps the real clock for its dial and handshake timeouts. Added:
// AFastFailingTransportIsRetried, the trap of docs/windows-port.md §7.2
// with the real transport (a dial with nothing on the socket fails before
// its first await); SystemInfoOfADroppedConnectionIsDropped, the
// generation check that Swift leaves untested (window.go fetchSystemInfo's
// state check); and the handlers that throw, which a Swift callback cannot
// (AThrowingStateHandlerStopsNeitherTheStatesNorTheRetries,
// AThrowingNotificationHandlerStillHearsTheNext,
// AThrowingBindingDoesNotStopTheConnection).

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ConnectionControllerTests
{
    private static readonly SystemInfoResult FakeInfo = new() { Version = "fake", ProtocolVersion = API.ProtocolVersion, Pid = 7, StorePath = "/tmp/s.db" };

    private static readonly ConnectionState Connecting = new ConnectionState.Connecting();

    private static readonly ConnectionState Connected = new ConnectionState.Connected(FakeInfo);

    // What an attempt that fails reports.
    private static readonly string[] FailedAttempt = ["connecting", "unavailable"];

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task ConnectsAndReportsSystemInfo()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        Assert.Equal(Connecting, await h.StateAsync());

        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal([Connecting, Connected], h.Log.States);
        Assert.Equal([API.SystemInfo.Name], fake.Calls);

        await h.StopAsync();
        Assert.Equal(new ConnectionState.Stopping(), await h.StateAsync());
        Assert.Equal(new ConnectionState.Stopping(), h.Log.States[^1]);
        Assert.Equal(new RpcClientState.Disconnected(null), h.Client.State);
        // Nothing after stopping: the close must not surface as unavailable,
        // and the loop does not dial again.
        await h.IdleAsync(fake);
        await h.TickAsync(fake);
        Assert.Equal(new ConnectionState.Stopping(), h.Log.States[^1]);
        Assert.Equal(1, Dials(fake));
        // What XAML binds to changes with the state, not with every
        // announcement: the attempt's "Connecting…" was the state already.
        Assert.Equal([nameof(ConnectionController.State), nameof(ConnectionController.State)], h.Log.Properties);
    }

    [Fact]
    public async Task AdoptsTheDaemonThroughTheSupervisor()
    {
        await using var fake = await MakeFakeAsync();
        using var sup = new DaemonSupervisor(null, fake.Path);
        await using var h = await Harness.CreateAsync(fake.Path, sup);
        await h.StartAsync(fake);
        Assert.IsType<ConnectionState.Connected>(await h.StateAsync());
        Assert.Equal(0, sup.Spawns);
        await h.StopAsync();
        // Somebody else's daemon is left alone.
        Assert.True(UnixSocketProbe.Answers(fake.Path));
    }

    /// <summary>The handshake agreed, system.info does not: still a mismatch, as a defence.</summary>
    [Fact]
    public async Task ProtocolMismatchIsReported()
    {
        await using var fake = await MakeFakeAsync(protocolVersion: 99);
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(new ConnectionState.ProtocolMismatch(99), await h.StateAsync());
    }

    /// <summary>
    /// A daemon of another protocol version fails the handshake's check: the
    /// mismatch is reported and nothing is called, and the retry loop keeps
    /// it up instead of saying "Connecting…" on every attempt, until an
    /// attempt ends otherwise.
    /// </summary>
    [Fact]
    public async Task AnotherProtocolIsAStickyMismatch()
    {
        (HandshakeMode Mode, int Daemon)[] cases = [(new HandshakeMode.ProtocolVersion(99), 99), (new HandshakeMode.OldDaemon(), 1)];
        foreach (var (mode, daemon) in cases)
        {
            await using var fake = await MakeFakeAsync();
            fake.SetHandshake(mode);
            await using var h = await Harness.CreateAsync(fake.Path);
            var mismatch = new ConnectionState.ProtocolMismatch(daemon);
            await h.StartAsync(fake);
            Assert.Equal(mismatch, await h.StateAsync());
            await h.TickAsync(fake);
            await h.TickAsync(fake);
            Assert.True(fake.Handshakes.Count >= 3, $"{fake.Handshakes.Count} handshakes");
            Assert.True(h.Log.States.SequenceEqual([Connecting, mismatch]), "no Connecting… between the attempts");
            Assert.True(fake.Calls.Count == 0, "nothing is called on a daemon of another protocol");
            Assert.True(
                (await h.LoggedRefusalsAsync()).SequenceEqual([HandshakeError.ProtocolMismatch(daemon).ToString()]),
                "logged once");

            // A daemon of this protocol takes over: the connection ends the
            // mismatch, "Connecting…" until system.info answers.
            fake.SetHandshake(new HandshakeMode.Normal());
            await h.TickAsync(fake);
            Assert.Equal(Connected, await h.StateAsync());
            Assert.Equal([Connecting, mismatch, Connecting, Connected], h.Log.States);
            Assert.True((await h.LoggedRefusalsAsync()).Count == 0, "a connection starts the log afresh");
        }
    }

    /// <summary>
    /// GTK drops the mismatch at Connected: here too, as soon as the client
    /// is connected, while system.info is still on its way.
    /// </summary>
    [Fact]
    public async Task AConnectionEndsTheMismatchBeforeSystemInfoAnswers()
    {
        // system.info answers only once the gate is opened.
        var gate = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var asked = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await using var fake = new FakeDaemon();
        fake.On(API.SystemInfo.Name, async _ =>
        {
            asked.TrySetResult();
            await gate.Task;
            return InfoJson(API.ProtocolVersion);
        });
        fake.SetHandshake(new HandshakeMode.OldDaemon());
        await fake.StartAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(new ConnectionState.ProtocolMismatch(1), await h.StateAsync());

        fake.SetHandshake(new HandshakeMode.Normal());
        h.Time.Advance(h.Cc.ReconnectInterval);
        // The state is reported before system.info is asked.
        await asked.Task.WaitAsync(TimeSpan.FromSeconds(10), Ct);
        Assert.Equal(Connecting, await h.StateAsync());
        Assert.Equal(new RpcClientState.Connected(), h.Client.State);
        Assert.True(!h.Log.States.Any(s => s is ConnectionState.Connected), "system.info has not answered yet");
        gate.SetResult();
        await h.IdleAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal([Connecting, new ConnectionState.ProtocolMismatch(1), Connecting, Connected], h.Log.States);
    }

    /// <summary>
    /// The mismatch stays up only while the attempts end in one: an attempt
    /// that ends otherwise replaces it, and the next says "Connecting…".
    /// </summary>
    [Fact]
    public async Task AMismatchGivesWayWhenAnAttemptEndsOtherwise()
    {
        await using var fake = await MakeFakeAsync();
        fake.SetHandshake(new HandshakeMode.OldDaemon());
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(new ConnectionState.ProtocolMismatch(1), await h.StateAsync());
        await fake.StopAsync(); // nothing answers on the socket any more
        await h.TickAsync(fake);
        await h.TickAsync(fake);
        Assert.Equal(["connecting", "protocolMismatch", "unavailable", "connecting"], h.Log.States.Take(4).Select(Kind));
    }

    /// <summary>
    /// A connection that breaks during the handshake is routine, as when the
    /// daemon went away: unavailable, logged at debug level, not a refusal.
    /// </summary>
    [Fact]
    public async Task ABrokenHandshakeIsUnavailableNotARefusal()
    {
        await using var fake = await MakeFakeAsync();
        fake.SetHandshake(new HandshakeMode.Raw(new HandshakeScript { Hello = _ => null, CloseAfterHello = true }));
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        await h.TickAsync(fake);
        // The retry loop keeps moving: what was reported, not the state now.
        Assert.Contains(h.Log.States, s => s is ConnectionState.Unavailable);
        Assert.True(fake.Handshakes.Count >= 2, $"{fake.Handshakes.Count} handshakes");
        Assert.True((await h.LoggedRefusalsAsync()).Count == 0, "a broken connection is no refusal");
        Assert.DoesNotContain(h.Logger.Entries, e => e.Level >= LogLevel.Warning);
        Assert.Empty(fake.Calls);
    }

    /// <summary>
    /// The peer on the socket chooses the error codes of its refusals, and so
    /// their texts: what the log remembers starts afresh after 16.
    /// </summary>
    [Fact]
    public async Task LoggedRefusalsAreCapped()
    {
        var attempts = 0;
        await using var fake = await MakeFakeAsync();
        fake.SetHandshake(new HandshakeMode.Raw(new HandshakeScript
        {
            Hello = _ =>
            {
                var refusal = new RpcError { Code = 2000 + Interlocked.Increment(ref attempts), Message = "no" };
                return Encoding.UTF8.GetString(FakeDaemon.Response(1, refusal));
            },
        }));
        // The attempts are driven by hand, one at a time: the retry loop
        // never moves in between (the clock stands still).
        await using var h = await Harness.CreateAsync(fake.Path);
        static string Refused(int n) => HandshakeError.Rejected(2000 + n).ToString();
        await h.StartAsync(fake);
        for (var n = 1; n <= 17; n++)
        {
            if (n > 1)
            {
                await h.Ui.RunAsync(h.Cc.ReconnectNow);
                await h.IdleAsync(fake);
            }
            Assert.Equal(new ConnectionState.Unavailable(Refused(n)), await h.StateAsync());
            if (n == 16)
            {
                Assert.True(
                    (await h.LoggedRefusalsAsync()).SequenceEqual(Enumerable.Range(1, 16).Select(Refused)),
                    "16 distinct refusals are remembered");
            }
        }
        Assert.True((await h.LoggedRefusalsAsync()).SequenceEqual([Refused(17)]), "the 17th starts afresh");
    }

    /// <summary>
    /// Something on the socket that does not hold the key is no daemon to
    /// use: unavailable, retried, and logged at error level only once.
    /// </summary>
    [Fact]
    public async Task AnUnprovenDaemonIsUnavailableAndLoggedOnce()
    {
        await using var fake = await MakeFakeAsync();
        fake.SetHandshake(new HandshakeMode.WrongProof());
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        var refused = HandshakeError.DaemonUnproven.ToString();
        await h.TickAsync(fake);
        await h.TickAsync(fake);
        // The retry loop keeps moving: what was reported, not the state now.
        Assert.Contains(new ConnectionState.Unavailable(refused), h.Log.States);
        Assert.True(fake.Handshakes.Count >= 3, $"{fake.Handshakes.Count} handshakes");
        Assert.True((await h.LoggedRefusalsAsync()).SequenceEqual([refused]), "logged once, the repeats at debug level");
        Assert.Equal([LogLevel.Error, LogLevel.Debug, LogLevel.Debug], h.Logger.Levels("backend refused: " + refused));
        Assert.DoesNotContain(h.Log.States, s => s is ConnectionState.Connected);
        Assert.True(fake.Handshakes.All(m => m == API.SystemHello.Name), "system.authenticate was never sent");
        Assert.Empty(fake.Calls);
    }

    /// <summary>
    /// A restarted daemon has a new key: the connection drops, and the next
    /// attempt reads the new key and connects.
    /// </summary>
    [Fact]
    public async Task ARestartedDaemonIsReconnectedWithItsNewKey()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        fake.Restart();
        await h.IdleAsync(fake, () => h.Log.States.Count >= 3);
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal(["connecting", "connected", "unavailable", "connecting", "connected"], h.Log.States.Select(Kind));
        Assert.Equal(
            [API.SystemHello.Name, API.SystemAuthenticate.Name, API.SystemHello.Name, API.SystemAuthenticate.Name],
            fake.Handshakes);
        Assert.Empty(await h.LoggedRefusalsAsync());
    }

    [Fact]
    public async Task InfoFailureIsReported()
    {
        await using var fake = new FakeDaemon();
        fake.On(
            API.SystemInfo.Name,
            new FakeDaemon.MethodHandler(_ => Task.FromException<string>(new RpcException(new RpcError { Code = 1000, Message = "broken" }))));
        await fake.StartAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(new ConnectionState.InfoFailed("broken (1000)"), await h.StateAsync());
        Assert.Equal([LogLevel.Error], h.Logger.Levels("system.info: broken (1000)"));
    }

    [Fact]
    public async Task ServerCloseIsUnavailableThenReconnects()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());

        fake.CloseAll();
        await h.IdleAsync(fake, () => h.Log.States.Count >= 3);
        var unavailable = Assert.IsType<ConnectionState.Unavailable>(await h.StateAsync());
        Assert.NotEmpty(unavailable.Reason);

        // The retry loop dials again after the interval.
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal(2, Dials(fake));
        Assert.Equal(["connecting", "connected", "unavailable", "connecting", "connected"], h.Log.States.Select(Kind));
    }

    [Fact]
    public async Task RetriesUntilTheDaemonAppears()
    {
        await using var fake = await MakeFakeAsync(start: false);
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.IsType<ConnectionState.Unavailable>(await h.StateAsync());
        await h.TickAsync(fake);
        await h.TickAsync(fake);
        Assert.Contains(h.Log.States, s => s is ConnectionState.Unavailable);

        await fake.StartAsync();
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal(Connecting, h.Log.States[0]);
        Assert.Contains(h.Log.States, s => s is ConnectionState.Unavailable);
    }

    /// <summary>
    /// docs/windows-port.md §7.2: with nothing on the socket the client's
    /// dial fails before its first await, so an attempt ported literally
    /// would end before its handle is set and block every later retry. The
    /// attempt starts after ReconnectNow returned: the loop keeps retrying,
    /// by the clock and by hand, until the daemon is there.
    /// </summary>
    [Fact]
    public async Task AFastFailingTransportIsRetried()
    {
        var path = FakeDaemon.NewSocketPath();
        using (var probe = new RpcClient(path, PortableKeyFilePolicy.Instance))
        {
            var dial = probe.ConnectAsync(Ct);
            Assert.True(dial.IsFaulted, "the dial fails at once");
            await Assert.ThrowsAsync<RpcClientException>(() => dial);
        }
        await using var h = await Harness.CreateAsync(path);
        await h.StartAsync();
        Assert.Equal(["connecting", "unavailable"], h.Log.States.Select(Kind));
        for (var i = 0; i < 3; i++)
        {
            await h.TickAsync();
        }
        Assert.Equal(8, h.Log.States.Count);
        // A retry by hand as well: the attempt cleared its handle.
        await h.Ui.RunAsync(h.Cc.ReconnectNow);
        await h.IdleAsync();
        Assert.Equal(10, h.Log.States.Count);
        Assert.Equal(Enumerable.Repeat(FailedAttempt, 5).SelectMany(p => p), h.Log.States.Select(Kind));
        Assert.All(h.Logger.Entries, e => Assert.Equal(LogLevel.Debug, e.Level));

        await using var fake = new FakeDaemon(path: path);
        fake.On(API.SystemInfo.Name, _ => InfoJson(API.ProtocolVersion));
        await fake.StartAsync();
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
    }

    [Fact]
    public async Task SupervisorFailureIsUnavailable()
    {
        var dead = FakeDaemon.NewSocketPath();
        using var sup = new DaemonSupervisor(null, dead);
        await using var h = await Harness.CreateAsync(dead, sup);
        await h.StartAsync();
        Assert.Equal(new ConnectionState.Unavailable(DaemonSupervisorException.NoDaemon().Message), await h.StateAsync());
    }

    [Fact]
    public async Task NotificationsAreForwardedInOrder()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());

        for (var i = 1; i <= 5; i++)
        {
            await fake.PushNotificationAsync($"notify.test{i}", $$"""{"i":{{i}}}""");
        }
        await h.IdleAsync(fake, () => h.Log.Notifications.Count == 5);
        Assert.Equal(Enumerable.Range(1, 5).Select(i => $"notify.test{i}"), h.Log.Notifications);
    }

    [Fact]
    public async Task ReconnectNowIsANoOpWhileConnected()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        await h.Ui.RunAsync(() =>
        {
            h.Cc.ReconnectNow();
            h.Cc.Start();
        });
        await h.IdleAsync(fake);
        Assert.Equal([Connecting, Connected], h.Log.States);
        Assert.Equal(1, Dials(fake));
        // Nor does the retry loop dial while connected.
        await h.TickAsync(fake);
        await h.TickAsync(fake);
        Assert.Equal([Connecting, Connected], h.Log.States);
        Assert.Equal(1, Dials(fake));
    }

    /// <summary>
    /// The generation (window.go <c>fetchSystemInfo</c> drops an answer once
    /// the state is no longer connected): the UI thread is held while the
    /// client connects and the daemon hangs up, so it sees the connection
    /// and its end in one turn, and the system.info asked at the connection
    /// fails (nothing to send it on) after the drop was reported. That
    /// failure belongs to a connection that is gone: the line stays
    /// unavailable, not "system.info failed".
    /// </summary>
    [Fact]
    public async Task SystemInfoOfADroppedConnectionIsDropped()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        var connected = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var dropped = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        h.Client.StateChanged += (_, s) =>
        {
            if (s is RpcClientState.Connected)
            {
                connected.TrySetResult();
            }
            else if (s is RpcClientState.Disconnected && connected.Task.IsCompleted)
            {
                dropped.TrySetResult();
            }
        };
        using var hold = new ManualResetEventSlim();
        // Start posts the readers and the attempt; the hold comes after
        // them, once the attempt is dialling.
        await h.Ui.RunAsync(h.Cc.Start);
        h.Ui.Post(_ => hold.Wait(TimeSpan.FromSeconds(10)), null);
        await connected.Task.WaitAsync(TimeSpan.FromSeconds(10), Ct);
        fake.CloseAll();
        await dropped.Task.WaitAsync(TimeSpan.FromSeconds(10), Ct);
        hold.Set();
        await h.IdleAsync(fake);

        Assert.Equal(["connecting", "unavailable"], h.Log.States.Select(Kind));
        Assert.IsType<ConnectionState.Unavailable>(await h.StateAsync());
        Assert.Empty(fake.Calls);
        Assert.DoesNotContain(h.Logger.Entries, e => e.Message.StartsWith("system.info", StringComparison.Ordinal));
        // The retry loop takes it from there.
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
    }

    /// <summary>
    /// A handler of StateChanged that throws (a view's bug) is reported and
    /// stops nothing: a throw on the drop, handed over by the reader of the
    /// client's states, leaves that reader running, and a throw on the
    /// retry loop's "Connecting…" leaves the attempt and the loop running
    /// (RpcClient.Raise isolates its handlers for the same reason).
    /// </summary>
    [Fact]
    public async Task AThrowingStateHandlerStopsNeitherTheStatesNorTheRetries()
    {
        const int OnUnavailable = 1;
        const int OnConnecting = 2;
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        var boom = new InvalidOperationException("a broken view");
        var trip = 0;
        h.Cc.StateChanged += (_, s) =>
        {
            var on = s switch
            {
                ConnectionState.Unavailable => OnUnavailable,
                ConnectionState.Connecting => OnConnecting,
                _ => 0,
            };
            if (on != 0 && Interlocked.CompareExchange(ref trip, 0, on) == on)
            {
                throw boom;
            }
        };
        var after = new ConcurrentQueue<ConnectionState>();
        h.Cc.StateChanged += (_, s) => after.Enqueue(s);
        await h.StartAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());

        Interlocked.Exchange(ref trip, OnUnavailable);
        fake.CloseAll();
        var failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync(fake, () => h.Log.States[^1] is ConnectionState.Unavailable));
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));

        Interlocked.Exchange(ref trip, OnConnecting);
        h.Time.Advance(h.Cc.ReconnectInterval);
        failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync(fake));
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));
        Assert.Equal(Connected, await h.StateAsync());

        // Both are still there: the states reader sees the next drop, the
        // loop dials again.
        fake.CloseAll();
        await h.IdleAsync(fake, () => h.Log.States[^1] is ConnectionState.Unavailable);
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        string[] want = ["connecting", "connected", "unavailable", "connecting", "connected", "unavailable", "connecting", "connected"];
        Assert.Equal(want, h.Log.States.Select(Kind));
        // The handler after the broken one heard every state.
        Assert.Equal(want, after.Select(Kind));
    }

    /// <summary>
    /// A handler of NotificationReceived that throws (one that cannot decode
    /// a notification, say) is reported; the handlers after it hear that
    /// notification, and every handler hears the next.
    /// </summary>
    [Fact]
    public async Task AThrowingNotificationHandlerStillHearsTheNext()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        var boom = new InvalidOperationException("an undecodable notification");
        var heard = new ConcurrentQueue<string>();
        h.Cc.NotificationReceived += (_, n) =>
        {
            heard.Enqueue(n.Method);
            if (heard.Count == 1)
            {
                throw boom;
            }
        };
        var after = new ConcurrentQueue<string>();
        h.Cc.NotificationReceived += (_, n) => after.Enqueue(n.Method);
        await h.StartAsync(fake);

        await fake.PushNotificationAsync("notify.test1", """{"i":1}""");
        var failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync(fake, () => h.Log.Notifications.Count == 1));
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));
        await fake.PushNotificationAsync("notify.test2", """{"i":2}""");
        await h.IdleAsync(fake, () => h.Log.Notifications.Count == 2);
        Assert.Equal(["notify.test1", "notify.test2"], heard);
        Assert.Equal(["notify.test1", "notify.test2"], after);
    }

    /// <summary>
    /// A PropertyChanged handler that throws (a binding) is reported and
    /// does not stop the change: the state is set and StateChanged follows.
    /// </summary>
    [Fact]
    public async Task AThrowingBindingDoesNotStopTheConnection()
    {
        await using var fake = await MakeFakeAsync();
        await using var h = await Harness.CreateAsync(fake.Path);
        var boom = new InvalidOperationException("a broken binding");
        h.Cc.PropertyChanged += (_, _) => throw boom;
        await h.Ui.RunAsync(h.Cc.Start);
        var failed = await Assert.ThrowsAsync<AggregateException>(() => h.IdleAsync(fake));
        Assert.Same(boom, Assert.Single(failed.InnerExceptions));
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Equal([Connecting, Connected], h.Log.States);
    }

    /// <summary>
    /// connection_test.go TestNextConnView: a mismatch is shown until an
    /// attempt ends otherwise, the retries every interval must not make the
    /// line flicker, and an attempt that ends otherwise (nothing answering, a
    /// handshake that times out or breaks) replaces it. The line is the
    /// status line over the controller's state, as the window shows it.
    /// </summary>
    [Fact]
    public async Task StatesFollowHowEachAttemptEnds()
    {
        var protocol1 = $"Protocol mismatch: UI {API.ProtocolVersion}, backend 1";
        var later = API.ProtocolVersion + 1;
        var protocolLater = $"Protocol mismatch: UI {API.ProtocolVersion}, backend {later}";
        await using var fake = await MakeFakeAsync(start: false);
        // The client's handshake timeout runs on a clock of its own, moved
        // only for the silent daemon once its hello arrived: a daemon that
        // answers is never timed out, however busy the machine. With a real
        // second, an old daemon's refusal under load sometimes came late and
        // the attempt ended as unavailable instead of the mismatch.
        var handshakeTimeout = TimeSpan.FromSeconds(1);
        var clientTime = new FakeTimeProvider();
        await using var h = await Harness.CreateAsync(fake.Path, handshakeTimeout: handshakeTimeout, clientTime: clientTime);
        async Task<string> Line() => SyncController.StatusLineFor(new ConnView(await h.StateAsync()), "Up to date", false).Text;

        await h.StartAsync(fake); // nothing listens
        Assert.Equal("Backend unavailable", await Line());
        fake.SetHandshake(new HandshakeMode.OldDaemon());
        await fake.StartAsync();
        await h.TickAsync(fake);
        Assert.Equal(protocol1, await Line());
        await h.TickAsync(fake);
        Assert.Equal(protocol1, await Line());
        // An attempt that ends otherwise replaces the mismatch.
        fake.SetHandshake(new HandshakeMode.Silent());
        var hellos = fake.Handshakes.Count;
        h.Time.Advance(h.Cc.ReconnectInterval);
        await Eventually.Holds(() => fake.Handshakes.Count > hellos, TimeSpan.FromSeconds(30), "the silent daemon got no hello");
        clientTime.Advance(handshakeTimeout);
        await h.IdleAsync(fake);
        Assert.Equal("Backend unavailable", await Line());
        fake.SetHandshake(new HandshakeMode.ProtocolVersion(later));
        await h.TickAsync(fake);
        Assert.Equal(protocolLater, await Line());
        fake.SetHandshake(new HandshakeMode.Hangup());
        await h.TickAsync(fake);
        Assert.Equal("Backend unavailable", await Line());
        fake.SetHandshake(new HandshakeMode.OldDaemon());
        await h.TickAsync(fake);
        Assert.Equal(protocol1, await Line());
        fake.SetHandshake(new HandshakeMode.Normal());
        await h.TickAsync(fake);
        Assert.Equal("Up to date", await Line());
        fake.CloseAll();
        await h.IdleAsync(fake, () => h.Log.States[^1] is ConnectionState.Unavailable);
        Assert.Equal("Backend unavailable", await Line());

        // "Connecting…" only where the state before was no mismatch.
        Assert.Equal(
            [
                "connecting", "unavailable", "connecting", "protocolMismatch", "unavailable", "connecting", "protocolMismatch",
                "unavailable", "connecting", "protocolMismatch", "connecting", "connected", "unavailable",
            ],
            h.Log.States.Select(Kind));
    }

    /// <summary>
    /// connection_test.go TestLogConnection: a refused handshake is worth an
    /// error once per distinct text until a connection succeeds, the retries
    /// repeat it at debug level; nothing answering on the socket is routine
    /// and stays at debug level.
    /// </summary>
    [Fact]
    public async Task RefusalsAreLoggedOnceUntilTheNextConnection()
    {
        await using var fake = await MakeFakeAsync(start: false);
        await using var h = await Harness.CreateAsync(fake.Path);
        var unproven = "backend refused: " + HandshakeError.DaemonUnproven;
        var protocol3 = "backend refused: " + HandshakeError.ProtocolMismatch(3);

        await h.StartAsync(fake); // no daemon: routine
        Assert.Equal([LogLevel.Debug], h.Logger.TakeLevels());
        fake.SetHandshake(new HandshakeMode.WrongProof());
        await fake.StartAsync();
        await h.TickAsync(fake);
        Assert.Equal([(LogLevel.Error, unproven)], h.Logger.Take());
        await h.TickAsync(fake); // the same text again
        Assert.Equal([(LogLevel.Debug, unproven)], h.Logger.Take());
        fake.SetHandshake(new HandshakeMode.ProtocolVersion(3)); // another text
        await h.TickAsync(fake);
        Assert.Equal([(LogLevel.Error, protocol3)], h.Logger.Take());
        fake.SetHandshake(new HandshakeMode.WrongProof());
        await h.TickAsync(fake);
        Assert.Equal([(LogLevel.Debug, unproven)], h.Logger.Take());

        // A connection forgets what was logged.
        fake.SetHandshake(new HandshakeMode.Normal());
        await h.TickAsync(fake);
        Assert.Equal(Connected, await h.StateAsync());
        Assert.Empty(h.Logger.Take());
        fake.SetHandshake(new HandshakeMode.WrongProof());
        fake.CloseAll(); // a connection that broke: routine, reported by the state alone
        await h.IdleAsync(fake, () => h.Log.States[^1] is ConnectionState.Unavailable);
        await h.TickAsync(fake);
        Assert.Equal([(LogLevel.Error, unproven)], h.Logger.Take());
    }

    private static string InfoJson(int protocolVersion) =>
        $$"""{"version":"fake","protocolVersion":{{protocolVersion}},"pid":7,"storePath":"/tmp/s.db"}""";

    /// <summary>A fake answering system.info; started unless <paramref name="start"/> is false.</summary>
    private static async Task<FakeDaemon> MakeFakeAsync(int protocolVersion = API.ProtocolVersion, bool start = true)
    {
        var fake = new FakeDaemon();
        fake.On(API.SystemInfo.Name, _ => InfoJson(protocolVersion));
        if (start)
        {
            await fake.StartAsync();
        }
        return fake;
    }

    /// <summary>
    /// The connections the client dialled: Swift counts the accepted ones,
    /// but here the socket probe before every dial is an accepted connection
    /// of its own, which says nothing; every dial says hello.
    /// </summary>
    private static int Dials(FakeDaemon fake) => fake.Handshakes.Count(m => m == API.SystemHello.Name);

    private static string Kind(ConnectionState s) => s switch
    {
        ConnectionState.Connecting => "connecting",
        ConnectionState.Connected => "connected",
        ConnectionState.ProtocolMismatch => "protocolMismatch",
        ConnectionState.InfoFailed => "infoFailed",
        ConnectionState.Unavailable => "unavailable",
        _ => "stopping",
    };

    /// <summary>
    /// The test's UI thread, a fake clock, the client, the controller made on
    /// that thread and what it reported.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private Harness(RpcClient client)
        {
            Client = client;
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 27, 12, 0, 0, TimeSpan.Zero));

        public RpcClient Client { get; }

        public ConnectionController Cc { get; private set; } = null!;

        public Log Log { get; } = new();

        public LevelLogger Logger { get; } = new();

        /// <summary>
        /// A harness over the daemon at <paramref name="path"/>; the client's
        /// timeouts run on <paramref name="clientTime"/> (the system's clock
        /// when null), the controller's retries on <see cref="Time"/>.
        /// </summary>
        public static async Task<Harness> CreateAsync(
            string path, DaemonSupervisor? supervisor = null, TimeSpan? handshakeTimeout = null, TimeProvider? clientTime = null)
        {
            var h = new Harness(new RpcClient(path, PortableKeyFilePolicy.Instance, handshakeTimeout, clientTime));
            h.Cc = await h.Ui.RunAsync(() => new ConnectionController(h.Client, supervisor, timeProvider: h.Time, logger: h.Logger, pending: h.Pending));
            h.Log.Attach(h.Cc);
            return h;
        }

        /// <summary>Starts the controller and waits until its first attempt is over.</summary>
        public async Task StartAsync(FakeDaemon? fake = null)
        {
            await Ui.RunAsync(Cc.Start);
            await IdleAsync(fake);
        }

        public Task IdleAsync(FakeDaemon? fake = null, Func<bool>? also = null) => Quiescence.IdleAsync(Ui, Pending, fake, also);

        /// <summary>One reconnect interval passes; waits until what it started is over.</summary>
        public async Task TickAsync(FakeDaemon? fake = null)
        {
            Time.Advance(Cc.ReconnectInterval);
            await IdleAsync(fake);
        }

        public Task<ConnectionState> StateAsync() => Ui.RunAsync(() => Cc.State);

        public Task<IReadOnlyList<string>> LoggedRefusalsAsync() => Ui.RunAsync(() => Cc.LoggedRefusals);

        public Task StopAsync() => Ui.InvokeAsync(Cc.StopAsync);

        public async ValueTask DisposeAsync()
        {
            await Ui.InvokeAsync(async () =>
            {
                await Cc.StopAsync();
                Cc.Dispose();
            });
            Client.Dispose();
            Ui.Dispose();
        }
    }

    /// <summary>Collects what the controller reports, readable from the test's thread.</summary>
    private sealed class Log
    {
        private readonly Lock gate = new();
        private readonly List<ConnectionState> states = [];
        private readonly List<string> notifications = [];
        private readonly List<string?> properties = [];

        public IReadOnlyList<ConnectionState> States
        {
            get
            {
                lock (gate)
                {
                    return [.. states];
                }
            }
        }

        public IReadOnlyList<string> Notifications
        {
            get
            {
                lock (gate)
                {
                    return [.. notifications];
                }
            }
        }

        /// <summary>The properties the controller said changed (INotifyPropertyChanged, what XAML binds to).</summary>
        public IReadOnlyList<string?> Properties
        {
            get
            {
                lock (gate)
                {
                    return [.. properties];
                }
            }
        }

        public void Attach(ConnectionController cc)
        {
            cc.StateChanged += (_, s) =>
            {
                lock (gate)
                {
                    states.Add(s);
                }
            };
            cc.NotificationReceived += (_, n) =>
            {
                lock (gate)
                {
                    notifications.Add(n.Method);
                }
            };
            cc.PropertyChanged += (_, e) =>
            {
                lock (gate)
                {
                    properties.Add(e.PropertyName);
                }
            };
        }
    }

    /// <summary>Keeps the level and text of every log line.</summary>
    private sealed class LevelLogger : ILogger<ConnectionController>
    {
        private readonly Lock gate = new();
        private readonly List<(LogLevel Level, string Message)> entries = [];

        public IReadOnlyList<(LogLevel Level, string Message)> Entries
        {
            get
            {
                lock (gate)
                {
                    return [.. entries];
                }
            }
        }

        /// <summary>The lines since the last call.</summary>
        public (LogLevel Level, string Message)[] Take()
        {
            lock (gate)
            {
                var taken = entries.ToArray();
                entries.Clear();
                return taken;
            }
        }

        /// <summary>The levels of the lines since the last call.</summary>
        public IReadOnlyList<LogLevel> TakeLevels() => [.. Take().Select(e => e.Level)];

        /// <summary>The levels of every line that says <paramref name="message"/>.</summary>
        public IReadOnlyList<LogLevel> Levels(string message) => [.. Entries.Where(e => e.Message == message).Select(e => e.Level)];

        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            lock (gate)
            {
                entries.Add((logLevel, formatter(state, exception)));
            }
        }
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Harness classes of
// macos/Tests/MalachiCoreTests/MailboxControllerFoldersTests.swift and
// MailboxControllerListTests.swift, shared by the C# suites: a MailFixture,
// a connected client, the mailbox (and its list half) over a throwaway
// settings store, created on the tests' UI thread. Swift polls with
// waitUntil and sleeps for the negative cases; here the tests wait for
// quiescence (IdleAsync) and move a FakeTimeProvider, which also runs the
// fixture's delays, the mark-as-read delay and the search pause.
//
// A clock that is moved must only be moved once the timer it is meant to
// fire exists, or the timer is armed after the move and never fires. The
// controllers arm theirs in work they post to the UI thread, which
// AdvanceAsync drains first; the fixture arms a delay on the daemon's
// thread, whose timers the fixture's clock counts (DelayedAnswers).

using System;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

/// <summary>A fixture, a client and the mailbox controllers on a test UI thread.</summary>
internal sealed class MailboxControllerHarness : IAsyncDisposable
{
    /// <summary>What the fixture's system.info says (Swift <c>Harness.info</c>).</summary>
    public static readonly SystemInfoResult Info = new() { Version = "fake", ProtocolVersion = API.ProtocolVersion, Pid = 7, StorePath = "/tmp/s.db" };

    private readonly CountingClock fixtureClock;
    private ListController? list;

    private MailboxControllerHarness()
    {
        fixtureClock = new CountingClock(Time);
    }

    /// <summary>The tests' UI thread; the controllers belong to it.</summary>
    public TestUIContext UI { get; } = new();

    /// <summary>The clock of the controllers and of the fixture's delays.</summary>
    public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 1, 12, 0, 0, TimeSpan.Zero));

    public MailFixture Fixture { get; private set; } = null!;

    public RpcClient Client { get; private set; } = null!;

    /// <summary>A throwaway settings store (Swift <c>ScratchSettings</c>).</summary>
    public SettingsStore Settings { get; } = CollapseStateTests.Scratch();

    /// <summary>The background work of the controllers.</summary>
    public PendingWork Pending { get; } = new();

    public MailboxControllerTestSync Sync { get; private set; } = null!;

    public MailboxController Mailbox { get; private set; } = null!;

    /// <summary>The list half, when asked for.</summary>
    public ListController List => list ?? throw new InvalidOperationException("the harness has no list half");

    /// <summary>Every toast, in order.</summary>
    public System.Collections.Generic.List<string> Toasts { get; } = [];

    /// <summary>How many answers the fixture has held back on the clock so far (<see cref="MailFixture.Delay"/>).</summary>
    public int DelayedAnswers => fixtureClock.TimersCreated;

    /// <summary>
    /// Scripts and starts a fixture, sets the settings up
    /// (<paramref name="prepare"/>), creates the controllers on the UI
    /// thread, lets <paramref name="wire"/> subscribe, then connects unless
    /// told not to.
    /// </summary>
    public static async Task<MailboxControllerHarness> StartAsync(
        Action<MailFixture> script,
        bool withList = false,
        Action<MailboxControllerHarness>? wire = null,
        bool connect = true,
        Action<SettingsStore>? prepare = null)
    {
        var h = new MailboxControllerHarness();
        h.Fixture = new MailFixture(h.fixtureClock);
        script(h.Fixture);
        prepare?.Invoke(h.Settings);
        await h.Fixture.StartAsync();
        h.Client = new RpcClient(h.Fixture.Path, PortableKeyFilePolicy.Instance);
        await h.UI.RunAsync(() =>
        {
            h.Sync = new MailboxControllerTestSync(h.Pending, h.Time);
            h.Mailbox = new MailboxController(h.Client, h.Settings, h.Sync, h.Toasts.Add, h.Pending);
            if (withList)
            {
                h.list = new ListController(h.Mailbox, h.Settings, h.Time);
            }
            wire?.Invoke(h);
        });
        if (connect)
        {
            await h.ConnectAsync();
        }
        return h;
    }

    /// <summary>Dials and tells the controllers, as the app does on the connection controller's state.</summary>
    public async Task ConnectAsync()
    {
        await Client.ConnectAsync(TestContext.Current.CancellationToken);
        await On(() => Mailbox.HandleConnection(new ConnectionState.Connected(Info)));
    }

    /// <summary>Runs <paramref name="action"/> on the UI thread.</summary>
    public Task On(Action action) => UI.RunAsync(action);

    /// <summary>Runs <paramref name="read"/> on the UI thread.</summary>
    public Task<T> On<T>(Func<T> read) => UI.RunAsync(read);

    /// <summary>Waits until the controllers, the daemon and the UI queue have nothing left to do.</summary>
    public Task IdleAsync(Func<bool>? also = null) => Quiescence.IdleAsync(UI, Pending, Fixture.Daemon, also);

    /// <summary>
    /// Waits until the fixture holds back its <paramref name="count"/>th
    /// answer on the clock (<see cref="DelayedAnswers"/>): the call arrived
    /// and its delay is armed, so a move of the clock releases it. A held
    /// answer keeps the daemon busy, which <see cref="IdleAsync"/> would wait
    /// for.
    /// </summary>
    public Task DelayedAsync(int count) =>
        Eventually.Holds(() => DelayedAnswers >= count, what: $"{count} answers held back");

    /// <summary>
    /// Moves the clock, once the timers the UI thread has been asked to arm
    /// are armed, and waits for what that set off.
    /// </summary>
    public async Task AdvanceAsync(TimeSpan by)
    {
        await UI.DrainAsync();
        Time.Advance(by);
        await IdleAsync();
    }

    public async ValueTask DisposeAsync()
    {
        await UI.RunAsync(() =>
        {
            list?.Close();
            Mailbox?.Close();
        });
        // Answers still held back on the clock go now, into closed controllers.
        Time.Advance(TimeSpan.FromHours(1));
        Client.Dispose();
        await Fixture.DisposeAsync();
        UI.Dispose();
        Settings.Dispose();
    }

    /// <summary>The fake clock, counting the timers armed on it (the fixture's delays).</summary>
    private sealed class CountingClock(TimeProvider inner) : TimeProvider
    {
        private int created;

        public int TimersCreated => Volatile.Read(ref created);

        public override TimeZoneInfo LocalTimeZone => inner.LocalTimeZone;

        public override long TimestampFrequency => inner.TimestampFrequency;

        public override DateTimeOffset GetUtcNow() => inner.GetUtcNow();

        public override long GetTimestamp() => inner.GetTimestamp();

        public override ITimer CreateTimer(TimerCallback callback, object? state, TimeSpan dueTime, TimeSpan period)
        {
            var timer = inner.CreateTimer(callback, state, dueTime, period);
            Interlocked.Increment(ref created);
            return timer;
        }
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/StorageUsageTests.swift: the Disk
// Space Used row of the preferences (ui/internal/window/preferences.go
// storageTexts, bindStorage; preferences_test.go TestStorageTexts), against
// a fake daemon. Swift runs the timer on the real clock with short periods
// and sleeps for the negative cases; here the period is on a fake clock the
// test advances, the test waits until nothing is left to happen
// (Quiescence.IdleAsync), and a slow answer is held until the test releases
// it (HeldAnswer).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class StorageUsageTests
{
    private static readonly TimeSpan Period = TimeSpan.FromSeconds(5);

    [Fact]
    public void StorageTextsTest()
    {
        Assert.Equal(("0 B", ""), StorageUsage.StorageTexts(new SystemStorageResult()));
        var plain = new SystemStorageResult { TotalBytes = 44_470_272 + 909_800_000, MessageBytes = 909_800_000, MessageUncompressedBytes = 909_800_000 };
        Assert.Equal(("910.1 MiB", ""), StorageUsage.StorageTexts(plain)); // no compression, nothing on the server
        var both = new SystemStorageResult { TotalBytes = 734_003_200, SavedBytes = 363_100_000, RemoteAttachmentBytes = 312_000_000 };
        Assert.Equal(
            ("700.0 MiB", "Compression saves 346.3 MiB\n297.5 MiB of attachments are on the server only"),
            StorageUsage.StorageTexts(both));
        var remoteOnly = new SystemStorageResult { TotalBytes = 5L << 30, RemoteAttachmentBytes = 2L << 30 };
        Assert.Equal(("5.0 GiB", "2.0 GiB of attachments are on the server only"), StorageUsage.StorageTexts(remoteOnly));
        var savedOnly = new SystemStorageResult { TotalBytes = 2048, SavedBytes = 1024 };
        Assert.Equal(("2 KiB", "Compression saves 1 KiB"), StorageUsage.StorageTexts(savedOnly));
        // A negative saving (a store converting back) says nothing.
        Assert.Equal("", StorageUsage.StorageTexts(new SystemStorageResult { TotalBytes = 10, SavedBytes = -5 }).Details);
        // The background conversion comes first while it runs or stopped.
        Assert.Equal(
            ("3.0 GiB", "Converting the stored mail in the background\nCompression saves 512.0 MiB"),
            StorageUsage.StorageTexts(new SystemStorageResult { TotalBytes = 3L << 30, SavedBytes = 512L << 20, Conversion = StorageConversion.Running }));
        Assert.Equal(
            ("3.0 GiB", "Converting stopped: the disk is full"),
            StorageUsage.StorageTexts(new SystemStorageResult { TotalBytes = 3L << 30, Conversion = StorageConversion.NoSpace }));
        Assert.Equal("", StorageUsage.StorageTexts(new SystemStorageResult { TotalBytes = 5, Conversion = StorageConversion.Idle }).Details);
        // A state a newer daemon adds says nothing either.
        Assert.Equal("", StorageUsage.StorageTexts(new SystemStorageResult { TotalBytes = 5, Conversion = new StorageConversion("paused") }).Details);
    }

    /// <summary>preferences_test.go TestStorageTexts, case by case.</summary>
    [Fact]
    public void GoStorageTexts()
    {
        (string Name, SystemStorageResult R, string Value, string Details)[] cases =
        [
            ("empty store", new SystemStorageResult(), "0 B", ""),
            (
                "uncompressed, everything local",
                new SystemStorageResult { TotalBytes = 700L << 20, MessageBytes = 650L << 20, MessageUncompressedBytes = 650L << 20 },
                "700.0 MiB", ""),
            ("compressed", new SystemStorageResult { TotalBytes = 3L << 30, SavedBytes = 512L << 20 }, "3.0 GiB", "Compression saves 512.0 MiB"),
            (
                "attachments on the server",
                new SystemStorageResult { TotalBytes = 3L << 30, RemoteAttachmentBytes = 2L << 30 },
                "3.0 GiB", "2.0 GiB of attachments are on the server only"),
            (
                "both",
                new SystemStorageResult { TotalBytes = (1L << 30) + (512L << 20), SavedBytes = 300L << 10, RemoteAttachmentBytes = 40L << 20 },
                "1.5 GiB", "Compression saves 300 KiB\n40.0 MiB of attachments are on the server only"),
            ("no saving to speak of", new SystemStorageResult { TotalBytes = 5, SavedBytes = -3 }, "5 B", ""),
            (
                "converting",
                new SystemStorageResult { TotalBytes = 3L << 30, SavedBytes = 512L << 20, Conversion = StorageConversion.Running },
                "3.0 GiB", "Converting the stored mail in the background\nCompression saves 512.0 MiB"),
            ("disk full", new SystemStorageResult { TotalBytes = 3L << 30, Conversion = StorageConversion.NoSpace }, "3.0 GiB", "Converting stopped: the disk is full"),
            ("idle says nothing", new SystemStorageResult { TotalBytes = 5, Conversion = StorageConversion.Idle }, "5 B", ""),
        ];
        foreach (var (name, r, value, details) in cases)
        {
            Assert.True((value, details) == StorageUsage.StorageTexts(r), $"{name}: {StorageUsage.StorageTexts(r)}");
        }
    }

    /// <summary>Windows addition: the period is GTK's storagePollSeconds.</summary>
    [Fact]
    public void ThePeriodIsTheGtkUis()
    {
        var preferences = GoContract.IntConstants(GoContract.Source("ui", "internal", "window", "preferences.go"));
        Assert.Equal(TimeSpan.FromSeconds(preferences["storagePollSeconds"]), StorageUsageController.DefaultInterval);
    }

    /// <summary>Asked when the window opens and then every period.</summary>
    [Fact]
    public async Task AsksAtOnceAndThenPeriodically()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(h.Controller.Start);
        await h.IdleAsync();
        Assert.Single(h.Log.Usages);
        Assert.Equal(new SystemStorageResult { TotalBytes = 3L << 30, SavedBytes = 1L << 30 }, h.Controller.Usage);
        await h.AdvanceAsync(Period);
        await h.AdvanceAsync(Period);
        Assert.Equal(3, h.Log.Usages.Count);
        Assert.True(h.Log.Errors.Count == 0 && h.Log.Unsupported == 0);

        // A second start does not add a second timer.
        await h.Ui.RunAsync(h.Controller.Start);
        await h.IdleAsync();
        var calls = h.Calls;
        await h.AdvanceAsync(Period);
        Assert.Equal(calls + 1, h.Calls);
        // Nothing before the period is up.
        await h.AdvanceAsync(Period - TimeSpan.FromMilliseconds(1));
        Assert.Equal(calls + 1, h.Calls);
    }

    /// <summary>
    /// One call at a time: refreshes asked for while one runs make one more
    /// call after it (a saved change must not go unmeasured).
    /// </summary>
    [Fact]
    public async Task OneCallAtATime()
    {
        await using var h = await Harness.StartAsync();
        var held = new HeldAnswer();
        h.Script.Hold = held;
        await h.Ui.RunAsync(() =>
        {
            h.Controller.Start();
            h.Controller.Refresh();
            h.Controller.Refresh();
        });
        await held.ArrivedAsync();
        h.Script.Hold = null;
        h.Script.Answer = new SystemStorageResult { TotalBytes = 1L << 30 };
        held.Release();
        await h.IdleAsync();
        Assert.Equal(2, h.Log.Usages.Count);
        Assert.Equal(1L << 30, h.Log.Usages[^1].TotalBytes);
        Assert.Equal(2, h.Calls); // the two refreshes made one call after the first
        // Once it answered, a refresh asks at once.
        await h.Ui.RunAsync(h.Controller.Refresh);
        await h.IdleAsync();
        Assert.Equal(3, h.Log.Usages.Count);
        Assert.Equal(3, h.Calls);
    }

    /// <summary>
    /// A failure replaces the details only and the polling goes on; the next
    /// answer brings the numbers back.
    /// </summary>
    [Fact]
    public async Task AFailureIsSaidAndThePollingGoesOn()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Failure = new RpcError { Code = ErrorCode.StorageError, Message = "disk" };
        await h.Ui.RunAsync(h.Controller.Start);
        await h.IdleAsync();
        await h.AdvanceAsync(Period);
        Assert.Equal(["Measuring the disk space failed", "Measuring the disk space failed"], h.Log.Errors);
        Assert.True(h.Log.Usages.Count == 0 && h.Log.Unsupported == 0);
        Assert.Null(h.Controller.Usage);
        h.Script.Failure = null;
        await h.AdvanceAsync(Period);
        Assert.Single(h.Log.Usages);
        Assert.Equal(2, h.Log.Errors.Count);
    }

    /// <summary>A daemon without system.storage hides the row and is not asked again.</summary>
    [Theory]
    [InlineData(ErrorCode.MethodNotFound)]
    [InlineData(ErrorCode.NotImplemented)]
    public async Task AnOlderDaemonHidesTheRow(int code)
    {
        await using var h = await Harness.StartAsync();
        h.Script.Failure = new RpcError { Code = code, Message = "no" };
        await h.Ui.RunAsync(h.Controller.Start);
        await h.IdleAsync();
        Assert.Equal(1, h.Log.Unsupported);
        Assert.True(h.Controller.IsUnsupported);
        await h.Ui.RunAsync(() =>
        {
            h.Controller.Refresh();
            h.Controller.Start();
        });
        await h.AdvanceAsync(Period);
        await h.AdvanceAsync(Period);
        Assert.Equal(1, h.Calls);
        Assert.True(h.Log.Unsupported == 1 && h.Log.Errors.Count == 0 && h.Log.Usages.Count == 0);
    }

    /// <summary>
    /// A daemon with no handler at all answers methodNotFound, as one from
    /// before system.storage does (Windows addition).
    /// </summary>
    [Fact]
    public async Task ADaemonThatDoesNotKnowTheMethod()
    {
        await using var h = await Harness.StartAsync(serve: false);
        await h.Ui.RunAsync(h.Controller.Start);
        await h.IdleAsync();
        Assert.True(h.Controller.IsUnsupported);
        Assert.Equal(1, h.Log.Unsupported);
    }

    /// <summary>Nothing is asked or reported once the window closed.</summary>
    [Fact]
    public async Task CloseStopsEverything()
    {
        await using var h = await Harness.StartAsync();
        var held = new HeldAnswer();
        h.Script.Hold = held;
        await h.Ui.RunAsync(h.Controller.Start);
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            h.Controller.Close();
            Assert.True(h.Controller.IsClosed);
        });
        held.Release();
        await h.IdleAsync();
        Assert.Empty(h.Log.Usages); // the answer in flight is dropped
        Assert.Equal(1, h.Calls);
        await h.Ui.RunAsync(() =>
        {
            h.Controller.Start();
            h.Controller.Refresh();
        });
        await h.AdvanceAsync(Period);
        Assert.Equal(1, h.Calls);
        Assert.Empty(h.Log.Usages);
    }

    /// <summary>The daemon's side of system.storage: what it answers, and whether it holds the answer.</summary>
    private sealed class StorageScript
    {
        private readonly object gate = new();
        private SystemStorageResult answer = new() { TotalBytes = 3L << 30, SavedBytes = 1L << 30 };
        private RpcError? failure;
        private HeldAnswer? hold;

        public SystemStorageResult Answer
        {
            get { lock (gate) { return answer; } }
            set { lock (gate) { answer = value; } }
        }

        public RpcError? Failure
        {
            get { lock (gate) { return failure; } }
            set { lock (gate) { failure = value; } }
        }

        public HeldAnswer? Hold
        {
            get { lock (gate) { return hold; } }
            set { lock (gate) { hold = value; } }
        }

        public async Task<string> ServeAsync()
        {
            if (Hold is { } held)
            {
                await held.WaitAsync();
            }
            if (Failure is { } f)
            {
                throw new RpcException(f);
            }
            return JsonCoding.EncodeToString(Answer);
        }
    }

    /// <summary>What the controller emitted, in order (the Swift suite's UsageLog).</summary>
    private sealed class UsageLog
    {
        public List<SystemStorageResult> Usages { get; } = [];

        public List<string> Errors { get; } = [];

        public int Unsupported { get; private set; }

        public void Attach(StorageUsageController c)
        {
            c.UsageChanged += (_, u) => Usages.Add(u);
            c.Failed += (_, text) => Errors.Add(text);
            c.Unsupported += (_, _) => Unsupported++;
        }
    }

    /// <summary>A fake daemon serving the script, the UI thread, a fake clock and a controller over a connected client.</summary>
    private sealed class Harness : IAsyncDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 28, 10, 0, 0, TimeSpan.Zero));

        public StorageScript Script { get; } = new();

        public UsageLog Log { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public StorageUsageController Controller { get; private set; } = null!;

        /// <summary>How many system.storage calls the daemon served.</summary>
        public int Calls => Fake.Calls.Count(m => m == API.SystemStorage.Name);

        public static async Task<Harness> StartAsync(bool serve = true)
        {
            var h = new Harness();
            if (serve)
            {
                h.Fake.On(API.SystemStorage.Name, _ => h.Script.ServeAsync());
            }
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Controller = await h.Ui.RunAsync(() =>
            {
                var c = new StorageUsageController(h.Client, Period, h.Time, pending: h.Pending);
                h.Log.Attach(c);
                return c;
            });
            return h;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>Lets everything settle, moves the clock on, and lets everything settle again.</summary>
        public async Task AdvanceAsync(TimeSpan by)
        {
            await IdleAsync();
            Time.Advance(by);
            await IdleAsync();
        }

        public async ValueTask DisposeAsync()
        {
            if (Controller is { } c)
            {
                await Ui.RunAsync(c.Close);
            }
            Client?.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of TriageScript, TriageDaemon and triageWait of
// macos/Tests/MalachiCoreTests/BoardPreferencesControllerTests.swift
// (shared with BoardTriageControllerTests.swift); GTK: ui/internal/
// boardtriage harness_test.go (the fake daemon). The daemon's side of the
// board's preferences and runs: board.preferences, board.setPreferences,
// board.runStart and board.runEnd from a script, each reply of the last
// three held at a gate while a test wants it. Thread-safe: the handlers run
// on the fake's threads. Also the default preferences (Swift's
// BoardPreferences()) and their comparison, as the record compares its
// account list by reference.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

/// <summary>A fake daemon answering the board's preferences and runs from a script, and a client connected to it.</summary>
internal sealed class BoardTriageDaemon : IAsyncDisposable
{
    private readonly Lock gate = new();
    private readonly List<BoardPreferences> sets = [];
    private readonly List<BoardRunStartParams> runStarts = [];
    private readonly List<BoardRunEndParams> runEnds = [];
    private BoardPreferences prefs = Prefs();
    private RpcException? getFailure;
    private RpcException? setFailure;
    private RpcException? runStartFailure;
    private int gets;

    private BoardTriageDaemon()
    {
        Fake.On(API.BoardPreferences.Name, (FakeDaemon.MethodHandler)(_ => GetAsync()));
        Fake.On(API.BoardSetPreferences.Name, (FakeDaemon.MethodHandler)SetAsync);
        Fake.On(API.BoardRunStart.Name, (FakeDaemon.MethodHandler)RunStartAsync);
        Fake.On(API.BoardRunEnd.Name, (FakeDaemon.MethodHandler)RunEndAsync);
    }

    public FakeDaemon Fake { get; } = new();

    public RpcClient Client { get; private set; } = null!;

    /// <summary>board.setPreferences replies wait here while held.</summary>
    public TriageGate Sets { get; } = new();

    /// <summary>board.runStart replies wait here while held.</summary>
    public TriageGate RunStarts { get; } = new();

    /// <summary>board.runEnd replies wait here while held (a daemon that does not answer).</summary>
    public TriageGate RunEnds { get; } = new();

    public BoardPreferences Preferences
    {
        get
        {
            lock (gate)
            {
                return prefs;
            }
        }
        set
        {
            lock (gate)
            {
                prefs = value;
            }
        }
    }

    public RpcException? GetFailure
    {
        set
        {
            lock (gate)
            {
                getFailure = value;
            }
        }
    }

    public RpcException? SetFailure
    {
        set
        {
            lock (gate)
            {
                setFailure = value;
            }
        }
    }

    public RpcException? RunStartFailure
    {
        set
        {
            lock (gate)
            {
                runStartFailure = value;
            }
        }
    }

    public int Gets
    {
        get
        {
            lock (gate)
            {
                return gets;
            }
        }
    }

    public IReadOnlyList<BoardPreferences> SetCalls
    {
        get
        {
            lock (gate)
            {
                return [.. sets];
            }
        }
    }

    public IReadOnlyList<BoardRunStartParams> RunStartCalls
    {
        get
        {
            lock (gate)
            {
                return [.. runStarts];
            }
        }
    }

    public IReadOnlyList<BoardRunEndParams> RunEndCalls
    {
        get
        {
            lock (gate)
            {
                return [.. runEnds];
            }
        }
    }

    /// <summary>Swift's BoardPreferences(...): the daemon's defaults with the given changes.</summary>
    public static BoardPreferences Prefs(
        bool enabled = true, bool assistant = false, int hot = BoardLimits.DefaultBoardHotDays, bool autoTriage = false,
        int autoTriageMinutes = BoardLimits.DefaultBoardAutoTriageMinutes,
        int autoTriageDailyCases = BoardLimits.DefaultBoardAutoTriageDailyCases) => new()
        {
            Enabled = enabled,
            Assistant = assistant,
            Windows = new BoardWindows
            {
                Hot = hot,
                You = BoardLimits.DefaultBoardYouDays,
                Them = BoardLimits.DefaultBoardThemDays,
                Info = BoardLimits.DefaultBoardInfoDays,
            },
            AutoTriage = autoTriage,
            AutoTriageMinutes = autoTriageMinutes,
            AutoTriageDailyCases = autoTriageDailyCases,
        };

    /// <summary>The daemon's error <paramref name="code"/>.</summary>
    public static RpcException Error(int code, string message) => new(new RpcError { Code = code, Message = message });

    /// <summary>Asserts that the preferences hold the same (Go's samePrefs).</summary>
    public static void Same(BoardPreferences? expected, BoardPreferences? actual) =>
        Assert.True(BoardPreferencesController.Same(expected, actual), $"expected {expected}, got {actual}");

    /// <summary>Asserts that the preferences sent are these, in order.</summary>
    public static void Same(IReadOnlyList<BoardPreferences> expected, IReadOnlyList<BoardPreferences> actual)
    {
        Assert.Equal(expected.Count, actual.Count);
        for (var i = 0; i < expected.Count; i++)
        {
            Same(expected[i], actual[i]);
        }
    }

    public static async Task<BoardTriageDaemon> StartAsync()
    {
        var d = new BoardTriageDaemon();
        await d.Fake.StartAsync();
        d.Client = new RpcClient(d.Fake.Path, PortableKeyFilePolicy.Instance);
        await d.Client.ConnectAsync(TestContext.Current.CancellationToken);
        return d;
    }

    /// <summary>Waits until <paramref name="condition"/> holds on the UI thread (Swift's triageWait, for a reply held on purpose).</summary>
    public static async Task UntilAsync(TestUIContext ui, Func<bool> condition, string what = "the condition")
    {
        var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(20);
        while (!await ui.RunAsync(condition))
        {
            if (DateTime.UtcNow > deadline)
            {
                throw new TimeoutException(what + " did not hold in time");
            }
            await Task.Delay(2);
        }
    }

    public async ValueTask DisposeAsync()
    {
        Sets.Hold(false);
        RunStarts.Hold(false);
        RunEnds.Hold(false);
        Client.Dispose();
        await Fake.DisposeAsync();
    }

    private Task<string> GetAsync()
    {
        lock (gate)
        {
            gets++;
            if (getFailure is { } f)
            {
                throw f;
            }
            return Task.FromResult(JsonCoding.EncodeToString(new BoardPreferencesResult { Preferences = prefs }));
        }
    }

    private async Task<string> SetAsync(string p)
    {
        var q = JsonCoding.Decode<BoardSetPreferencesParams>(p);
        lock (gate)
        {
            sets.Add(q.Preferences);
        }
        await Sets.PassAsync();
        lock (gate)
        {
            if (setFailure is { } f)
            {
                throw f;
            }
            prefs = q.Preferences;
            return JsonCoding.EncodeToString(new BoardSetPreferencesResult { Preferences = prefs });
        }
    }

    private async Task<string> RunStartAsync(string p)
    {
        var q = JsonCoding.Decode<BoardRunStartParams>(p);
        int n;
        lock (gate)
        {
            runStarts.Add(q);
            n = runStarts.Count;
        }
        await RunStarts.PassAsync();
        lock (gate)
        {
            if (runStartFailure is { } f)
            {
                throw f;
            }
        }
        return JsonCoding.EncodeToString(new BoardRunStartResult { RunId = new BoardRunId("run_" + n) });
    }

    private async Task<string> RunEndAsync(string p)
    {
        var q = JsonCoding.Decode<BoardRunEndParams>(p);
        lock (gate)
        {
            runEnds.Add(q);
        }
        await RunEnds.PassAsync();
        return "{}";
    }
}

/// <summary>A hold of the fake's replies, released or never closed.</summary>
internal sealed class TriageGate
{
    private readonly Lock gate = new();
    private bool holding;
    private int waiting;
    private TaskCompletionSource released = new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <summary>Calls waiting at the gate.</summary>
    public int Waiting
    {
        get
        {
            lock (gate)
            {
                return waiting;
            }
        }
    }

    /// <summary>Holds the replies from now on, or lets them through again and releases the ones held.</summary>
    public void Hold(bool on)
    {
        TaskCompletionSource? release = null;
        lock (gate)
        {
            if (on && !holding)
            {
                holding = true;
                released = new(TaskCreationOptions.RunContinuationsAsynchronously);
            }
            else if (!on)
            {
                holding = false;
                release = released;
            }
        }
        release?.TrySetResult();
    }

    public async Task PassAsync()
    {
        Task wait;
        lock (gate)
        {
            if (!holding)
            {
                return;
            }
            waiting++;
            wait = released.Task;
        }
        await wait.WaitAsync(TimeSpan.FromSeconds(30));
        lock (gate)
        {
            waiting--;
        }
    }
}

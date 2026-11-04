// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ClaudeDesktopController.swift
// (ClaudeDesktopController, Answer, change, restartPending, writeOwn,
// terminated, the serialised operations); no GTK counterpart: the GTK app
// does not hand mail to Claude Desktop, so it offers no restart. The texts
// are ui/internal/assistant's (RestartTexts).
//
// Windows differences. A restart waits DefaultQuitTimeout (45 s) where
// macOS waits 20 s: Claude Desktop quits through the Restart Manager's
// session end (Malachi.Platform.Windows.Claude.ClaudeDesktopApp), which an
// Electron app answered in about 20 s in the measurement; a quit that
// comes later still counts through the termination (Terminated). The Swift
// callbacks are events of the same words (onChange is Changed, onToast
// ToastRequested, onStatus StatusReported); the writes are
// Func<bool, Task<McpStatus?>> (Swift's Write), the page's being
// McpRegistrationController.ChangeAsync.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// Claude Desktop around a change of "Register with Claude" (the MCP switch
/// of Preferences → AI, <see cref="McpRegistrationController"/>). Claude
/// Desktop reads its MCP servers only when it starts and, while it runs,
/// rewrites its configuration file from memory, so an entry that
/// <c>malachi-mcp install</c> writes, or <c>uninstall</c> removes, while it
/// runs is undone at its next write (docs/mcp.md). Claude Code does not do
/// this.
/// </summary>
/// <remarks>
/// <para>
/// A flip of the switch while Claude Desktop runs and the bridge's status
/// has it as a present client (<see cref="OffersRestart"/>) asks first
/// (<see cref="Assistant.RestartTexts"/>). Restart asks it to quit, waits
/// until it has gone (at most the quit timeout), writes the change and
/// starts it again; when it did not quit in time a toast says so and the
/// change is written anyway and stays pending. Later writes the change now
/// and keeps it pending. A pending change (<see cref="Pending"/>: the
/// registration Claude Desktop still has to get) has a row under the switch
/// whose Restart is the same restart (<see cref="RestartPendingAsync"/>),
/// and is written once more as soon as Claude Desktop quits by itself
/// (<see cref="Terminated"/>, which the application calls when it notices):
/// then the write sticks, and its next start loads it. Pending lives for the
/// application's run only.
/// </para>
/// <para>
/// The writes go one at a time, whether through the page's registration (so
/// its switch and row follow) or this controller's own
/// (<see cref="WriteOwnAsync"/>, the termination's: a
/// <see cref="McpRegistrationController"/> without toasts);
/// <see cref="IsBusy"/> while one runs or a restart waits. Every status a
/// write reports goes to <see cref="StatusReported"/>, which the
/// application hands to the Assistant state. Create it, and call it, on the
/// UI thread.
/// </para>
/// </remarks>
public sealed partial class ClaudeDesktopController : IDisposable
{
    /// <summary>How long a restart waits for Claude Desktop to quit (Windows; macOS waits 20 s).</summary>
    public static readonly TimeSpan DefaultQuitTimeout = TimeSpan.FromSeconds(45);

    private readonly ClaudeDesktopPlatform platform;
    private readonly TimeSpan quitTimeout;
    private readonly Func<bool, Task<McpStatus?>> own;
    private readonly McpRegistrationController? ownRegistration;
    private readonly ControllerScope scope;
    private readonly ILogger logger;

    // One operation at a time: whether one runs, and who waits.
    private readonly Queue<TaskCompletionSource> waiting = new();
    private bool running;

    // A restart runs: the termination it causes is its own.
    private bool restarting;

    /// <summary>
    /// A controller over <paramref name="platform"/> whose own write (the
    /// termination's, <see cref="WriteOwnAsync"/>) is <paramref name="write"/>.
    /// Created on the UI thread.
    /// </summary>
    public ClaudeDesktopController(
        ClaudeDesktopPlatform platform,
        Func<bool, Task<McpStatus?>> write,
        TimeSpan? quitTimeout = null,
        ILogger<ClaudeDesktopController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(platform);
        ArgumentNullException.ThrowIfNull(write);
        this.platform = platform;
        own = write;
        this.quitTimeout = quitTimeout ?? DefaultQuitTimeout;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// A controller whose own write is an <see cref="McpRegistrationController"/>
    /// of its own over <paramref name="bridge"/> (<see cref="Daemon.Paths.McpBridge"/>;
    /// null when there is none beside the application): no toasts, a failure
    /// is logged.
    /// </summary>
    public ClaudeDesktopController(
        string? bridge,
        ClaudeDesktopPlatform platform,
        BridgeRunner? runner = null,
        TimeSpan? timeout = null,
        ClaudeDesktopPackage? claudeDesktop = null,
        TimeSpan? quitTimeout = null,
        ILogger<ClaudeDesktopController>? logger = null,
        PendingWork? pending = null)
        : this(platform, NoWrite, quitTimeout, logger, pending)
    {
        var registration = new McpRegistrationController(bridge, runner, timeout, claudeDesktop: claudeDesktop, pending: pending);
        ownRegistration = registration;
        own = registration.ChangeAsync;
    }

    /// <summary>What the user answered to "Restart Claude Desktop?".</summary>
    public enum Answer
    {
        /// <summary>Quit Claude Desktop, write, start it again.</summary>
        Restart,

        /// <summary>Write now; the change stays pending while Claude Desktop runs.</summary>
        Later,
    }

    /// <summary><see cref="Pending"/> or <see cref="IsBusy"/> changed; the AI page listens while it is open.</summary>
    public event EventHandler? Changed;

    /// <summary>The text of a toast (Claude Desktop did not quit); the AI page shows it while open, otherwise it is only logged.</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>Every status a write reported.</summary>
    public event EventHandler<McpStatus>? StatusReported;

    /// <summary>The registration Claude Desktop still has to get: true registered, false unregistered, null nothing pending.</summary>
    public bool? Pending { get; private set; }

    /// <summary>A write runs or a restart waits: the switch and the pending row's Restart wait too.</summary>
    public bool IsBusy { get; private set; }

    /// <summary>Nothing runs and nothing is reported afterwards.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>
    /// Stops: nothing runs or is reported afterwards, late answers are
    /// dropped, whoever waits for a write goes on without it.
    /// </summary>
    public void Close()
    {
        if (IsClosed)
        {
            return;
        }
        scope.Close();
        ownRegistration?.Close();
        Changed = null;
        ToastRequested = null;
        StatusReported = null;
        var released = waiting.ToArray();
        waiting.Clear();
        running = false;
        foreach (var w in released)
        {
            w.TrySetResult();
        }
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    // The switch

    /// <summary>
    /// Whether a change of the registration offers the restart first: Claude
    /// Desktop runs, and <paramref name="status"/> (the page's last from the
    /// bridge) has it as a present client.
    /// </summary>
    public bool OffersRestart(McpStatus? status)
    {
        if (IsClosed || status is null)
        {
            return false;
        }
        var desktop = AssistantTarget.Desktop.ClientId();
        var present = false;
        foreach (var client in status.Clients)
        {
            present |= client.Id == desktop && client.Present;
        }
        return present && platform.IsRunning();
    }

    /// <summary>
    /// The AI page's switch flipped to <paramref name="want"/>.
    /// <paramref name="status"/> is the page's last status,
    /// <paramref name="write"/> the page's own write (its switch and row
    /// follow it). Without the offer (<see cref="OffersRestart"/>) the change
    /// is written at once; otherwise <paramref name="ask"/> decides: the
    /// restart, or the write now with the change pending while Claude Desktop
    /// runs.
    /// </summary>
    public async Task ChangeAsync(bool want, McpStatus? status, Func<Task<Answer>> ask, Func<bool, Task<McpStatus?>> write)
    {
        ArgumentNullException.ThrowIfNull(ask);
        ArgumentNullException.ThrowIfNull(write);
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        if (!OffersRestart(status))
        {
            await SerializedAsync(() => WriteNowAsync(want, write, later: false));
            return;
        }
        var answer = await ask();
        if (IsClosed)
        {
            return;
        }
        if (answer == Answer.Restart)
        {
            await SerializedAsync(() => RestartAsync(want, write));
        }
        else
        {
            await SerializedAsync(() => WriteNowAsync(want, write, later: true));
        }
    }

    /// <summary>
    /// The pending row's Restart: the restart with the pending state, through
    /// <paramref name="write"/> (the page's). Nothing when nothing is pending
    /// by the time it runs.
    /// </summary>
    public Task RestartPendingAsync(Func<bool, Task<McpStatus?>> write)
    {
        ArgumentNullException.ThrowIfNull(write);
        scope.VerifyAccess();
        return SerializedAsync(() => Pending is { } want ? RestartAsync(want, write) : Task.CompletedTask);
    }

    /// <summary>
    /// A write through this controller's own registration: for a page whose
    /// registration closed with its window while a restart waited. Not
    /// serialised itself; it runs inside the operation that calls it.
    /// </summary>
    public Task<McpStatus?> WriteOwnAsync(bool want) => IsClosed ? Task.FromResult<McpStatus?>(null) : own(want);

    // Claude Desktop quit

    /// <summary>
    /// Claude Desktop has gone (the application noticed it): a pending change
    /// is written once more now that it does not run, then no longer
    /// pending. The quit of a restart is the restart's own; nothing pending,
    /// nothing to do.
    /// </summary>
    public void Terminated()
    {
        scope.VerifyAccess();
        if (IsClosed || restarting || Pending is null)
        {
            return;
        }
        scope.Run(_ => ReapplyAsync());
    }

    private Task ReapplyAsync() => SerializedAsync(async () =>
    {
        // A restart queued before may have written it already, or Claude
        // Desktop runs again: then the write would not stick.
        if (Pending is not { } want || platform.IsRunning())
        {
            return;
        }
        var s = await own(want);
        if (s is null)
        {
            LogReapplyFailed(logger);
            return;
        }
        if (IsClosed)
        {
            return;
        }
        StatusReported?.Invoke(this, s);
        SetPending(null);
    });

    // Internals

    // Writes want now. While Claude Desktop runs, later keeps the change
    // pending (it overwrites the change); once it does not run the change
    // sticks and nothing is pending.
    private async Task WriteNowAsync(bool want, Func<bool, Task<McpStatus?>> write, bool later)
    {
        var s = await write(want);
        if (s is null || IsClosed)
        {
            return;
        }
        StatusReported?.Invoke(this, s);
        if (!platform.IsRunning())
        {
            SetPending(null);
        }
        else if (later)
        {
            SetPending(want);
        }
    }

    // Quit, wait, write, start. When Claude Desktop did not quit in time the
    // change is written anyway and stays pending; a failed write leaves
    // Pending as it was, and a Claude Desktop that quit is started again
    // either way.
    private async Task RestartAsync(bool want, Func<bool, Task<McpStatus?>> write)
    {
        restarting = true;
        try
        {
            var quit = await platform.Quit(quitTimeout, CancellationToken.None);
            if (IsClosed)
            {
                return;
            }
            if (!quit)
            {
                LogDidNotQuit(logger, quitTimeout.TotalSeconds);
                ToastRequested?.Invoke(this, Assistant.RestartTexts().NotQuit);
            }
            var s = await write(want);
            if (s is not null)
            {
                if (IsClosed)
                {
                    return;
                }
                StatusReported?.Invoke(this, s);
                SetPending(quit ? null : want);
            }
            if (IsClosed || !quit)
            {
                return;
            }
            platform.Launch();
        }
        finally
        {
            restarting = false;
        }
    }

    // Runs op once no other operation runs, busy meanwhile; nothing once
    // closed.
    private async Task SerializedAsync(Func<Task> op)
    {
        if (running)
        {
            var turn = new TaskCompletionSource();
            waiting.Enqueue(turn);
            await turn.Task;
        }
        else
        {
            running = true;
        }
        try
        {
            if (IsClosed)
            {
                return;
            }
            SetBusy(true);
            await op();
        }
        finally
        {
            HandOver();
        }
    }

    // The next waiting operation runs, or none is busy.
    private void HandOver()
    {
        if (IsClosed)
        {
            return;
        }
        if (waiting.Count == 0)
        {
            running = false;
            SetBusy(false);
        }
        else
        {
            waiting.Dequeue().TrySetResult();
        }
    }

    private void SetPending(bool? pending)
    {
        if (pending == Pending)
        {
            return;
        }
        Pending = pending;
        Changed?.Invoke(this, EventArgs.Empty);
    }

    private void SetBusy(bool busy)
    {
        if (busy == IsBusy)
        {
            return;
        }
        IsBusy = busy;
        Changed?.Invoke(this, EventArgs.Empty);
    }

    // The own write of the first constructor until the second replaces it.
    private static Task<McpStatus?> NoWrite(bool want) => Task.FromResult<McpStatus?>(null);

    [LoggerMessage(Level = LogLevel.Warning, Message = "Claude Desktop did not quit within {Seconds} s; writing the MCP registration anyway")]
    private static partial void LogDidNotQuit(ILogger logger, double seconds);

    [LoggerMessage(Level = LogLevel.Warning, Message = "writing the MCP registration again after Claude Desktop quit failed; it stays pending")]
    private static partial void LogReapplyFailed(ILogger logger);
}

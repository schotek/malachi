// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/StorageUsageController.swift;
// GTK: ui/internal/window/preferences.go (bindStorage, storagePollSeconds).
//
// The Swift callbacks are events (onUsage is UsageChanged, onUnsupported
// Unsupported, onError Failed); the state is observable as well. The timer
// waits on the injected TimeProvider, as SyncController's refresher does,
// and runs detached (docs/windows-port.md §7): a fake clock would hold a
// tracked wait for ever. An older daemon is told by Download's
// MethodUnsupported, which the attachments' download shares, as Swift
// shares Download.swift's methodUnsupported.

using System;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The Disk Space Used row of the preferences (preferences.go
/// <c>bindStorage</c>) without the widgets: <c>system.storage</c> when the
/// window opens (<see cref="Start"/>), again after every change the daemon
/// confirmed (<see cref="Refresh"/>), and every <see cref="DefaultInterval"/>
/// while the window is open.
/// </summary>
/// <remarks>
/// One call at a time: a refresh asked for while one runs follows it, so a
/// saved change never goes unmeasured. The page renders
/// <see cref="UsageChanged"/> through <see cref="StorageUsage.StorageTexts"/>;
/// a failure replaces only the details (<see cref="Failed"/>, the value stays
/// as it was) and the next answer puts them back; a daemon without the
/// method (methodNotFound, notImplemented) hides the row and is not asked
/// again (<see cref="Unsupported"/>). Create it, and call it, on the UI
/// thread.
/// </remarks>
public sealed partial class StorageUsageController : ObservableObject, IDisposable
{
    /// <summary>How often the row is refreshed while the window is open (preferences.go <c>storagePollSeconds</c>).</summary>
    public static readonly TimeSpan DefaultInterval = TimeSpan.FromSeconds(5);

    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly TimeProvider time;
    private readonly TimeSpan interval;

    // A call is running; again: a refresh was asked for meanwhile and
    // follows it.
    private bool inFlight;
    private bool again;

    // The periodic refresh, while it runs.
    private CancellationTokenSource? timer;

    /// <summary>A controller over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="interval">The period of the refresh; <see cref="DefaultInterval"/> when null.</param>
    /// <param name="time">The clock of the period; the system's when null.</param>
    /// <param name="logger">Receives the method's name and error codes.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public StorageUsageController(
        RpcClient client, TimeSpan? interval = null, TimeProvider? time = null, ILogger<StorageUsageController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.interval = interval ?? DefaultInterval;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>Called with every answer (Swift <c>onUsage</c>).</summary>
    public event EventHandler<SystemStorageResult>? UsageChanged;

    /// <summary>Called once when the daemon does not offer <c>system.storage</c> (Swift <c>onUnsupported</c>).</summary>
    public event EventHandler? Unsupported;

    /// <summary>
    /// Called with the sentence of a failed call, anything but a daemon
    /// without the method (Swift <c>onError</c>).
    /// </summary>
    public event EventHandler<string>? Failed;

    /// <summary>The last answer; null until the first one.</summary>
    [ObservableProperty]
    public partial SystemStorageResult? Usage { get; private set; }

    /// <summary>The daemon has no <c>system.storage</c>; nothing is asked any more.</summary>
    [ObservableProperty]
    public partial bool IsUnsupported { get; private set; }

    /// <summary>The window closed: the timer stopped, late replies dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Asks now and then every period until <see cref="Close"/>; a second call starts no second timer.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (IsClosed || timer is not null)
        {
            return;
        }
        Refresh();
        if (IsUnsupported)
        {
            return;
        }
        var cts = new CancellationTokenSource();
        timer = cts;
        scope.RunDetached(async lifetime =>
        {
            try
            {
                using var linked = CancellationTokenSource.CreateLinkedTokenSource(lifetime, cts.Token);
                while (true)
                {
                    await Task.Delay(interval, time, linked.Token);
                    if (cts.IsCancellationRequested || IsClosed || IsUnsupported)
                    {
                        return;
                    }
                    Refresh();
                }
            }
            catch (OperationCanceledException) when (cts.IsCancellationRequested)
            {
                // Stopped: an older daemon, or closed.
            }
            finally
            {
                if (timer == cts)
                {
                    timer = null;
                }
                cts.Dispose();
            }
        });
    }

    /// <summary>Asks <c>system.storage</c> now, or right after the call that is running.</summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (IsClosed || IsUnsupported)
        {
            return;
        }
        if (inFlight)
        {
            again = true;
            return;
        }
        inFlight = true;
        scope.Perform(client, API.SystemStorage, new EmptyParams(), outcome =>
        {
            inFlight = false;
            if (outcome.TryGetValue(out var res, out var error))
            {
                Usage = res;
                UsageChanged?.Invoke(this, res);
            }
            else if (Download.MethodUnsupported(error))
            {
                IsUnsupported = true;
                StopTimer();
                Unsupported?.Invoke(this, EventArgs.Empty);
            }
            else
            {
                // Asked every few seconds: a failure is not worth a warning each time.
                if (logger.IsEnabled(LogLevel.Debug))
                {
                    var (kind, daemonError) = RpcErrorText.Classify(error);
                    LogCallFailed(logger, API.SystemStorage.Name, kind, daemonError?.Code.Value ?? 0);
                }
                Failed?.Invoke(this, RpcErrorText.Text(L10n.T("Measuring the disk space"), error));
            }
            if (again && !IsUnsupported)
            {
                again = false;
                Refresh();
            }
        });
    }

    /// <summary>Stops the timer; nothing is asked or reported afterwards.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        StopTimer();
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    private void StopTimer()
    {
        if (timer is { } cts)
        {
            timer = null;
            cts.Cancel();
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogCallFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);
}

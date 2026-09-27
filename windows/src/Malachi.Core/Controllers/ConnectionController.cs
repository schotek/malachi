// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ConnectionController.swift
// (ConnectionController and describe); GTK: ui/internal/window/window.go
// (the reconnect timer, reconnect, showConnectionState, fetchSystemInfo)
// and connection.go (nextConnView, backendProtocol, logConnection,
// maxConnWarned).
//
// Swift's Tasks become the work of a ControllerScope, which starts after
// the caller's turn as a Task does (docs/windows-port.md §7.2): a transport
// that fails at once still finds the attempt handle set, and clears it. The
// reconnect loop and the two stream readers are detached (they live as long
// as the controller, and the loop waits on the TimeProvider); an attempt and
// the system.info check are tracked. The client's streams are read with
// WaitToReadAsync on the UI thread's context, so that every state and
// notification is handled on the UI thread in the daemon's order, before
// whatever the transport completes after writing it. stop() cancels the
// loop and the attempt first and the readers last; here StopAsync closes
// the scope at once, which ends all of them: a state still buffered then is
// one that Swift's report() (guarded by stopping) ignores, and a
// notification one for a window on its way out. Swift's Task.sleep is a
// wait on the injected TimeProvider; its os.Logger is an ILogger, at the
// same levels.
//
// Swift's callbacks cannot throw; the handlers of StateChanged,
// NotificationReceived and PropertyChanged can, and are isolated
// (ControllerEvents): a handler's failure is reported and neither leaves a
// state change half done nor ends the readers or the retry loop, which
// also guard every item and tick.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// Keeps the app connected to malachid, the counterpart of the reconnect
/// loop and connection status of ui/internal/window/window.go.
/// </summary>
/// <remarks>
/// <para>
/// Owns the <see cref="RpcClient"/>'s streams and the
/// <see cref="DaemonSupervisor"/>: on <see cref="Start"/> it brings the
/// daemon up (or adopts a running one), dials (the client authenticates the
/// connection and compares the protocol version in the handshake), checks
/// <c>system.info</c>, and while there is no connection retries every
/// <see cref="ReconnectInterval"/>. Notifications and state changes are
/// consumed from the client's streams and handed to
/// <see cref="NotificationReceived"/> and <see cref="StateChanged"/> on the
/// UI thread; decoding a notification is the consumer's job.
/// </para>
/// <para>
/// An attempt ends connected, as a protocol mismatch or as unavailable. A
/// mismatch stays up across the retries (no "Connecting…" every few
/// seconds) until an attempt ends otherwise; a connection ends it at once.
/// A refused handshake is logged at error level once per distinct reason
/// until the next connection; the routine failures while the daemon is
/// down stay at debug level.
/// </para>
/// <para>
/// A handler of <see cref="StateChanged"/>, <see cref="NotificationReceived"/>
/// or <see cref="ObservableObject.PropertyChanged"/> that throws is reported
/// by <see cref="Pending"/> (logged at error level) and stops nothing: the
/// other handlers are called, the state change completes, and the stream
/// readers and the retry loop go on.
/// </para>
/// <para>
/// UI-thread-affine (docs/windows-port.md §7.1): create it and call it on
/// the UI thread.
/// </para>
/// </remarks>
public sealed partial class ConnectionController : ObservableObject, IDisposable
{
    /// <summary>How long system.info may take, as in the GTK UI (Swift <c>infoTimeout</c>).</summary>
    public static readonly TimeSpan InfoTimeout = RpcTimeouts.SystemInfo;

    /// <summary>How often a dead socket is retried, as in the GTK UI (window.go <c>reconnectInterval</c>).</summary>
    public static readonly TimeSpan DefaultReconnectInterval = TimeSpan.FromSeconds(5);

    /// <summary>
    /// How many refusals <see cref="LoggedRefusals"/> remembers before it
    /// starts afresh (window/connection.go <c>maxConnWarned</c>): the peer on
    /// the socket chooses the error codes of its refusals, and so their texts.
    /// </summary>
    private const int MaxLoggedRefusals = 16;

    private readonly ControllerScope scope;
    private readonly TimeProvider time;
    private readonly ILogger logger;
    private readonly List<string> loggedRefusals = [];

    // The attempt in flight (Swift `attempt`); cleared by the attempt itself.
    private Task? attempt;

    // What the client last reported; the retry loop keys off it.
    private bool clientConnected;

    // Bumped on every connect and disconnect so a late system.info reply of
    // an earlier connection is dropped.
    private int generation;
    private bool started;
    private bool stopping;

    /// <summary>A controller over <paramref name="client"/>, not started yet.</summary>
    /// <param name="client">The transport; its streams are consumed here, so nobody else may read them.</param>
    /// <param name="supervisor">Brings the daemon up before each dial; null to only ever dial.</param>
    /// <param name="reconnectInterval">The retry period while disconnected (<see cref="DefaultReconnectInterval"/>).</param>
    /// <param name="timeProvider">The clock of the retry loop.</param>
    /// <param name="logger">Receives reasons and codes, never a key or mail data.</param>
    /// <param name="pending">Counts the controller's background work (a tracker of its own when null).</param>
    public ConnectionController(
        RpcClient client,
        DaemonSupervisor? supervisor,
        TimeSpan? reconnectInterval = null,
        TimeProvider? timeProvider = null,
        ILogger<ConnectionController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        Client = client;
        Supervisor = supervisor;
        ReconnectInterval = reconnectInterval ?? DefaultReconnectInterval;
        time = timeProvider ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        State = new ConnectionState.Connecting();
    }

    /// <summary>
    /// Called on every state change, after <see cref="State"/> was updated,
    /// and for every attempt's "Connecting…" (Swift <c>onState</c>).
    /// </summary>
    public event EventHandler<ConnectionState>? StateChanged;

    /// <summary>Called for every daemon notification, in order (Swift <c>onNotification</c>).</summary>
    public event EventHandler<RpcNotification>? NotificationReceived;

    /// <summary>The transport.</summary>
    public RpcClient Client { get; }

    /// <summary>Brings the daemon up before each dial; null when the controller only dials.</summary>
    public DaemonSupervisor? Supervisor { get; }

    /// <summary>The retry period while disconnected.</summary>
    public TimeSpan ReconnectInterval { get; }

    /// <summary>Counts the controller's background work; tests wait on it.</summary>
    public PendingWork Pending => scope.Pending;

    /// <summary>
    /// The handshake refusals logged at error level since the last
    /// connection, in order: the retry loop meets the same refusal every few
    /// seconds, and the log names each one once (the repeats go to debug).
    /// Swift keeps it internal for its tests.
    /// </summary>
    public IReadOnlyList<string> LoggedRefusals => [.. loggedRefusals];

    /// <summary>The connection as the status line shows it (Swift <c>state</c>).</summary>
    [ObservableProperty]
    public partial ConnectionState State { get; private set; }

    // Whether the state is a protocol mismatch, which ReconnectNow keeps up
    // and a connection ends.
    private bool ShowsMismatch => State is ConnectionState.ProtocolMismatch;

    /// <summary>Connects now and keeps retrying while disconnected. Idempotent.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (started || stopping)
        {
            return;
        }
        started = true;
        scope.RunDetached(ReadStatesAsync);
        scope.RunDetached(ReadNotificationsAsync);
        ReconnectNow();
        scope.RunDetached(ReconnectLoopAsync);
    }

    /// <summary>
    /// Starts a connection attempt at once unless one is in flight or the
    /// client is connected.
    /// </summary>
    public void ReconnectNow()
    {
        scope.VerifyAccess();
        if (stopping || attempt is not null || clientConnected)
        {
            return;
        }
        // Every attempt announces itself, as the GTK client's Connect does,
        // even when the previous state was already Connecting; a protocol
        // mismatch stays up instead until an attempt ends otherwise, so the
        // line does not flip every few seconds.
        if (!ShowsMismatch)
        {
            State = new ConnectionState.Connecting();
            scope.Raise(StateChanged, this, State);
        }
        // Run starts the work after this turn: the handle is set before the
        // attempt can end and clear it, however fast it fails.
        attempt = scope.Run(async ct =>
        {
            try
            {
                await ConnectOnceAsync(ct);
            }
            finally
            {
                attempt = null;
            }
        });
    }

    /// <summary>
    /// Stops retrying, closes the connection and stops the daemon this app
    /// started. The state stays <see cref="ConnectionState.Stopping"/>.
    /// </summary>
    public async Task StopAsync()
    {
        scope.VerifyAccess();
        stopping = true;
        SetState(new ConnectionState.Stopping());
        // The loop, the attempt in flight and the stream readers end with the
        // scope (Swift cancels the loop and the attempt here, the readers
        // once the daemon is stopped; what they would still hand over is
        // dropped by report() then).
        scope.Close();
        attempt = null;
        Client.Close();
        if (Supervisor is not null)
        {
            await Supervisor.StopAsync();
        }
    }

    /// <summary>
    /// Ends the controller's background work without stopping the daemon;
    /// <see cref="StopAsync"/> is the way out of a running app.
    /// </summary>
    public void Dispose() => scope.Dispose();

    // Internals

    /// <summary>
    /// One attempt: daemon up, then dial. Failures are routine while the
    /// daemon is down or backing off, so they are logged at debug level; a
    /// daemon that answers but refuses the handshake, or is refused by it,
    /// is not, and is logged once (<see cref="LogRefusal"/>). A daemon of
    /// another protocol is the mismatch, anything else the client refused is
    /// unavailable.
    /// </summary>
    private async Task ConnectOnceAsync(CancellationToken cancellationToken)
    {
        if (Supervisor is not null)
        {
            try
            {
                await Supervisor.EnsureAsync(cancellationToken);
            }
#pragma warning disable CA1031 // Every failure of the start is a state, as Swift's catch-all is.
            catch (Exception e)
#pragma warning restore CA1031
            {
                var reason = Describe(e);
                LogNotStarted(logger, reason);
                Report(new ConnectionState.Unavailable(reason));
                return;
            }
        }
        try
        {
            await Client.ConnectAsync(cancellationToken);
        }
        catch (HandshakeException refusal)
        {
            LogRefusal(refusal.Message);
            if (refusal.Error.Reason == HandshakeReason.ProtocolMismatch)
            {
                Report(new ConnectionState.ProtocolMismatch(refusal.Error.Daemon));
            }
            else
            {
                Report(new ConnectionState.Unavailable(refusal.Message));
            }
        }
#pragma warning disable CA1031 // Every other failure of the dial is a state, as Swift's catch-all is.
        catch (Exception e)
#pragma warning restore CA1031
        {
            var reason = Describe(e);
            LogUnavailable(logger, reason);
            Report(new ConnectionState.Unavailable(reason));
        }
    }

    /// <summary>
    /// Logs a refused handshake at error level the first time since the last
    /// connection, at debug level when the retry loop meets it again. The
    /// descriptions carry no key, nonce or proof.
    /// </summary>
    private void LogRefusal(string description)
    {
        if (loggedRefusals.Contains(description))
        {
            LogRefusedAgain(logger, description);
            return;
        }
        if (loggedRefusals.Count >= MaxLoggedRefusals)
        {
            loggedRefusals.Clear();
        }
        loggedRefusals.Add(description);
        LogRefused(logger, description);
    }

    // Swift `for await s in client.states`: the only reader of the stream,
    // so no state may end it. WaitToReadAsync is awaited on the UI thread's
    // context, so a state the transport writes is posted there before
    // anything it completes after it (the attempt's ConnectAsync, say).
    private async Task ReadStatesAsync(CancellationToken cancellationToken)
    {
        ChannelReader<RpcClientState> states = Client.States;
        while (await states.WaitToReadAsync(cancellationToken))
        {
            while (states.TryRead(out var s))
            {
                scope.Guard(() => ClientStateChanged(s));
            }
        }
    }

    // Swift `for await n in client.notifications`: forwarded in order, each
    // handler isolated, so a notification a view fails on (one it cannot
    // decode, say) does not end the stream.
    private async Task ReadNotificationsAsync(CancellationToken cancellationToken)
    {
        ChannelReader<RpcNotification> notifications = Client.Notifications;
        while (await notifications.WaitToReadAsync(cancellationToken))
        {
            while (notifications.TryRead(out var n))
            {
                scope.Raise(NotificationReceived, this, n);
            }
        }
    }

    // Swift's `loop`: every interval, a new attempt while disconnected.
    private async Task ReconnectLoopAsync(CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            await Task.Delay(ReconnectInterval, time, cancellationToken);
            if (!clientConnected)
            {
                scope.Guard(ReconnectNow);
            }
        }
    }

    private void ClientStateChanged(RpcClientState s)
    {
        switch (s)
        {
            case RpcClientState.Connecting:
                break; // reported by ReconnectNow already
            case RpcClientState.Connected:
                clientConnected = true;
                generation++;
                loggedRefusals.Clear();
                // A connection ends a mismatch kept up through the attempts at
                // once, as GTK drops it at Connected: "Connecting…" until
                // system.info answers.
                if (ShowsMismatch)
                {
                    Report(new ConnectionState.Connecting());
                }
                CheckSystemInfo();
                break;
            case RpcClientState.Disconnected d:
                // A dial that never got through is reported by the attempt
                // itself; here only a connection that was up and dropped.
                if (!clientConnected)
                {
                    return;
                }
                clientConnected = false;
                generation++;
                Report(new ConnectionState.Unavailable(d.Reason ?? ClientError.Disconnected.ToString()));
                break;
        }
    }

    private void CheckSystemInfo()
    {
        var gen = generation;
        scope.Perform(Client, API.SystemInfo, new EmptyParams(), outcome =>
        {
            ConnectionState result;
            if (outcome.TryGetValue(out var info, out var error))
            {
                // The handshake compared the version already; an answer that
                // disagrees with it is still a mismatch, as a defence.
                result = info.ProtocolVersion == API.ProtocolVersion
                    ? new ConnectionState.Connected(info)
                    : new ConnectionState.ProtocolMismatch(info.ProtocolVersion);
            }
            else
            {
                result = new ConnectionState.InfoFailed(Describe(error!));
            }
            if (generation != gen)
            {
                return;
            }
            if (result is ConnectionState.InfoFailed failed)
            {
                LogInfoFailed(logger, failed.Reason);
            }
            Report(result);
        }, InfoTimeout);
    }

    private void Report(ConnectionState s)
    {
        if (stopping)
        {
            return;
        }
        SetState(s);
    }

    private void SetState(ConnectionState s)
    {
        if (s == State)
        {
            return;
        }
        State = s;
        scope.Raise(StateChanged, this, s);
    }

    /// <summary>
    /// Notifies the bindings of a change; a handler that throws is reported
    /// and does not leave the state change half done (<see cref="ControllerEvents"/>).
    /// </summary>
    protected override void OnPropertyChanged(PropertyChangedEventArgs e) => scope.Guard(() => base.OnPropertyChanged(e));

    /// <summary>
    /// Notifies the bindings of a change to come; a handler that throws is
    /// reported and does not keep the change from being made.
    /// </summary>
    protected override void OnPropertyChanging(PropertyChangingEventArgs e) => scope.Guard(() => base.OnPropertyChanging(e));

    /// <summary>A short description of a connect-time error for the state (Swift <c>describe</c>).</summary>
    private static string Describe(Exception error) => error switch
    {
        OperationCanceledException => "cancelled",
        _ => error.Message,
    };

    [LoggerMessage(Level = LogLevel.Debug, Message = "backend not started: {Reason}")]
    private static partial void LogNotStarted(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Debug, Message = "backend unavailable: {Reason}")]
    private static partial void LogUnavailable(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "backend refused: {Reason}")]
    private static partial void LogRefused(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Debug, Message = "backend refused: {Reason}")]
    private static partial void LogRefusedAgain(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "system.info: {Reason}")]
    private static partial void LogInfoFailed(ILogger logger, string reason);
}

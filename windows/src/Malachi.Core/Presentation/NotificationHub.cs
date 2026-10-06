// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (NotificationHub,
// HandlerList); GTK: ui/internal/window/notify.go (handleNotification) and
// window.go (showConnectionState, the part that hands the state on).
//
// A presentation class that macOS keeps in AppKit (docs/windows-port.md
// §7.4): here it lives in Core with tests. Swift's removable Token is an
// IDisposable; its handler lists are copied before they are called, so a
// handler may remove itself (or another one) while being called and every
// handler of that round is still called, as in Swift. A Swift callback
// cannot throw; a C# handler can, and one that does is reported through
// the hub's PendingWork (logged at error level) while the others are still
// called (docs/windows-port.md §7.5). os.Logger's lines are ILogger's, at
// the same levels: a payload that does not decode is a warning (its method
// and the decoder's message, never the payload), an unknown method is
// information.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>
/// Decodes the daemon's notifications and the connection state and fans
/// them out to every interested part of the app (window/notify.go
/// <c>handleNotification</c> and window.go <c>showConnectionState</c>): the
/// status line, the banners, the list and the desktop notifications all
/// register here. UI-thread-affine: <see cref="ConnectionController"/>
/// raises its events on the UI thread, and so the handlers run there.
/// </summary>
public sealed partial class NotificationHub : IDisposable
{
    private readonly ILogger logger;
    private readonly HandlerList<NewMessageNotification> newMessage;
    private readonly HandlerList<SyncState> syncState;
    private readonly HandlerList<AuthRequiredNotification> authRequired;
    private readonly HandlerList<AccountsChangedNotification> accountsChanged;
    private readonly HandlerList<MessagesChangedNotification> messagesChanged;
    private readonly HandlerList<BoardChangedNotification> boardChanged;
    private readonly HandlerList<ConnectionState> connection;
    private ConnectionController? attached;

    /// <summary>A hub that is attached to nothing yet.</summary>
    /// <param name="logger">Receives methods and decoder messages, never a payload.</param>
    /// <param name="pending">Where a handler's failure is reported; a tracker of its own when null.</param>
    public NotificationHub(ILogger<NotificationHub>? logger = null, PendingWork? pending = null)
    {
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        Pending = pending ?? new PendingWork(this.logger);
        newMessage = new HandlerList<NewMessageNotification>(Pending);
        syncState = new HandlerList<SyncState>(Pending);
        authRequired = new HandlerList<AuthRequiredNotification>(Pending);
        accountsChanged = new HandlerList<AccountsChangedNotification>(Pending);
        messagesChanged = new HandlerList<MessagesChangedNotification>(Pending);
        boardChanged = new HandlerList<BoardChangedNotification>(Pending);
        connection = new HandlerList<ConnectionState>(Pending);
    }

    /// <summary>The last state the connection reported (Swift <c>connectionState</c>).</summary>
    public ConnectionState ConnectionState { get; private set; } = new ConnectionState.Connecting();

    /// <summary>Where a handler's failure is reported.</summary>
    public PendingWork Pending { get; }

    /// <summary>
    /// Takes over the connection's <see cref="ConnectionController.NotificationReceived"/>
    /// and <see cref="ConnectionController.StateChanged"/> (Swift
    /// <c>attach(to:)</c>, which installs the hub as <c>onNotification</c>
    /// and <c>onState</c>). One connection at a time; attaching again moves
    /// the hub.
    /// </summary>
    public void Attach(ConnectionController c)
    {
        ArgumentNullException.ThrowIfNull(c);
        Detach();
        attached = c;
        ConnectionState = c.State;
        c.NotificationReceived += OnNotification;
        c.StateChanged += OnState;
    }

    /// <summary>Fires on every <c>notify.newMessage</c>.</summary>
    public IDisposable AddNewMessage(Action<NewMessageNotification> handler) => newMessage.Add(handler);

    /// <summary>Fires on every <c>notify.syncState</c>.</summary>
    public IDisposable AddSyncState(Action<SyncState> handler) => syncState.Add(handler);

    /// <summary>Fires on every <c>notify.authRequired</c>.</summary>
    public IDisposable AddAuthRequired(Action<AuthRequiredNotification> handler) => authRequired.Add(handler);

    /// <summary>Fires on every <c>notify.accountsChanged</c>.</summary>
    public IDisposable AddAccountsChanged(Action handler)
    {
        ArgumentNullException.ThrowIfNull(handler);
        return accountsChanged.Add(_ => handler());
    }

    /// <summary>Fires on every <c>notify.messagesChanged</c>.</summary>
    public IDisposable AddMessagesChanged(Action<MessagesChangedNotification> handler) => messagesChanged.Add(handler);

    /// <summary>Fires on every <c>notify.boardChanged</c> (Swift <c>addBoardChanged</c>; the board's source lists again).</summary>
    public IDisposable AddBoardChanged(Action<BoardChangedNotification> handler) => boardChanged.Add(handler);

    /// <summary>
    /// Fires on every connection state change, after
    /// <see cref="ConnectionState"/> was updated. A handler added later does
    /// not get the current state; read <see cref="ConnectionState"/> for that.
    /// </summary>
    public IDisposable AddConnectionState(Action<ConnectionState> handler) => connection.Add(handler);

    /// <summary>
    /// Decodes <paramref name="raw"/> and hands it to the handlers of its
    /// kind (Swift <c>handle</c>). A payload that does not decode is logged
    /// and dropped, an unknown method logged.
    /// </summary>
    public void Handle(RpcNotification raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        DaemonNotification n;
        try
        {
            n = raw.Decode();
        }
        catch (JsonException e)
        {
            LogBadPayload(logger, raw.Method, e.Message);
            return;
        }
        switch (n)
        {
            case DaemonNotification.NewMessage m:
                newMessage.Fire(m.Payload);
                break;
            case DaemonNotification.SyncState s:
                syncState.Fire(s.State);
                break;
            case DaemonNotification.AuthRequired a:
                authRequired.Fire(a.Payload);
                break;
            case DaemonNotification.AccountsChanged:
                accountsChanged.Fire(new AccountsChangedNotification());
                break;
            case DaemonNotification.MessagesChanged c:
                messagesChanged.Fire(c.Payload);
                break;
            case DaemonNotification.BoardChanged b:
                boardChanged.Fire(b.Payload);
                break;
            case DaemonNotification.Unknown u:
                LogUnknown(logger, u.Method);
                break;
        }
    }

    /// <summary>
    /// Records <paramref name="state"/> and hands it to the connection's
    /// handlers (Swift's <c>onState</c> closure).
    /// </summary>
    public void HandleState(ConnectionState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        ConnectionState = state;
        connection.Fire(state);
    }

    /// <summary>Lets go of the connection; the handlers stay registered.</summary>
    public void Dispose() => Detach();

    private void Detach()
    {
        if (attached is { } c)
        {
            c.NotificationReceived -= OnNotification;
            c.StateChanged -= OnState;
            attached = null;
        }
    }

    private void OnNotification(object? sender, RpcNotification raw) => Handle(raw);

    private void OnState(object? sender, ConnectionState state) => HandleState(state);

    [LoggerMessage(Level = LogLevel.Warning, Message = "bad {Method} payload: {Reason}")]
    private static partial void LogBadPayload(ILogger logger, string method, string reason);

    [LoggerMessage(Level = LogLevel.Information, Message = "notification {Method}")]
    private static partial void LogUnknown(ILogger logger, string method);

    /// <summary>
    /// An ordered handler table with removable entries (Swift
    /// <c>HandlerList</c>); a handler may remove itself while being called.
    /// </summary>
    private sealed class HandlerList<T>(PendingWork pending)
    {
        private readonly List<(int Id, Action<T> Handler)> handlers = [];
        private int nextId;

        public Token Add(Action<T> handler)
        {
            ArgumentNullException.ThrowIfNull(handler);
            var id = nextId++;
            handlers.Add((id, handler));
            return new Token(() => handlers.RemoveAll(h => h.Id == id));
        }

        [SuppressMessage("Design", "CA1031", Justification = "One view's handler must not keep the others from the notification.")]
        public void Fire(T value)
        {
            foreach (var (_, handler) in handlers.ToArray())
            {
                try
                {
                    handler(value);
                }
                catch (Exception e)
                {
                    pending.Report(e);
                }
            }
        }
    }

    /// <summary>Removes a handler once; disposing again does nothing (Swift <c>Token.cancel</c>).</summary>
    private sealed class Token(Action remove) : IDisposable
    {
        private Action? remove = remove;

        public void Dispose()
        {
            var r = remove;
            remove = null;
            r?.Invoke();
        }
    }
}

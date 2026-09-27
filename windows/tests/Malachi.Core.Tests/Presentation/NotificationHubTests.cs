// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// NotificationHub (Core/Presentation), the port of AppState.swift's
// NotificationHub, which macOS leaves untested in AppKit: the decoded
// notifications reach the handlers of their kind in registration order,
// the connection state is recorded before its handlers run, a handler may
// remove itself while being called (and the others of that round still
// run), a payload that does not decode and an unknown method reach no
// handler, and a handler that throws neither stops the others nor escapes.

using System;
using System.Collections.Generic;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class NotificationHubTests
{
    private static RpcNotification Raw(string method, string parameters) =>
        new(method, Encoding.UTF8.GetBytes($$$"""{"jsonrpc":"2.0","method":"{{{method}}}","params":{{{parameters}}}}"""));

    private static readonly RpcNotification SyncStateLine =
        Raw("notify.syncState", """{"state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}}""");

    [Fact]
    public void EachKindReachesItsHandlersInOrder()
    {
        using var hub = new NotificationHub();
        var log = new List<string>();
        hub.AddSyncState(s => log.Add("sync1 " + s.AccountId.Value));
        hub.AddSyncState(s => log.Add("sync2 " + s.Status.Value));
        hub.AddAuthRequired(a => log.Add("auth " + a.Reason.Name));
        hub.AddAccountsChanged(() => log.Add("accounts"));
        hub.AddNewMessage(n => log.Add("new " + n.FolderId.Value));

        hub.Handle(SyncStateLine);
        hub.Handle(Raw("notify.authRequired", """{"accountId":"acc_1","reason":1201,"message":"535"}"""));
        hub.Handle(Raw("notify.accountsChanged", "{}"));

        Assert.Equal(["sync1 acc_1", "sync2 idle", "auth authFailed", "accounts"], log);
        Assert.Empty(hub.Pending.TakeFaults());
    }

    [Fact]
    public void AccountsChangedWithoutParamsStillArrives()
    {
        using var hub = new NotificationHub();
        var seen = 0;
        hub.AddAccountsChanged(() => seen++);
        hub.Handle(new RpcNotification("notify.accountsChanged", Encoding.UTF8.GetBytes("""{"jsonrpc":"2.0","method":"notify.accountsChanged"}""")));
        Assert.Equal(1, seen);
    }

    [Fact]
    public void ABadPayloadAndAnUnknownMethodReachNoHandler()
    {
        using var hub = new NotificationHub();
        var seen = 0;
        hub.AddSyncState(_ => seen++);
        hub.AddNewMessage(_ => seen++);
        hub.Handle(Raw("notify.syncState", """{"state":"not an object"}"""));
        hub.Handle(Raw("notify.somethingNew", """{"x":1}"""));
        Assert.Equal(0, seen);
        Assert.Empty(hub.Pending.TakeFaults());
    }

    [Fact]
    public void TheConnectionStateIsRecordedBeforeItsHandlersRun()
    {
        using var hub = new NotificationHub();
        Assert.IsType<ConnectionState.Connecting>(hub.ConnectionState);
        ConnectionState? seenInside = null;
        hub.AddConnectionState(_ => seenInside = hub.ConnectionState);
        var unavailable = new ConnectionState.Unavailable("refused");
        hub.HandleState(unavailable);
        Assert.Equal(unavailable, hub.ConnectionState);
        Assert.Equal(unavailable, seenInside);
    }

    [Fact]
    public void AHandlerMayRemoveItselfWhileBeingCalled()
    {
        using var hub = new NotificationHub();
        var log = new List<string>();
        IDisposable? first = null;
        first = hub.AddSyncState(_ =>
        {
            log.Add("first");
            first!.Dispose();
        });
        hub.AddSyncState(_ => log.Add("second"));
        hub.Handle(SyncStateLine);
        hub.Handle(SyncStateLine);
        Assert.Equal(["first", "second", "second"], log);
    }

    [Fact]
    public void AHandlerRemovedByAnEarlierOneStillRunsInThatRound()
    {
        // Swift iterates over a copy of its handler array.
        using var hub = new NotificationHub();
        var log = new List<string>();
        IDisposable? second = null;
        hub.AddSyncState(_ =>
        {
            log.Add("first");
            second!.Dispose();
        });
        second = hub.AddSyncState(_ => log.Add("second"));
        hub.Handle(SyncStateLine);
        hub.Handle(SyncStateLine);
        Assert.Equal(["first", "second", "first"], log);
    }

    [Fact]
    public void DisposingATokenTwiceRemovesOnlyItsHandler()
    {
        using var hub = new NotificationHub();
        var log = new List<string>();
        var a = hub.AddAccountsChanged(() => log.Add("a"));
        hub.AddAccountsChanged(() => log.Add("b"));
        a.Dispose();
        a.Dispose();
        hub.Handle(Raw("notify.accountsChanged", "{}"));
        Assert.Equal(["b"], log);
    }

    [Fact]
    public void AThrowingHandlerIsReportedAndTheOthersRun()
    {
        using var hub = new NotificationHub();
        var log = new List<string>();
        hub.AddConnectionState(_ => throw new InvalidOperationException("view broke"));
        hub.AddConnectionState(_ => log.Add("after"));
        hub.HandleState(new ConnectionState.Stopping());
        Assert.Equal(["after"], log);
        var fault = Assert.Single(hub.Pending.TakeFaults());
        Assert.Equal("view broke", fault.Message);
    }

    [Fact]
    public async Task AttachedToAConnectionItHearsItsStatesAndNotifications()
    {
        await using var fake = new FakeDaemon();
        fake.On(API.SystemInfo.Name, _ => """{"version":"fake","protocolVersion":2,"pid":7,"storePath":"/tmp/s.db"}""");
        await fake.StartAsync();
        using var ui = new TestUIContext();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        var pending = new PendingWork();
        var time = new FakeTimeProvider();
        var log = new List<string>();
        var (connection, hub) = await ui.RunAsync(() =>
        {
            var c = new ConnectionController(client, supervisor: null, timeProvider: time, pending: pending);
            var h = new NotificationHub(pending: pending);
            h.HandleState(new ConnectionState.Stopping());
            h.Attach(c);
            // The connection's state from the start, not what the hub had.
            log.Add(h.ConnectionState is ConnectionState.Connecting ? "attached connecting" : "attached other");
            h.AddConnectionState(s => log.Add("state " + s.GetType().Name));
            h.AddSyncState(s => log.Add("sync " + s.AccountId.Value));
            c.Start();
            return (c, h);
        });
        await Quiescence.IdleAsync(ui, pending, fake);
        await fake.PushNotificationAsync("notify.syncState", """{"state":{"accountId":"acc_9","status":"idle","progress":-1,"pendingOutbox":0}}""");
        await Quiescence.IdleAsync(ui, pending, fake, () => log.Count >= 4);
        Assert.Equal(["attached connecting", "state Connecting", "state Connected", "sync acc_9"], log);
        Assert.IsType<ConnectionState.Connected>(await ui.RunAsync(() => hub.ConnectionState));

        // Detached, it hears nothing more.
        await ui.RunAsync(() =>
        {
            hub.Dispose();
            return 0;
        });
        await fake.PushNotificationAsync("notify.syncState", """{"state":{"accountId":"acc_9","status":"idle","progress":-1,"pendingOutbox":0}}""");
        await ui.InvokeAsync(connection.StopAsync);
        await Quiescence.IdleAsync(ui, pending, fake);
        Assert.Equal(4, log.Count);
        await ui.RunAsync(() =>
        {
            connection.Dispose();
            return 0;
        });
    }
}

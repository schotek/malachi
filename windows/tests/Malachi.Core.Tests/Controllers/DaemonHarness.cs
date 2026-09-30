// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The setting of the Swift controller suites that talk to a fake daemon
// with a connected client and nothing else (JiraWizardControllerTests,
// JiraAccountControllerTests, IssueActionsControllerTests): the fake, the
// UI thread, the params each method was called with and the answers held
// back, all let go at the end. Swift's makeFake, RPCClient.connect, json
// and throw RPCError(...).

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

/// <summary>A fake daemon with a UI thread, for controllers that only call it.</summary>
internal sealed class DaemonHarness : IAsyncDisposable
{
    private readonly List<HeldAnswer> holds = [];
    private readonly List<RpcClient> clients = [];
    private readonly List<Action> closers = [];

    private DaemonHarness()
    {
    }

    public TestUIContext Ui { get; } = new();

    public PendingWork Pending { get; } = new();

    public FakeDaemon Fake { get; } = new();

    public ParamsLog Params { get; } = new();

    /// <summary>A started fake daemon that knows no method yet.</summary>
    public static async Task<DaemonHarness> StartAsync()
    {
        var h = new DaemonHarness();
        await h.Fake.StartAsync();
        return h;
    }

    /// <summary>The daemon's refusal (Swift's <c>RPCError(code:message:)</c>).</summary>
    public static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    /// <summary>A handler that fails as the daemon does (Swift's <c>throw RPCError(...)</c>).</summary>
    public static Func<string, string> Fails(int code, string message) => _ => throw Daemon(code, message);

    /// <summary>An answer the test lets go of later; released at the end at the latest.</summary>
    public HeldAnswer Hold()
    {
        var held = new HeldAnswer();
        holds.Add(held);
        return held;
    }

    /// <summary>Answers <paramref name="method"/>, recording its params first.</summary>
    public void On(string method, Func<string, string> answer) =>
        Fake.On(method, (FakeDaemon.MethodHandler)(p =>
        {
            Params.Record(method, p);
            return Task.FromResult(answer(p));
        }));

    /// <summary>Answers <paramref name="method"/> asynchronously, recording its params first.</summary>
    public void On(string method, Func<string, Task<string>> answer) =>
        Fake.On(method, (FakeDaemon.MethodHandler)(p =>
        {
            Params.Record(method, p);
            return answer(p);
        }));

    /// <summary>A client connected to the fake, disposed at the end.</summary>
    public async Task<RpcClient> ConnectAsync()
    {
        var client = new RpcClient(Fake.Path, PortableKeyFilePolicy.Instance);
        clients.Add(client);
        await client.ConnectAsync(TestContext.Current.CancellationToken);
        return client;
    }

    /// <summary>Runs <paramref name="close"/> on the UI thread at the end, before the answers are let go.</summary>
    public void CloseAtEnd(Action close) => closers.Add(close);

    /// <summary>Waits until nothing is left to happen.</summary>
    public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

    public async ValueTask DisposeAsync()
    {
        await Ui.RunAsync(() =>
        {
            foreach (var close in closers)
            {
                close();
            }
        });
        foreach (var held in holds)
        {
            held.Release();
        }
        try
        {
            await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            await Fake.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
        }
        catch (TimeoutException)
        {
            // Torn down all the same; the test's own asserts said what went wrong.
        }
        foreach (var client in clients)
        {
            client.Dispose();
        }
        await Fake.DisposeAsync();
        Ui.Dispose();
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ContactsControllerTests.swift
// fake daemon and timer: FakeContactsDaemon, FakeTimers, options, settle,
// fire, ctlAccounts, ctlBooks, people, serveList, standard); GTK:
// ui/internal/contactsctl fake_test.go.
//
// The contacts controllers against a fake daemon whose answers come back on
// the UI thread (SettleAsync waits for every call and question to end, as
// the Go tests' loop.run) and a timer the test fires itself (loop.fire).
// Params and results go through the contract's JSON, as over the socket.
// Fictional people only.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Controllers;
/// <summary>The fake daemon of the contacts controllers: a handler per method over the JSON of the params; every call is recorded.</summary>
internal sealed class FakeContactsDaemon : IContactsCaller
{
    private readonly Lock gate = new();
    private readonly Dictionary<string, Func<JsonElement, object>> handlers = new(StringComparer.Ordinal);
    private readonly List<(string Method, JsonElement Params)> calls = [];

    public void On(string method, Func<JsonElement, object> handler)
    {
        lock (gate)
        {
            handlers[method] = handler;
        }
    }

    public void Answer(string method, object value) => On(method, _ => value);

    public void Fail(string method, RpcError e) => On(method, _ => throw new RpcException(e));

    public int Count(string method)
    {
        lock (gate)
        {
            return calls.Count(c => c.Method == method);
        }
    }

    public TParams Last<TParams, TResult>(RpcMethod<TParams, TResult> method)
    {
        JsonElement p;
        lock (gate)
        {
            p = calls.Last(c => c.Method == method.Name).Params;
        }
        return p.Deserialize(method.ParamsInfo)!;
    }

    public Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken)
    {
        var raw = JsonSerializer.SerializeToElement(parameters, method.ParamsInfo);
        Func<JsonElement, object>? h;
        lock (gate)
        {
            calls.Add((method.Name, raw));
            handlers.TryGetValue(method.Name, out h);
        }
        if (h is null)
        {
            return Task.FromException<TResult>(new RpcException(new RpcError { Code = ErrorCode.MethodNotFound, Message = method.Name }));
        }
        try
        {
            var v = h(raw);
            var bytes = JsonSerializer.SerializeToUtf8Bytes(v, ApiJsonContext.Wire.GetTypeInfo(v.GetType())!);
            return Task.FromResult(JsonSerializer.Deserialize(bytes, method.ResultInfo)!);
        }
        catch (RpcException e)
        {
            return Task.FromException<TResult>(e);
        }
    }
}

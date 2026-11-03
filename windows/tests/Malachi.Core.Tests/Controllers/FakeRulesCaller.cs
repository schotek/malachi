// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The fake daemon of the rules controllers: a handler per method over the
// JSON of the params, as FakeContactsDaemon is; every call is recorded.
// Params go through the contract's JSON, as over the socket.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;

namespace Malachi.Core.Tests.Controllers;

internal sealed class FakeRulesCaller : IRulesCaller
{
    private readonly Lock gate = new();
    private readonly Dictionary<string, Func<JsonElement, object>> handlers = new(StringComparer.Ordinal);
    private readonly List<(string Method, JsonElement Params)> calls = [];

    public static Rule Sample(
        string id, string name = "Rule", bool enabled = true, AccountId? account = null, params RuleAction[] actions) => new()
        {
            Id = id,
            Name = name,
            Enabled = enabled,
            AccountId = account,
            Match = RuleMatch.All,
            Conditions = [new RuleCondition { Field = RuleField.From, Op = RuleOperator.Contains, Value = "acme" }],
            Actions = actions.Length > 0 ? actions : [new RuleAction { Kind = RuleActionKind.MarkRead }],
            Outgoing = actions.Any(a => a.Kind.IsOutgoing),
        };

    public static RpcError Error(int code, object? data = null) => new()
    {
        Code = code,
        Message = "fake",
        Data = data is RuleInvalidData d ? JsonSerializer.SerializeToElement(d, ApiJsonContext.Wire.RuleInvalidData) : null,
    };

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
            return Task.FromResult((TResult)h(raw));
        }
#pragma warning disable CA1031 // A handler's failure is the call's.
        catch (Exception e)
#pragma warning restore CA1031
        {
            return Task.FromException<TResult>(e);
        }
    }
}

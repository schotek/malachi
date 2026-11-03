// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/RulesController.swift
// (extension RPCClient: RulesCaller); GTK: the client the rules controllers
// call.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>The connected daemon as the rules controllers' <see cref="IRulesCaller"/>.</summary>
/// <param name="client">The application's client.</param>
public sealed class RpcRulesCaller(RpcClient client) : IRulesCaller
{
    private readonly RpcClient client = client ?? throw new ArgumentNullException(nameof(client));

    /// <inheritdoc/>
    public Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken) =>
        client.CallAsync(method, parameters, timeout, cancellationToken);
}

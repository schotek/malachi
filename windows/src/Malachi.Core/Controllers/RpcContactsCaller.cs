// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsOptions.swift
// (extension RPCClient: ContactsCaller); GTK: the *client.Client that
// contactsctl.Options.Caller is.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;

namespace Malachi.Core.Controllers;

/// <summary>The connected daemon as the contacts controllers' <see cref="IContactsCaller"/>.</summary>
/// <param name="client">The application's client.</param>
public sealed class RpcContactsCaller(RpcClient client) : IContactsCaller
{
    private readonly RpcClient client = client ?? throw new ArgumentNullException(nameof(client));

    /// <inheritdoc/>
    public Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken) =>
        client.CallAsync(method, parameters, timeout, cancellationToken);
}

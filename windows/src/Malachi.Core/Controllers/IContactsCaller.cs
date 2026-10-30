// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsOptions.swift
// (ContactsCaller); GTK: ui/internal/contactsctl client.go (Options.Caller,
// board.Caller).
//
// The daemon as the contacts controllers call it: the connected RpcClient
// (RpcContactsCaller), the sample daemon of MALACHI_CONTACTS_SAMPLES=1
// (ContactsSampleCaller), or a fake in the tests.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>The daemon as the contacts controllers call it (Swift <c>ContactsCaller</c>).</summary>
public interface IContactsCaller
{
    /// <summary>
    /// One call of <paramref name="method"/> with <paramref name="timeout"/>.
    /// A daemon's refusal is an <see cref="Transport.RpcException"/>, as
    /// <see cref="Transport.RpcClient"/> throws it.
    /// </summary>
    Task<TResult> CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, CancellationToken cancellationToken);
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ProfileStore.swift; GTK:
// ui/internal/assistantpanel/profile_store.go (ProfileStore).
//
// The application's copy of the user's profile (docs/api.md §4.18):
// Profile is the profile with the names of its accounts for the prompts;
// Proposals and Limits are for Preferences → AI → About You and the panel's
// cards. One per application; it reads the profile when the connection
// comes (Refresh) and again on notify.assistantProfileChanged and
// notify.accountsChanged, and writes through SetAsync and ResolveAsync. A
// daemon without the profile (an older one) leaves it empty and Supported
// false; a transient failure keeps what was read before. Every read and
// write bumps a generation: an older answer that ends after a newer one is
// dropped.
//
// Windows: Swift's completion closures are the awaited RpcError (null after
// the daemon stored the change); a failure of the transport is an error of
// code unavailable. Create it, and call it, on the UI thread.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The application's copy of the user's profile (Swift <c>ProfileStore</c>).</summary>
public sealed partial class ProfileStore : IDisposable
{
    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private IReadOnlyList<Account> accounts = [];
    private int generation;

    /// <summary>A store over <paramref name="client"/>, on the calling (UI) thread; nothing is read yet.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="logger">Receives method names and error codes, never the profile.</param>
    /// <param name="pending">Counts the store's background work; one of its own when null.</param>
    public ProfileStore(RpcClient client, ILogger<ProfileStore>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>The profile, the proposals or <see cref="Supported"/> changed.</summary>
    public event EventHandler? Changed;

    /// <summary>A read has ended (with the profile or without it).</summary>
    public bool Loaded { get; private set; }

    /// <summary>The daemon has the profile; false until a read ended, and for an older daemon.</summary>
    public bool Supported { get; private set; }

    /// <summary>The profile as the daemon has it.</summary>
    public AssistantProfile Stored { get; private set; } = new();

    /// <summary>The pending proposals, oldest first.</summary>
    public IReadOnlyList<ProfileProposal> Proposals { get; private set; } = [];

    /// <summary>The daemon's limits.</summary>
    public ProfileLimits Limits { get; private set; } = new();

    /// <summary>The profile for the prompts, with the names of its accounts.</summary>
    public UserProfile Profile => UserProfile.From(Stored, accounts);

    /// <summary>A pending proposal by id; null when there is none.</summary>
    public ProfileProposal? Proposal(string id) => Proposals.FirstOrDefault(q => q.Id == id);

    /// <summary>Reads the profile and the accounts again.</summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        var my = ++generation;
        scope.Run(async _ => await RefreshAsync(my));
    }

    private async Task RefreshAsync(int my)
    {
        AssistantProfileGetResult? result = null;
        IReadOnlyList<Account>? list = null;
        Exception? error = null;
        try
        {
            result = await client.CallAsync(API.AssistantProfileGet, new EmptyParams(), scope.Lifetime);
            try
            {
                list = (await client.CallAsync(API.AccountList, new EmptyParams(), scope.Lifetime)).Accounts;
            }
            catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException)
            {
                LogFailure(API.AccountList.Name, e);
            }
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            error = e;
        }
        if (my != generation || scope.IsClosed)
        {
            return;
        }
        Loaded = true;
        if (result is not null)
        {
            Supported = true;
            Stored = result.Profile;
            Proposals = result.Proposals;
            Limits = result.Limits ?? new ProfileLimits();
            if (list is not null)
            {
                accounts = list;
            }
        }
        else if (Unsupported(error))
        {
            Supported = false;
            Stored = new AssistantProfile();
            Proposals = [];
            Limits = new ProfileLimits();
            accounts = [];
        }
        else
        {
            // A transient failure keeps what was read before.
            LogFailure(API.AssistantProfileGet.Name, error);
        }
        scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));
    }

    /// <summary>
    /// Writes the fields <paramref name="p"/> carries: null after the daemon
    /// stored them (the store holds the new profile then), else its error.
    /// </summary>
    public async Task<RpcError?> SetAsync(AssistantProfileSetParams p)
    {
        ArgumentNullException.ThrowIfNull(p);
        scope.VerifyAccess();
        var my = ++generation;
        try
        {
            var r = await client.CallAsync(API.AssistantProfileSet, p, CancellationToken.None);
            if (my == generation && !scope.IsClosed)
            {
                Stored = r.Profile;
                scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));
            }
            return null;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            LogFailure(API.AssistantProfileSet.Name, e);
            return ErrorOf(e);
        }
    }

    /// <summary>
    /// Accepts (with <paramref name="text"/>, the user's edit, or as
    /// proposed) or dismisses a proposal: null after the daemon did it, else
    /// its error.
    /// </summary>
    public async Task<RpcError?> ResolveAsync(string id, bool accept, string? text = null)
    {
        ArgumentNullException.ThrowIfNull(id);
        scope.VerifyAccess();
        var my = ++generation;
        try
        {
            var r = await client.CallAsync(
                API.AssistantProfileResolve, new AssistantProfileResolveParams { Id = id, Accept = accept, Text = text }, CancellationToken.None);
            if (my == generation && !scope.IsClosed)
            {
                Stored = r.Profile;
                Proposals = r.Proposals;
                scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));
            }
            return null;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException or TimeoutException)
        {
            LogFailure(API.AssistantProfileResolve.Name, e);
            return ErrorOf(e);
        }
    }

    /// <summary>Stops reporting; answers on their way are dropped.</summary>
    public void Dispose() => scope.Close();

    /// <summary>The error of a daemon without the profile.</summary>
    internal static bool Unsupported(Exception? e) =>
        e is RpcException { Error.Code.Value: ErrorCode.MethodNotFound or ErrorCode.NotImplemented };

    private static RpcError ErrorOf(Exception e) =>
        e is RpcException r ? r.Error : new RpcError { Code = ErrorCode.Unavailable };

    private void LogFailure(string method, Exception? e)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            LogCallFailed(logger, method, e is RpcException r ? r.Error.Code.Value : 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed (code {Code})")]
    private static partial void LogCallFailed(ILogger logger, string method, int code);
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The app of the account list's smoke tests (AccountsSmokeTests): started
// once on an empty data folder with four mail accounts, added the way a
// client adds them (account.add over the app's socket, the passwords
// through the fake keyring), the last two of them paused
// (account.setEnabled), for all of its tests, and quit when they are done.
// Their servers are a closed port of the loopback address: the accounts
// never sync, which their rows show as a status, and nothing leaves this
// machine. The fixture is also the other client of the tests: it reads the
// accounts from the daemon and reorders them over the same socket.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>One app with four mail accounts for <see cref="AccountsSmokeTests"/>.</summary>
public sealed class AccountsSmokeFixture : IAsyncLifetime
{
    /// <summary>The accounts' names in the order they are added, and whether each is enabled.</summary>
    public static readonly IReadOnlyList<(string Name, bool Enabled)> Accounts =
    [
        ("Alpha", true),
        ("Bravo", true),
        ("Charlie", false),
        ("Delta", false),
    ];

    /// <summary>The accounts' names, which are their rows' titles.</summary>
    public static readonly IReadOnlySet<string> Names = Accounts.Select(a => a.Name).ToHashSet();

    // The discard port: closed on Windows, so a connection is refused at once.
    private const int ClosedPort = 9;

    private AppSession? session;

    /// <summary>Why the tests cannot run here; null when the app runs.</summary>
    public string? SkipReason { get; private set; }

    /// <summary>The app.</summary>
    internal AppSession Session => session ?? throw new InvalidOperationException(SkipReason ?? "the app did not start");

    /// <summary>The accounts' ids by name.</summary>
    internal IReadOnlyDictionary<string, AccountId> Ids { get; private set; } = new Dictionary<string, AccountId>();

    /// <inheritdoc/>
    public async ValueTask InitializeAsync()
    {
        SkipReason = UiEnvironment.SkipReason;
        if (SkipReason is not null)
        {
            return;
        }
        try
        {
            session = AppSession.Start();
            session.WaitForDaemon();
            var ids = new Dictionary<string, AccountId>();
            await CallAsync(async (client, token) =>
            {
                foreach (var (name, enabled) in Accounts)
                {
                    var added = await client.CallAsync(API.AccountAdd, Add(name), token);
                    ids[name] = added.AccountId;
                    if (!enabled)
                    {
                        await client.CallAsync(API.AccountSetEnabled, new AccountSetEnabledParams { AccountId = added.AccountId, Enabled = false }, token);
                    }
                }
            });
            Ids = ids;
        }
        catch
        {
            await DisposeAsync();
            session = null;
            throw;
        }
    }

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        session?.Dispose();
        return ValueTask.CompletedTask;
    }

    /// <summary>The daemon's accounts (account.list), in its order.</summary>
    internal async Task<IReadOnlyList<Account>> ListAsync()
    {
        IReadOnlyList<Account> accounts = [];
        await CallAsync(async (client, token) => accounts = (await client.CallAsync(API.AccountList, new EmptyParams(), token)).Accounts);
        return accounts;
    }

    /// <summary>Reorders the accounts as another client does (account.reorder), by name.</summary>
    internal Task ReorderAsync(IEnumerable<string> names)
    {
        var ids = names.Select(n => Ids[n]).ToList();
        return CallAsync((client, token) => client.CallAsync(API.AccountReorder, new AccountReorderParams { AccountIds = ids }, token));
    }

    private async Task CallAsync(Func<RpcClient, CancellationToken, Task> calls)
    {
        using var client = new RpcClient(Session.Socket, PortableKeyFilePolicy.Instance);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(60));
        await client.ConnectAsync(timeout.Token);
        await calls(client, timeout.Token);
    }

    // A mail account of <name> whose servers refuse every connection.
    private static AccountAddParams Add(string name)
    {
        var user = name.ToLowerInvariant() + "@example.test";
        ServerConfig Endpoint() => new()
        {
            Host = "127.0.0.1",
            Port = ClosedPort,
            // Allowed on a loopback address only (internal/core/accounts.go validateServer).
            Security = new Security(Security.None),
            Username = user,
            AuthMethod = new AuthMethod(AuthMethod.Password),
        };
        return new AccountAddParams
        {
            Config = new AccountConfig
            {
                Name = name,
                Email = user,
                DisplayName = name,
                Imap = Endpoint(),
                Smtp = Endpoint(),
            },
            Credentials = new Credentials { Password = "secret" },
        };
    }
}

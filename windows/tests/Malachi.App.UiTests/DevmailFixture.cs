// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The opt-in suite's world (MALACHI_DEVMAIL): devmail on ports of its own,
// the app on an empty data folder, and the devmail account added the way a
// client adds one (account.add over the app's socket, the password through
// the fake keyring), so the tests start with the Inbox listed.

using System;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading;
using System.Threading.Tasks;
using System.Windows.Automation;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>devmail, the app and the account for <see cref="DevmailTests"/>.</summary>
public sealed class DevmailFixture : IAsyncLifetime
{
    private DevmailServer? server;
    private AppSession? session;

    /// <summary>Why the suite does not run; null when it does.</summary>
    public string? SkipReason { get; private set; }

    /// <summary>The server.</summary>
    internal DevmailServer Server => server ?? throw new InvalidOperationException(SkipReason);

    /// <summary>The app.</summary>
    internal AppSession Session => session ?? throw new InvalidOperationException(SkipReason);

    /// <inheritdoc/>
    public async ValueTask InitializeAsync()
    {
        SkipReason = UiEnvironment.SkipReason;
        if (SkipReason is null)
        {
            var exe = DevmailServer.Executable;
            if (exe is null)
            {
                SkipReason = $"opt-in: set {DevmailServer.FolderEnv} to a folder with devmail.exe to run the tests against a local mail server";
            }
            else if (!File.Exists(exe))
            {
                SkipReason = $"{DevmailServer.FolderEnv} names no devmail.exe: {exe}";
            }
        }
        if (SkipReason is not null)
        {
            return;
        }
        try
        {
            server = DevmailServer.Start(Path.Combine(RepositoryRoot, "backend", "testdata", "mime"));
            session = AppSession.Start();
            session.WaitForDaemon();
            await AddAccountAsync(session.Socket, server);
            // The Inbox, selected by the mailbox once the account is there, lists its messages.
            var list = Uia.Find(session.MainWindow, "MessageList", AppSession.StartTimeout);
            Uia.WaitFor(() => Uia.All(list, ControlType.ListItem).Count > 5, "the Inbox's messages", AppSession.StartTimeout);
        }
        catch
        {
            await DisposeAsync();
            session = null;
            server = null;
            throw;
        }
    }

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        session?.Dispose();
        server?.Dispose();
        return ValueTask.CompletedTask;
    }

    // The repository root baked in at build time.
    private static string RepositoryRoot =>
        typeof(DevmailFixture).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>().First(a => a.Key == "MalachiRoot").Value
        ?? throw new InvalidOperationException("no MalachiRoot");

    private static async Task AddAccountAsync(string socket, DevmailServer devmail)
    {
        ServerConfig Endpoint(int port) => new()
        {
            Host = "127.0.0.1",
            Port = port,
            // Allowed on a loopback address only (internal/core/accounts.go validateServer).
            Security = new Security(Security.None),
            Username = DevmailServer.User,
            AuthMethod = new AuthMethod(AuthMethod.Password),
        };
        using var client = new RpcClient(socket, PortableKeyFilePolicy.Instance);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(60));
        await client.ConnectAsync(timeout.Token);
        await client.CallAsync(API.AccountAdd, new AccountAddParams
        {
            Config = new AccountConfig
            {
                Name = "Devmail",
                Email = DevmailServer.User,
                DisplayName = "Test User",
                Imap = Endpoint(devmail.ImapPort),
                Smtp = Endpoint(devmail.SmtpPort),
            },
            Credentials = new Credentials { Password = DevmailServer.Password },
        }, timeout.Token);
    }
}

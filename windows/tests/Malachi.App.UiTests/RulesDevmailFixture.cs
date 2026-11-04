// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The world of the rules' context-menu tests (RulesDevmailTests, opt-in
// with MALACHI_DEVMAIL): devmail on ports of its own for real folders and
// messages, the app on an empty data folder with the invented rules
// (MALACHI_RULES_SAMPLES=1: the rule editor and Apply Rules… talk to
// RulesSampleCaller, so nothing a test does changes the server's mail), and
// the devmail account added as DevmailFixture adds it.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>devmail, the app with the invented rules and the account for <see cref="RulesDevmailTests"/>.</summary>
public sealed class RulesDevmailFixture : IAsyncLifetime
{
    private DevmailServer? server;
    private AppSession? session;

    /// <summary>Why the suite does not run; null when it does.</summary>
    public string? SkipReason { get; private set; }

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
            server = DevmailServer.Start(Path.Combine(DevmailFixture.RepositoryRoot, "backend", "testdata", "mime"));
            session = AppSession.Start(new Dictionary<string, string> { ["MALACHI_RULES_SAMPLES"] = "1" });
            session.WaitForDaemon();
            await DevmailFixture.AddAccountAsync(session.Socket, server);
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
}

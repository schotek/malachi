// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (assistant,
// claudeDesktop and their wiring) and AppDelegate.swift (the Assistant
// refreshed when the application becomes active, Claude Desktop's
// termination); GTK: ui/main.go (window.NewAssistant, once for the
// application). What the Assistant needs once for the application: the
// Claude Code the panel runs (ClaudeCodeLocator), what the Assistant menus
// know about the Claude apps (AssistantController: the link handlers are the
// shell's default handlers of claude and claude-cli, the bridge's
// registration its own status), and Claude Desktop around a change of
// "Register with Claude" (ClaudeDesktopController over ClaudeDesktopApp),
// whose reported statuses the Assistant takes at once. Every prompt of the
// assistant is the daemon's (docs/api.md §4.18 assistant.prompt): one
// DaemonPrompter over the application's connection builds them for the
// panel, the one-shot requests and Codex (Swift AppState prompter).
//
// The third provider, an AI provider account of the daemon (kind ai,
// docs/plans/ai-api.md §7.4; Swift AppState wireAssistantAPI,
// assistantFooter; GTK window/notify.go, window.go showConnectionState,
// main.go WaitAPISessions): the Assistant has the application's client and
// the bridge the daemon starts for a session over an account, the
// sessions of the daemon's assistant get notify.assistantEvent, the ai
// accounts are listed again on notify.accountsChanged, on
// notify.assistantUsageChanged (the account's state may have moved with the
// turn) and on every connection made, and the sessions end with the
// connection. A runtime generation the accounts bring (the chosen
// account's configuration or switch changed, the account listed or gone:
// AssistantController.ProviderChanged) is the application's
// InAppProviderChanged, as a change of the assistant-provider key is. At
// quit the panel's conversation ends after the triage and the suggested
// reply and the daemon gets every sessionStop while the connection stands
// (StopAssistantSessionsAsync, at most AssistantController.ApiSessionStopWait).
//
// Windows differences: macOS hears of Claude Desktop's termination from
// NSWorkspace; here, while a change is pending, the app waits for its
// processes to go (ClaudeDesktopApp.WaitForExitAsync) and then tells the
// controller.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.App.Assistants;
using Malachi.Core.ApiAssistants;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Claude;
using Malachi.Platform.Windows.Files;
using Malachi.Platform.Windows.Registration;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The application's Assistant.</summary>
public sealed partial class AppState
{
    private ClaudeDesktopApp? claudeDesktopApp;
    private CancellationTokenSource? claudeDesktopWatch;
    private Malachi.Core.Assistants.DaemonPrompter? prompter;
    private readonly List<IDisposable> assistantApiTokens = [];

    /// <summary>
    /// Builds the assistant's prompts over the application's connection (the
    /// daemon's assistant.prompt), for the panel, the one-shot requests and
    /// Codex.
    /// </summary>
    public Malachi.Core.Assistants.IAssistantPrompter Prompter => prompter ??= new Malachi.Core.Assistants.DaemonPrompter(Client);

    /// <summary>The user's Claude Code, which the assistant panel and the one-shot requests run.</summary>
    public ClaudeCodeLocator ClaudeCode { get; private set; } = null!;

    /// <summary>What the Assistant menus know about the Claude apps (AssistantController).</summary>
    public AssistantController Assistant { get; private set; } = null!;

    /// <summary>Claude Desktop around a change of its "Register with Claude" (Preferences → Integrations → Claude Desktop).</summary>
    public ClaudeDesktopController ClaudeDesktop { get; private set; } = null!;

    // On the UI thread, once the settings and the paths exist.
    private void InitializeAssistant()
    {
        ClaudeCode = new ClaudeCodeLocator(
            Settings,
            ProcessEnvironment.Copy(ProcessEnvironment.Current()),
            directory: Paths.AssistantDir,
            directories: new PrivateDirectory(),
            logger: Logs.CreateLogger<ClaudeCodeLocator>());
        Assistant = new AssistantController(
            Paths.McpBridge,
            Settings,
            scheme => ShellAssociations.DefaultProgId(scheme) is not null,
            locator: ClaudeCode,
            registrationLogger: Logs.CreateLogger<McpRegistrationController>(),
            client: Client,
            daemonBridge: Paths.McpBridge,
            logger: Logs.CreateLogger<AssistantController>(),
            apiLogger: Logs.CreateLogger<DaemonAssistantProvider>());
        var app = new ClaudeDesktopApp(logger: Logs.CreateLogger<ClaudeDesktopApp>());
        claudeDesktopApp = app;
        ClaudeDesktop = new ClaudeDesktopController(
            Paths.McpBridge,
            new ClaudeDesktopPlatform(app.IsRunning, app.QuitAsync, () => app.Launch()),
            logger: Logs.CreateLogger<ClaudeDesktopController>());
        ClaudeDesktop.StatusReported += (_, s) => Assistant.Apply(s);
        ClaudeDesktop.Changed += (_, _) => WatchClaudeDesktop();
        Assistant.Refresh();
        InitializeChatGpt();
        WireAssistantApi();
    }

    /// <summary>
    /// The assistant panel's footer: where the mail it is asked about goes
    /// (ChatGPT's, an ai account's destination, else Claude's; Swift
    /// AppState.assistantFooter, GTK assistant_panel.go syncShown).
    /// </summary>
    public string AssistantFooter => Assistant.Source switch
    {
        AssistantProviderSource.ChatGpt => ChatGptText.Footer,
        AssistantProviderSource.Account when Assistant.ApiAccount is { } acc => AssistantController.ApiFooter(acc),
        _ => Core.Assistants.Assistant.PanelTexts().Footer,
    };

    /// <summary>
    /// The provider the compose window's assistant bar names (GTK
    /// Assistant.ProviderBrand): ChatGPT's or Claude's brand, or the ai
    /// account's name (<see cref="AssistantController.ApiBrand"/>).
    /// </summary>
    public string AssistantBrand => Assistant.Source switch
    {
        AssistantProviderSource.ChatGpt => Core.Assistants.Assistant.ChatGptBrand,
        AssistantProviderSource.Account => Assistant.ApiBrand,
        _ => Core.Assistants.Assistant.ClaudeBrand,
    };

    /// <summary>
    /// Opens what mends the chosen ai account (the panel's and the board's
    /// Set Up in Integrations…): its dialog over <paramref name="window"/>,
    /// on Replace Key… when the provider refused its key, else Preferences →
    /// Integrations (the account not listed yet, or removed).
    /// </summary>
    public void OpenAssistantAccount(Microsoft.UI.Xaml.Window? window)
    {
        if (Assistant.ApiAccount is { } acc && Hooks.EditAiAccount is { } edit)
        {
            edit(window, acc, acc.State.Status == Core.Api.SyncStatus.AuthRequired);
            return;
        }
        Hooks.OpenIntegrations?.Invoke(window);
    }

    // The ai accounts of the assistant (AssistantController.Api.cs; Swift
    // wireAssistantAPI): the daemon's events reach the sessions, the
    // accounts are listed again on the daemon's word and every connection
    // made, the sessions end with the connection; a runtime generation of
    // the accounts is the application's InAppProviderChanged.
    private void WireAssistantApi()
    {
        var assistant = Assistant;
        assistantApiTokens.Add(Notifications.AddAssistantEvent(n => assistant.ApiSessions.Handle(n)));
        assistantApiTokens.Add(Notifications.AddAssistantUsageChanged(_ => assistant.AccountsChanged()));
        assistantApiTokens.Add(Notifications.AddAccountsChanged(assistant.AccountsChanged));
        assistantApiTokens.Add(Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected or ConnectionState.InfoFailed)
            {
                assistant.AccountsChanged();
            }
            else
            {
                assistant.ApiDisconnected();
            }
        }));
        if (Notifications.ConnectionState is ConnectionState.Connected or ConnectionState.InfoFailed)
        {
            assistant.AccountsChanged();
        }
        assistant.ProviderChanged += OnAssistantProviderChanged;
    }

    // The chosen account's runtime changed: the provider over it was built
    // anew, so whatever holds the in-app provider takes it again and a run
    // under way over the old one ends (the triage, Suggest Reply, the panel,
    // the compose windows and the search follow InAppProviderChanged).
    private void OnAssistantProviderChanged(object? sender, EventArgs e)
    {
        if (chatGptClosed)
        {
            return;
        }
        InAppProviderChanged?.Invoke(this, EventArgs.Empty);
    }

    /// <summary>
    /// Quitting, after the triage and the suggested reply ended and before
    /// the connection closes: the panel's conversation ends, then the daemon
    /// gets the sessionStop of every session of its assistant ended so far
    /// (the panel's, the requests', the board's; ui/main.go WaitAPISessions),
    /// waited for at most <see cref="AssistantController.ApiSessionStopWait"/>.
    /// </summary>
    public async Task StopAssistantSessionsAsync()
    {
        MainWindow?.CloseAssistantConversation();
        await Assistant.WaitApiSessionsAsync();
    }

    /// <summary>
    /// A one-shot request on the user's Claude Code in the panel's private
    /// directory (GTK Assistant.NewRequest); the caller sets its Consent (the
    /// question on its own window) and closes it with its window.
    /// </summary>
    public AssistantRequest NewAssistantRequest() => new(
        Settings,
        ClaudeCode,
        Paths.AssistantDir,
        ProcessEnvironment.Copy(ProcessEnvironment.Current()),
        directories: new PrivateDirectory(),
        logger: Logs.CreateLogger<AssistantRequest>(),
        processLogger: Logs.CreateLogger<ClaudeCodeProcess>())
    { Provider = InAppProvider, Prompter = Prompter };

    // While a change is pending, the controller hears when Claude Desktop
    // has quit (macOS: NSWorkspace's termination notification).
    private void WatchClaudeDesktop()
    {
        if (ClaudeDesktop.Pending is null || ClaudeDesktop.IsClosed)
        {
            claudeDesktopWatch?.Cancel();
            claudeDesktopWatch = null;
            return;
        }
        if (claudeDesktopWatch is not null || claudeDesktopApp is not { } app)
        {
            return;
        }
        var watch = new CancellationTokenSource();
        claudeDesktopWatch = watch;
        _ = WaitAsync();

        async Task WaitAsync()
        {
            try
            {
                await app.WaitForExitAsync(watch.Token);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            // Back on the UI thread.
            if (ReferenceEquals(claudeDesktopWatch, watch))
            {
                claudeDesktopWatch = null;
                ClaudeDesktop.Terminated();
            }
        }
    }

    // Quit: nothing more is asked or written; a running Claude Code ends with
    // the panel's controller, and its sign-in, which nobody waits for any
    // more, here.
    private void CloseAssistant()
    {
        foreach (var t in assistantApiTokens)
        {
            t.Dispose();
        }
        assistantApiTokens.Clear();
        if (Assistant is { } assistant)
        {
            assistant.ProviderChanged -= OnAssistantProviderChanged;
        }
        CloseChatGpt();
        ClaudeCode?.CancelSignIn();
        claudeDesktopWatch?.Cancel();
        claudeDesktopWatch = null;
        ClaudeDesktop?.Close();
        Assistant?.Close();
    }
}

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
// whose reported statuses the Assistant takes at once.
//
// Windows differences: macOS hears of Claude Desktop's termination from
// NSWorkspace; here, while a change is pending, the app waits for its
// processes to go (ClaudeDesktopApp.WaitForExitAsync) and then tells the
// controller.

using System;
using System.Threading;
using System.Threading.Tasks;
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

    /// <summary>The user's Claude Code, which the assistant panel and the one-shot requests run.</summary>
    public ClaudeCodeLocator ClaudeCode { get; private set; } = null!;

    /// <summary>What the Assistant menus know about the Claude apps (AssistantController).</summary>
    public AssistantController Assistant { get; private set; } = null!;

    /// <summary>Claude Desktop around a change of "Register with Claude".</summary>
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
            registrationLogger: Logs.CreateLogger<McpRegistrationController>());
        var app = new ClaudeDesktopApp(logger: Logs.CreateLogger<ClaudeDesktopApp>());
        claudeDesktopApp = app;
        ClaudeDesktop = new ClaudeDesktopController(
            Paths.McpBridge,
            new ClaudeDesktopPlatform(app.IsRunning, app.QuitAsync, () => app.Launch()),
            logger: Logs.CreateLogger<ClaudeDesktopController>());
        ClaudeDesktop.StatusReported += (_, s) => Assistant.Apply(s);
        ClaudeDesktop.Changed += (_, _) => WatchClaudeDesktop();
        Assistant.Refresh();
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
        processLogger: Logs.CreateLogger<ClaudeCodeProcess>());

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
        ClaudeCode?.CancelSignIn();
        claudeDesktopWatch?.Cancel();
        claudeDesktopWatch = null;
        ClaudeDesktop?.Close();
        Assistant?.Close();
    }
}

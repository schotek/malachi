// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppDelegate.swift
// (applicationDidFinishLaunching, applicationShouldTerminate,
// applicationShouldTerminateAfterLastWindowClosed,
// applicationShouldHandleReopen, application(_:open:), showMainWindow);
// GTK: ui/main.go (startup, activate, open, shutdown) and window.go's
// close-request. The application's life on Windows (docs/windows-port.md
// §10):
//
// - DispatcherShutdownMode.OnExplicitShutdown: closing a window never ends
//   the process by itself; Quit does (Application.Exit).
// - One main window for the process, made at start and shown unless the
//   activation says otherwise (a mailto: launch shows only the composer,
//   GTK's open signal, U4; --background shows nothing). Closing it hides
//   it; with Run in Background off, closing the last window quits (the
//   GApplication rule, WindowLifetime).
// - Every activation, the first and each second launch's redirect, goes
//   through Activate on the UI thread.
// - Quit is QuitSequence: the dirty drafts saved and asked about, then the
//   point of no return (the windows hide, the supervisor starts no daemon,
//   the platform services stop, the open directory is emptied), the
//   connection closed and the daemon this app started stopped (never one it
//   adopted), the open directory emptied once more, the settings let go,
//   the WebView2 crash dumps removed, Application.Exit. Ctrl+C in the
//   terminal is Quit; the terminal closing, and the session ending
//   (WM_ENDSESSION), stop the daemon without saving or asking.

using System;
using System.Threading.Tasks;
using Malachi.App.Platform;
using Malachi.App.Shell;
using Malachi.Core.Compose;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Consoles;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>The WinUI application.</summary>
public partial class App : Application
{
    private readonly AppLog log;
    private readonly ConsoleAttachment? console;
    private readonly Core.Daemon.Paths paths;
    private readonly ActivationRequest initial;
    private readonly ILogger logger;
    private AppState? state;
    private QuitSequence? quit;
    private QuitReason? pendingQuit;

    /// <summary>The application of the process's first activation <paramref name="initial"/>.</summary>
    public App(ActivationRequest initial, AppLog log, ConsoleAttachment? console, Core.Daemon.Paths paths)
    {
        ArgumentNullException.ThrowIfNull(initial);
        ArgumentNullException.ThrowIfNull(log);
        ArgumentNullException.ThrowIfNull(paths);
        this.initial = initial;
        this.log = log;
        this.console = console;
        this.paths = paths;
        logger = log.CreateLogger<App>();
        InitializeComponent();
        // Closing a window never ends the app by itself (the main window
        // hides; a background app has none): Quit does.
        DispatcherShutdownMode = DispatcherShutdownMode.OnExplicitShutdown;
        UnhandledException += (_, e) => LogUnhandled(logger, e.Exception);
    }

    /// <summary>The application's objects, once launched.</summary>
    internal AppState? State => state;

    /// <summary>
    /// Handles one activation on the UI thread (ui/main.go activate and
    /// open, AppDelegate's reopen and open): compose windows for its
    /// mailto: URIs, then the main window when it asks for it.
    /// </summary>
    internal void Activate(ActivationRequest request)
    {
        if (state is null || state.IsStopping)
        {
            // Past Quit's point of no return nothing opens or comes back:
            // the windows hid for good.
            return;
        }
        if (request.Ignored.Count > 0)
        {
            // The arguments may be addresses: their count only.
            LogIgnoredArguments(logger, request.Ignored.Count);
        }
        var shown = false;
        foreach (var uri in request.MailtoUris)
        {
            shown |= OpenMailto(uri);
        }
        if (request.ShowMainWindow || (request.MailtoUris.Count > 0 && !shown && !request.StartHidden))
        {
            // A mailto: nobody could open still brings the app up.
            state.ShowMainWindow();
        }
    }

    /// <summary>
    /// Quits for <paramref name="reason"/> (UI thread). Before the app is
    /// launched, the request waits for it.
    /// </summary>
    internal Task<bool> QuitAsync(QuitReason reason)
    {
        if (quit is null)
        {
            pendingQuit = pendingQuit == QuitReason.SessionEnd ? QuitReason.SessionEnd : reason;
            return Task.FromResult(false);
        }
        return quit.QuitAsync(reason);
    }

    /// <inheritdoc/>
    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        var s = AppState.Create(log, console, paths, initial.StartHidden);
        // The WebView2 environment is created now: it once took seconds, and the
        // reader shows the plain text until it is ready (docs/windows-port.md §6.1).
        global::Malachi.App.WebViews.WebViewEnvironment.LoggerFactory = log;
        global::Malachi.App.WebViews.WebViewEnvironment.Start(
            System.IO.Path.Combine(paths.DataDir, global::Malachi.App.WebViews.WebViewEnvironment.UserDataFolderName));
        state = s;
        global::Malachi.App.Resources.Icons.Logger = log.CreateLogger("Icons");
        var main = new MainWindow(s, SessionEnding);
        s.MainWindow = main;
        s.QuitHandler = QuitAsync;
        s.Windows.Quit = s.Quit;
        s.Windows.LastWindowClosed += (_, _) => _ = QuitAsync(QuitReason.User);
        main.CloseRequested += (_, _) => OnMainWindowCloseRequested(s, main);
        // The notification-area icon is there exactly while the app runs with its window hidden.
        main.ShownChanged += (_, visible) => PlatformServices.SetRunningInBackground(!visible);

        // The platform services subscribe to the notifications first, so that a new
        // message reaches the desktop notification before the list (notify.go's order).
        PlatformServices.Start(new PlatformContext
        {
            Settings = s.Settings,
            Notifications = s.Notifications,
            IsMainWindowActive = () => s.IsMainWindowActive,
            ShowMainWindow = s.ShowMainWindow,
            NewMessage = () => s.Hooks.ComposeNew?.Invoke(),
            CheckForNewMail = () => s.Hooks.CheckForNewMail?.Invoke(),
            Quit = s.Quit,
            LoggerFactory = log,
        });

        // The controllers plug into the shell before the connection reports anything.
        var integration = new Integration(s, main);
        s.Integration = integration;
        main.Attach(integration);
        // The compose windows (wave 2, E5): before the first activation, which may be a mailto:.
        global::Malachi.App.Compose.ComposeManager.Install(s, integration);
        // The Preferences window and the account wizard (wave 2, E6).
        Preferences.PreferencesEntryPoints.Install(s);

        quit = new QuitSequence(
            new QuitSteps
            {
                SaveDrafts = integration.Compose.SaveForQuitAsync,
                BeginStopping = () =>
                {
                    s.IsStopping = true;
                    s.Supervisor.BeginStopping();
                    main.SaveGeometry();
                    PlatformServices.Stop();
                    foreach (var w in s.Windows.Windows)
                    {
                        w.Window.AppWindow.Hide();
                    }
                    // Before the daemon's stop, which may take 15 s: a quit
                    // that never completes (the session ends meanwhile)
                    // leaves nothing behind (ui/main.go's shutdown order).
                    s.PurgeOpenDir();
                },
                StopDaemon = s.Connection.StopAsync,
                Release = () =>
                {
                    s.Dispose();
                    global::Malachi.App.WebViews.WebViewEnvironment.SweepCrashDumps();
                },
                Exit = () =>
                {
                    console?.ShutdownCompleted();
                    Exit();
                },
            },
            log.CreateLogger<QuitSequence>());

        Activate(initial);
        if (initial.StartHidden && !main.AppWindow.IsVisible)
        {
            LogStartedHidden(logger);
            // Never shown, the window raises no ShownChanged: the icon in the
            // notification area is the way back to it (launch at login).
            PlatformServices.SetRunningInBackground(true);
        }
        Program.AppLaunched(this);
        s.Connection.Start();
        if (pendingQuit is { } reason)
        {
            _ = quit.QuitAsync(reason);
        }
    }

    // window.go close-request: hidden with Run in Background (read each
    // time: it changes live), otherwise gone for the GApplication rule.
    private void OnMainWindowCloseRequested(AppState s, MainWindow main)
    {
        if (quit?.IsQuitting == true)
        {
            return;
        }
        main.AppWindow.Hide();
        if (s.Lifetime.MainWindowClosed(s.Settings.RunInBackground))
        {
            _ = QuitAsync(QuitReason.User);
        }
    }

    // WM_ENDSESSION: the process may be ended as soon as the message
    // returns, so the daemon this app started is stopped here, waiting
    // (docs/windows-port.md §5); the rest of the way out follows.
    private void SessionEnding()
    {
        if (state is not { } s)
        {
            return;
        }
        LogSessionEnding(logger);
        s.PurgeOpenDir();
        s.Supervisor.BeginStopping();
        s.Client.Close();
        try
        {
            s.Supervisor.StopAsync().Wait(s.Supervisor.StopTimeout + TimeSpan.FromSeconds(1));
        }
        catch (AggregateException e)
        {
            LogSessionStopFailed(logger, e.InnerException ?? e);
        }
        s.PurgeOpenDir();
        global::Malachi.App.WebViews.WebViewEnvironment.SweepCrashDumps();
        _ = QuitAsync(QuitReason.SessionEnd);
    }

    // mailto: to a compose window (ui/main.go open: the UI only splits the
    // URI; everything else about the message is the backend's). False when
    // no compose window opened.
    private bool OpenMailto(string uri)
    {
        ComposeParams p;
        try
        {
            p = Mailto.ParseMailto(uri);
        }
        catch (MailtoException e)
        {
            LogNotMailto(logger, e.Error.ToString());
            return false;
        }
        if (state?.Hooks.OpenCompose is not { } open)
        {
            LogNoComposer(logger);
            return false;
        }
        open(p);
        return true;
    }

    [LoggerMessage(Level = LogLevel.Critical, Message = "unhandled exception")]
    private static partial void LogUnhandled(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "ignoring {Count} arguments of an activation")]
    private static partial void LogIgnoredArguments(ILogger logger, int count);

    [LoggerMessage(Level = LogLevel.Information, Message = "started hidden in the background")]
    private static partial void LogStartedHidden(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "the session ends: stopping the daemon")]
    private static partial void LogSessionEnding(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "the daemon could not be stopped at the session's end")]
    private static partial void LogSessionStopFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "ignoring non-mailto URI: {Reason}")]
    private static partial void LogNotMailto(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "a mailto: link came before the compose windows exist")]
    private static partial void LogNoComposer(ILogger logger);
}

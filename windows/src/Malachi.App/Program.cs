// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The entry point, in place of the one the XAML compiler generates
// (DISABLE_XAML_GENERATED_MAIN); the counterpart of ui/main.go's main (the
// GApplication with its unique name) and of macos main.swift. What has to
// happen before the application starts, in this order
// (docs/windows-port.md §5 Console, §10):
//
// 1. The console: the standard handles a launcher passed stop being
//    inheritable, the app attaches to the terminal it was started from
//    (make run-windows) and takes its Ctrl+C (a graceful Quit) and its
//    closing (the daemon stops by itself; nothing restarts it). Before
//    anything touches System.Console.
// 2. The working directory leaves the app's folder for the user's profile
//    (Windows-only: a launch from Explorer, a shortcut or make run-windows
//    starts the app in its own folder): everything the app starts inherits
//    it, and a Claude Desktop, browser or viewer it started would keep the
//    folder from being removed or updated as long as it runs. The
//    MALACHI_* variables that name a path and were given relative keep
//    meaning what they meant where the app was started.
// 3. The log (the data directory's logs, and the terminal).
// 4. The notifications' hook (PlatformServices.InitializeEarly): the
//    handler before Register(), both before the single instance.
// 5. The single instance: AppInstance.FindOrRegisterForKey with the app
//    id. A second launch redirects its activation (its command line with
//    a mailto: URI or --background, a notification's click) to the first
//    and exits once the first has taken it, however long that takes, or
//    once the first has ended without taking it (exit code 1): a second
//    launch that gave up while the first was busy would make the first
//    abort when it got to the orphaned redirect (measured). The redirect
//    passes the right to come to the front on (APP-SPIKES.md §5.2). The
//    first instance takes redirected activations on a worker thread and
//    hands them to the UI thread.
// 6. The application, on a DispatcherQueueSynchronizationContext.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.App.Platform;
using Malachi.App.Shell;
using Malachi.Core;
using Malachi.Core.Daemon;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Consoles;
using Malachi.Platform.Windows.Files;
using Malachi.Platform.Windows.Notifications;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.System.Com;
using Windows.Win32.System.Threading;

namespace Malachi.App;

/// <summary>Starts the WinUI application, once per user session.</summary>
public static partial class Program
{
    private static readonly Lock Gate = new();
    private static readonly List<Action<App>> Waiting = [];
    private static App? app;
    private static ILogger logger = Microsoft.Extensions.Logging.Abstractions.NullLogger.Instance;

    // The MALACHI_* variables whose value is a path (DaemonSupervisor,
    // Paths, Catalogue); MALACHI_DAEMON may also be none.
    private static readonly string[] PathVariables =
        ["MALACHI_DATA_DIR", "MALACHI_SOCKET", "MALACHI_DAEMON", "MALACHI_KEYRING_HELPER", "MALACHI_LOCALE_DIR"];

    // Step 2: a relative path in a path variable becomes absolute against
    // the directory the app was started in, then the working directory is
    // the user's profile (the system folder when there is none).
    private static void LeaveLaunchDirectory()
    {
        foreach (var name in PathVariables)
        {
            var value = Environment.GetEnvironmentVariable(name);
            if (value is { Length: > 0 } && value.IndexOfAny(['\\', '/']) >= 0 && !System.IO.Path.IsPathFullyQualified(value))
            {
                Environment.SetEnvironmentVariable(name, System.IO.Path.GetFullPath(value));
            }
        }
        var profile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        try
        {
            Environment.CurrentDirectory = profile.Length > 0 ? profile : Environment.SystemDirectory;
        }
        catch (Exception e) when (e is System.IO.IOException or UnauthorizedAccessException or System.Security.SecurityException)
        {
            // Where the app was started, then: nothing else depends on it.
        }
    }

    [STAThread]
    private static int Main()
    {
        var console = ConsoleAttachment.Initialize(OnConsoleControl);
        LeaveLaunchDirectory();
        var paths = Paths.Resolve();
        string? logDirectory = paths.LogDir;
        try
        {
            paths.EnsureDirectories(new PrivateDirectory());
        }
        catch (Exception e) when (e is System.IO.IOException or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            // No log file: the terminal, if any, still gets the lines.
            logDirectory = null;
        }
        using var log = new AppLog(logDirectory, console.Terminal);
        logger = log.CreateLogger("Program");

        WinRT.ComWrappersSupport.InitializeComWrappers();
        PlatformServices.InitializeEarly(OnNotificationInvoked, log);

        var activation = AppInstance.GetCurrent().GetActivatedEventArgs();
        var request = RequestOf(activation, redirected: false);
        var key = AppInstance.FindOrRegisterForKey(AppIdentity.AppId);
        if (!key.IsCurrent)
        {
            LogRedirecting(logger, activation.Kind);
            var delivered = Redirect(key, activation);
            console.ShutdownCompleted();
            return delivered ? 0 : 1;
        }
        key.Activated += OnRedirected;

        Application.Start(callback =>
        {
            var context = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
            SynchronizationContext.SetSynchronizationContext(context);
            _ = new App(request, log, console, paths);
        });
        LogExited(logger);
        console.ShutdownCompleted();
        return 0;
    }

    /// <summary>The application has launched: what waited for it (redirects, a Ctrl+C) runs now, on the UI thread.</summary>
    internal static void AppLaunched(App launched)
    {
        List<Action<App>> run;
        lock (Gate)
        {
            app = launched;
            run = [.. Waiting];
            Waiting.Clear();
        }
        foreach (var action in run)
        {
            action(launched);
        }
    }

    // The request of an activation (AppInstance.GetActivatedEventArgs or a
    // redirect's Activated): an unpackaged app's launch carries its whole
    // command line.
    private static ActivationRequest RequestOf(AppActivationArguments activation, bool redirected)
    {
        switch (activation.Kind)
        {
            case ExtendedActivationKind.Launch:
                var line = (activation.Data as Windows.ApplicationModel.Activation.ILaunchActivatedEventArgs)?.Arguments;
                return ActivationRequest.FromCommandLine(ActivationKind.Launch, line, redirected);
            case ExtendedActivationKind.AppNotification:
                var clicked = PlatformServices.NotificationActivationFrom(activation);
                return ActivationRequest.FromNotification(clicked?.AccountId, clicked?.MessageId, redirected);
            case ExtendedActivationKind.Protocol:
                var uri = (activation.Data as Windows.ApplicationModel.Activation.IProtocolActivatedEventArgs)?.Uri?.AbsoluteUri;
                return ActivationRequest.FromArguments(ActivationKind.Protocol, uri is null ? [] : [uri], redirected);
            default:
                return ActivationRequest.FromArguments(ActivationKind.Other, [], redirected);
        }
    }

    // A second launch: hands the activation to the first instance, keeping
    // this STA thread pumping COM while the redirect runs (the documented
    // way: the redirect's own calls may need it). The wait has no timeout,
    // as in the Windows App SDK's own pattern, since giving up is worse:
    // a busy first instance (measured with a suspended one) takes the
    // activation once it is free, but aborts in the Windows App SDK when it
    // gets to a redirect whose launch has ended. It ends when the redirect
    // is done or failed, or when the first instance's process ends, which
    // leaves the redirect pending forever (measured). False when the
    // activation was not handed over: the redirect's event is then left to
    // the finaliser, since the redirect may still set it.
    private static unsafe bool Redirect(AppInstance key, AppActivationArguments activation)
    {
        var done = new ManualResetEvent(false);
        var failed = false;
        _ = Task.Run(() =>
        {
            try
            {
                key.RedirectActivationToAsync(activation).AsTask().Wait();
            }
            catch (AggregateException e)
            {
                failed = true;
                LogRedirectFailed(logger, e.InnerException ?? e);
            }
            finally
            {
                done.Set();
            }
        });
        using var first = PInvoke.OpenProcess_SafeHandle(PROCESS_ACCESS_RIGHTS.PROCESS_SYNCHRONIZE, false, key.ProcessId);
        Span<HANDLE> handles =
        [
            (HANDLE)done.SafeWaitHandle.DangerousGetHandle(),
            first.IsInvalid ? default : (HANDLE)first.DangerousGetHandle(),
        ];
        var waited = PInvoke.CoWaitForMultipleObjects(
            (uint)CWMO_FLAGS.CWMO_DEFAULT, PInvoke.INFINITE, first.IsInvalid ? handles[..1] : handles, out var index);
        if (waited.Failed || index != 0)
        {
            if (waited.Failed)
            {
                LogRedirectWaitFailed(logger, waited.Value);
            }
            else
            {
                LogFirstInstanceEnded(logger, key.ProcessId);
            }
            return false;
        }
        done.Dispose();
        return !Volatile.Read(ref failed);
    }

    // A second launch's activation, on a worker thread.
    private static void OnRedirected(object? sender, AppActivationArguments activation)
    {
        var request = RequestOf(activation, redirected: true);
        OnApp(a => a.Activate(request));
    }

    // A notification clicked while the app runs (PlatformServices), on any thread.
    // The argument is the notification's own (message, account): the
    // click opens the message in its own window, as GTK's app.open-message;
    // whether it named one only is logged.
    private static void OnNotificationInvoked(NotificationActivation activation)
    {
        LogNotificationInvoked(logger, activation.MessageId is not null);
        OnApp(a => a.Activate(ActivationRequest.FromNotification(activation.AccountId, activation.MessageId, redirected: true)));
    }

    // The terminal's control events, on a thread of the system (ConsoleAttachment).
    private static void OnConsoleControl(ConsoleControl control)
    {
        var sessionEnd = control is ConsoleControl.Close or ConsoleControl.Logoff or ConsoleControl.Shutdown;
        if (sessionEnd)
        {
            // The daemon got the event too and stops by itself: nothing may
            // restart it, from this moment.
            lock (Gate)
            {
                app?.State?.Supervisor.BeginStopping();
            }
        }
        LogConsoleControl(logger, control);
        OnApp(a => _ = a.QuitAsync(sessionEnd ? QuitReason.SessionEnd : QuitReason.User));
    }

    // Runs action on the UI thread of the application, now or once it has launched.
    private static void OnApp(Action<App> action)
    {
        App? target;
        lock (Gate)
        {
            target = app;
            if (target is null)
            {
                Waiting.Add(action);
                return;
            }
        }
        if (target.State?.Dispatcher is { } dispatcher)
        {
            dispatcher.TryEnqueue(() => action(target));
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "another instance runs: redirecting a {Kind} activation to it")]
    private static partial void LogRedirecting(ILogger logger, ExtendedActivationKind kind);

    [LoggerMessage(Level = LogLevel.Error, Message = "the activation could not be redirected")]
    private static partial void LogRedirectFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "the wait for the redirect failed (0x{Result:X8})")]
    private static partial void LogRedirectWaitFailed(ILogger logger, int result);

    [LoggerMessage(Level = LogLevel.Error, Message = "the first instance (pid {ProcessId}) ended before it took the activation")]
    private static partial void LogFirstInstanceEnded(ILogger logger, uint processId);

    [LoggerMessage(Level = LogLevel.Information, Message = "console {Control}: quitting")]
    private static partial void LogConsoleControl(ILogger logger, ConsoleControl control);

    [LoggerMessage(Level = LogLevel.Debug, Message = "a notification was clicked (for a message: {ForMessage})")]
    private static partial void LogNotificationInvoked(ILogger logger, bool forMessage);

    [LoggerMessage(Level = LogLevel.Information, Message = "exited")]
    private static partial void LogExited(ILogger logger);
}

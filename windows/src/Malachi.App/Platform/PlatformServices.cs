// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the entry points of the Windows platform services
// (docs/windows-port.md §10, "Notifications and sound", "Background, tray,
// launch at login", "mailto: and the default mail app"), what the macOS
// AppDelegate and AppState wire by hand (NotificationService,
// LoginItemService, the URL type) and GTK gets from the desktop (the
// notification server, the Background portal, the desktop file).
//
// The shell calls them in this order:
//   Main, first:        InitializeEarly(onActivated): the notification
//                       platform, before AppInstance.GetActivatedEventArgs
//                       and the single-instance redirect;
//   Main, cold start:   NotificationActivationFrom(GetActivatedEventArgs())
//                       tells a click on a notification from a launch (also
//                       for a redirected activation in AppInstance.Activated);
//   UI thread, started: Start(context), once the hub exists and before the
//                       mailbox adds its notify.newMessage handler (GTK
//                       shows the notification first, then updates the list);
//   UI thread:          SetRunningInBackground(true) when the main window is
//                       hidden in the background (closed with Run in
//                       Background, or never shown after --background), false
//                       when it is shown again; WithdrawNotifications(ids)
//                       when the mailbox finds notifications outdated;
//   on the way out:     Stop(), on the UI thread (Main's thread after
//                       Application.Start returned is the same one).
// LaunchAtLogin, Mailto and OpenDefaultApps are for Preferences.
// InitializeEarly and Stop never throw: notifications are optional, and
// neither the start nor the shutdown (which still stops the daemon) may
// depend on them. With MALACHI_DATA_DIR set (tests, agents, dev builds run
// from a temporary folder) Start leaves the user's mailto: registration and
// Run value alone (Registration/SelfRegistration).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;
using Malachi.Platform.Windows.Registration;
using Malachi.Platform.Windows.Sound;
using Malachi.Platform.Windows.Startup;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Microsoft.UI.Dispatching;
using Microsoft.Windows.AppLifecycle;

namespace Malachi.App.Platform;

/// <summary>
/// Notifications and the new-mail sound, the notification-area icon,
/// launch at login and the <c>mailto:</c> registration.
/// </summary>
public static partial class PlatformServices
{
    private static readonly Lock Gate = new();
    private static ILoggerFactory loggers = NullLoggerFactory.Instance;
    private static ILogger logger = NullLogger.Instance;
    private static NotificationService? notifications;
    private static Action<NotificationActivation>? onActivated;
    private static List<NotificationActivation>? early = [];
    private static DispatcherQueue? dispatcher;
    private static PlatformContext? current;
    private static IDesktopNotifier? notifier;
    private static IDisposable? newMessage;
    private static BackgroundTray? tray;
    private static bool background;
    private static LaunchAtLogin? launchAtLogin;
    private static MailtoRegistration? mailto;

    /// <summary>
    /// Registers the app with the notification platform and routes clicks
    /// on its notifications to <paramref name="activated"/>. Call it first
    /// in Main, before <c>AppInstance.GetActivatedEventArgs</c> (a click on
    /// a notification of an app that is not running starts it through COM,
    /// and the handler has to exist before registering). The handler runs
    /// on the UI thread; clicks that arrive before <see cref="Start"/> are
    /// handed over by it, in order. A click opens its message (the main
    /// window without one). Idempotent. Never throws but for a null handler: whatever the
    /// notification platform throws is logged, and the app then starts
    /// without notifications or without their clicks.
    /// </summary>
    /// <param name="activated">Opens the message of a click; the activation names it.</param>
    /// <param name="loggerFactory">Where the services log; nothing when null.</param>
    /// <exception cref="ArgumentNullException"><paramref name="activated"/> is null.</exception>
    public static void InitializeEarly(Action<NotificationActivation> activated, ILoggerFactory? loggerFactory = null)
    {
        ArgumentNullException.ThrowIfNull(activated);
        lock (Gate)
        {
            if (notifications is not null)
            {
                return;
            }
            loggers = loggerFactory ?? NullLoggerFactory.Instance;
            logger = loggers.CreateLogger("Malachi.App.Platform");
            onActivated = activated;
        }
        var service = NotificationService.Initialize(Activate, logger);
        lock (Gate)
        {
            notifications = service;
        }
    }

    /// <summary>
    /// The click on a notification that started or re-activated the app,
    /// from <c>AppInstance.GetActivatedEventArgs()</c> or the
    /// <c>Activated</c> event of a redirected instance; null when the
    /// activation is anything else (a launch, a <c>mailto:</c> link).
    /// </summary>
    public static NotificationActivation? NotificationActivationFrom(AppActivationArguments? args) =>
        NotificationService.FromActivation(args);

    /// <summary>
    /// Starts the services on the UI thread: the desktop notification and
    /// the new-mail sound of every <c>notify.newMessage</c> of the hub (a
    /// handler added now, so call it before the mailbox adds its own), the
    /// launch-at-login mirror (a Run value of a moved app folder follows
    /// it), the <c>mailto:</c> registration when it is missing or stale (off
    /// the UI thread), and the notification-area icon once the app runs in
    /// the background. With <c>MALACHI_DATA_DIR</c> set, neither the Run
    /// value nor the <c>mailto:</c> registration is written: such a copy
    /// runs from a folder that goes away (<see cref="SelfRegistration"/>).
    /// Clicks held since <see cref="InitializeEarly"/> are handed over. Call
    /// it once, after <see cref="InitializeEarly"/>.
    /// </summary>
    /// <exception cref="InvalidOperationException">Off the UI thread, or a second time.</exception>
    public static void Start(PlatformContext context)
    {
        ArgumentNullException.ThrowIfNull(context);
        var queue = DispatcherQueue.GetForCurrentThread()
            ?? throw new InvalidOperationException("PlatformServices.Start runs on the UI thread");
        List<NotificationActivation> held;
        lock (Gate)
        {
            if (current is not null)
            {
                throw new InvalidOperationException("PlatformServices.Start was called already");
            }
            if (context.LoggerFactory is not null && ReferenceEquals(loggers, NullLoggerFactory.Instance))
            {
                loggers = context.LoggerFactory;
                logger = loggers.CreateLogger("Malachi.App.Platform");
            }
            current = context;
            dispatcher = queue;
            held = early ?? [];
            early = null;
        }
        if (notifications is null)
        {
            LogNotInitialized(logger);
        }
        var exe = ExecutablePath;
        notifier = notifications is { } n ? n : new NoNotifier();
        var policy = new NotificationPolicy(context.Settings, context.IsMainWindowActive, notifier, new NewMailSound(logger));
        // A notification that was shown is remembered, so that it can be
        // withdrawn once it is outdated (WithdrawNotifications).
        newMessage = context.Notifications.AddNewMessage(m =>
        {
            if (policy.Deliver(m))
            {
                context.NotificationShown?.Invoke(m);
            }
        });
        tray = new BackgroundTray(context, queue, exe, logger);
        tray.SetVisible(background);
        var selfRegistration = SelfRegistration.IsAllowed();
        if (!selfRegistration)
        {
            LogNoSelfRegistration(logger, SelfRegistration.DataDirVariable);
        }
        StartLaunchAtLogin(context, selfRegistration);
        if (selfRegistration)
        {
            StartMailtoRegistration();
        }
        foreach (var activation in held)
        {
            Deliver(activation);
        }
    }

    /// <summary>
    /// Withdraws the desktop notifications of these messages from the
    /// notification centre (notify.go <c>WithdrawNotification</c>), by the
    /// tags they were shown with (<see cref="DesktopNotification.TagOf"/>).
    /// The mailbox decides which (MailboxController.Notifications.cs). Call
    /// it on the UI thread; nothing before <see cref="Start"/>. Never throws.
    /// </summary>
    public static void WithdrawNotifications(IReadOnlyList<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        notifier?.Withdraw([.. ids.Select(DesktopNotification.TagOf)]);
    }

    /// <summary>
    /// Whether the app runs in the background with its main window hidden:
    /// the notification-area icon is shown exactly then. Call it on the UI
    /// thread; before <see cref="Start"/> the state is kept for it.
    /// </summary>
    public static void SetRunningInBackground(bool inBackground)
    {
        background = inBackground;
        tray?.SetVisible(inBackground);
    }

    /// <summary>
    /// Stops the services on the way out: the icon leaves the notification
    /// area, the hub's handler is removed and the live notification
    /// registration is revoked (a later click starts the app again). Safe to
    /// call in any state and more than once, and never throws (a failure is
    /// logged), so the shell's later steps on the way out, such as stopping
    /// the daemon it started, always run.
    /// </summary>
    public static void Stop()
    {
        var icon = tray;
        tray = null;
        Quietly("the notification-area icon", () => icon?.Dispose());
        var handler = newMessage;
        newMessage = null;
        notifier = null;
        Quietly("the notify.newMessage handler", () => handler?.Dispose());
        NotificationService? service;
        lock (Gate)
        {
            service = notifications;
            dispatcher = null;
        }
        // Unregister logs its own failures and never throws.
        service?.Unregister();
    }

    /// <summary>Launch at login of this executable, for Preferences (read afresh on every call).</summary>
    public static LaunchAtLogin LaunchAtLogin => launchAtLogin ??= new LaunchAtLogin(ExecutablePath);

    /// <summary>The <c>mailto:</c> registration of this executable, for Preferences and an uninstaller.</summary>
    public static MailtoRegistration Mailto => mailto ??= new MailtoRegistration(ExecutablePath);

    /// <summary>
    /// Opens Settings → Apps → Default apps on Malachi Mail's page (the
    /// Default apps button of Preferences); false when Windows could not.
    /// </summary>
    public static bool OpenDefaultApps() => SystemSettings.Open(SystemSettings.DefaultAppsUri);

    private static string ExecutablePath =>
        current?.ExecutablePath ?? Environment.ProcessPath ?? Path.Combine(AppContext.BaseDirectory, "MalachiMail.exe");

    // A click, from the notification platform's thread: to the UI thread,
    // or held until Start.
    private static void Activate(NotificationActivation activation)
    {
        lock (Gate)
        {
            if (early is not null)
            {
                early.Add(activation);
                return;
            }
        }
        Deliver(activation);
    }

    private static void Deliver(NotificationActivation activation)
    {
        DispatcherQueue? queue;
        Action<NotificationActivation>? handler;
        lock (Gate)
        {
            queue = dispatcher;
            handler = onActivated;
        }
        if (queue is null || handler is null || !queue.TryEnqueue(() => Guard(() => handler(activation))))
        {
            LogActivationDropped(logger);
        }
    }

    private static void StartLaunchAtLogin(PlatformContext context, bool repair)
    {
        try
        {
            if (repair && LaunchAtLogin.RepairMovedExecutable())
            {
                LogRunRepaired(logger);
            }
            LaunchAtLogin.MirrorInto(context.Settings);
        }
        catch (Exception e) when (e is UnauthorizedAccessException or IOException or SecurityException)
        {
            LogRegistryFailed(logger, "launch at login", e.Message);
        }
    }

    private static void StartMailtoRegistration()
    {
        var registration = Mailto;
        _ = Task.Run(() =>
        {
            try
            {
                if (registration.EnsureRegistered())
                {
                    LogMailtoRegistered(logger);
                }
            }
            catch (Exception e) when (e is UnauthorizedAccessException or IOException or SecurityException)
            {
                LogRegistryFailed(logger, "mailto: registration", e.Message);
            }
        });
    }

    // A step on the way out: logged when it fails, so that the next one runs.
    private static void Quietly(string what, Action step)
    {
        try
        {
            step();
        }
#pragma warning disable CA1031 // Stop never throws: the shell still has the daemon to stop.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogStopFailed(logger, what, e);
        }
    }

    private static void Guard(Action action)
    {
        try
        {
            action();
        }
#pragma warning disable CA1031 // A queued callback that throws would end the application.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogHandlerFailed(logger, e);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "platform: Start without InitializeEarly; no desktop notifications")]
    private static partial void LogNotInitialized(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "platform: a notification click was dropped; the UI thread is gone")]
    private static partial void LogActivationDropped(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "platform: the Run value pointed at a moved app folder and was rewritten")]
    private static partial void LogRunRepaired(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "platform: mailto: registration written")]
    private static partial void LogMailtoRegistered(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "platform: {Variable} is set; the mailto: registration and the Run value are left alone")]
    private static partial void LogNoSelfRegistration(ILogger logger, string variable);

    [LoggerMessage(Level = LogLevel.Warning, Message = "platform: stopping {What} failed")]
    private static partial void LogStopFailed(ILogger logger, string what, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "platform: {What}: {Reason}")]
    private static partial void LogRegistryFailed(ILogger logger, string what, string reason);

    [LoggerMessage(Level = LogLevel.Error, Message = "platform: the activation handler failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception error);

    /// <summary>Desktop notifications when the platform was never initialised: none.</summary>
    private sealed class NoNotifier : IDesktopNotifier
    {
        public void Show(DesktopNotification notification)
        {
        }

        public void Withdraw(IReadOnlyList<string> tags)
        {
        }
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (post, the delegate's didReceive), the Windows side of
// ui/internal/window/notify.go (SendNotification with app.show as the
// default action): desktop notifications through the Windows App SDK's
// AppNotificationManager, for an unpackaged app (docs/windows-port.md §10,
// measured in APP-SPIKES §1).
//
// The order matters: NotificationInvoked is attached before Register(),
// or COM registers the class single-use and every click starts a new
// process. Register("Malachi Mail", Assets\notification.png) with no
// explicit AUMID: Windows keys the registration by the executable's path
// and shows the name and icon given here. A click while the app runs raises
// NotificationInvoked (on a worker thread); a click after the app exited
// starts it through COM with "----AppNotificationActivated: -Embedding",
// and the activation arrives through AppInstance.GetActivatedEventArgs as
// kind AppNotification (FromActivation), not through the event. Unregister()
// on exit only revokes the live class object: a later click still starts
// the app. AppNotificationManager.IsSupported() is false in an elevated
// process: no notifications then, as macOS has none unbundled. With the
// Windows App SDK 2.5.1 Register() throws 0x8007007E unless the build puts
// Microsoft.WindowsAppRuntime.Insights.Resource.dll beside the app (the
// build target of §10); the toasts still show then, but clicks are lost,
// and the failure is logged. Notifications are optional and the platform is
// fragile, so nothing here throws: CsWinRT turns a failed HRESULT into
// COMException, UnauthorizedAccessException, ArgumentException,
// InvalidOperationException, FileNotFoundException and others, and every
// one of them is logged with its HRESULT and leaves the app without
// notifications (or without their clicks) instead of stopping it.
//
// The toast is plain text only: the title and body are mail data (the
// sender, the subject), which AppNotificationBuilder escapes into its XML.
// The sound is the app's own switch (NewMailSound), so the toast is muted.

using System;
using System.IO;
using Malachi.Core;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;
using Microsoft.Extensions.Logging;
using Microsoft.Windows.AppLifecycle;
using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;

namespace Malachi.App.Platform;

/// <summary>New-message notifications as Windows toasts.</summary>
internal sealed partial class NotificationService : IDesktopNotifier
{
    private readonly Action<NotificationActivation> onActivated;
    private readonly ILogger logger;
    private bool supported;
    private bool registered;

    private NotificationService(Action<NotificationActivation> onActivated, ILogger logger)
    {
        this.onActivated = onActivated;
        this.logger = logger;
    }

    /// <summary>The icon the notifications show, beside the executable.</summary>
    public static string IconPath { get; } = Path.Combine(AppContext.BaseDirectory, "Assets", "notification.png");

    /// <summary>Whether Register() succeeded, so that clicks reach the app.</summary>
    public bool IsRegistered => registered;

    /// <summary>
    /// Attaches <paramref name="onActivated"/> for clicks (called on a
    /// worker thread) and registers the app with the notification platform.
    /// Call it first in Main, before AppInstance.GetActivatedEventArgs.
    /// </summary>
    public static NotificationService Initialize(Action<NotificationActivation> onActivated, ILogger logger)
    {
        var service = new NotificationService(onActivated, logger);
        service.Register();
        return service;
    }

    /// <summary>
    /// The activation of a click that started or re-activated the app
    /// (kind AppNotification); null for any other activation.
    /// </summary>
    public static NotificationActivation? FromActivation(AppActivationArguments? args) =>
        args is { Kind: ExtendedActivationKind.AppNotification, Data: AppNotificationActivatedEventArgs e }
            ? NotificationArguments.Parse(e.Arguments)
            : null;

    /// <inheritdoc/>
    public void Show(DesktopNotification notification)
    {
        ArgumentNullException.ThrowIfNull(notification);
        if (!supported)
        {
            return;
        }
        try
        {
            var builder = new AppNotificationBuilder();
            foreach (var (key, value) in NotificationArguments.For(notification))
            {
                builder.AddArgument(key, value);
            }
            var toast = builder
                .AddText(notification.Title)
                .AddText(notification.Body)
                .MuteAudio()
                .BuildNotification();
            toast.Tag = notification.Tag;
            toast.Group = notification.Group;
            AppNotificationManager.Default.Show(toast);
        }
#pragma warning disable CA1031 // A notification that cannot be shown is logged; the sound and the list still follow.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogNotShown(logger, e.HResult, e);
        }
    }

    /// <summary>
    /// Revokes the live registration on the way out (a later click starts
    /// the app again). Safe to call more than once; never throws (a failure
    /// is logged).
    /// </summary>
    public void Unregister()
    {
        if (!registered)
        {
            return;
        }
        registered = false;
        try
        {
            AppNotificationManager.Default.Unregister();
        }
#pragma warning disable CA1031 // The way out goes on: the shell still has the daemon to stop.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogNotUnregistered(logger, e.HResult, e);
        }
    }

    // Never throws: whatever the platform throws leaves the app without
    // notifications (IsSupported) or without their clicks (Register).
    private void Register()
    {
        try
        {
            supported = AppNotificationManager.IsSupported();
        }
#pragma warning disable CA1031 // An optional feature: the app starts without it.
        catch (Exception e)
#pragma warning restore CA1031
        {
            supported = false;
            LogNotSupportedError(logger, e.HResult, e);
            return;
        }
        if (!supported)
        {
            LogNotSupported(logger);
            return;
        }
        try
        {
            var manager = AppNotificationManager.Default;
            // Before Register(): a handler makes COM register the class for
            // many activations, so a click reaches this process.
            manager.NotificationInvoked += OnNotificationInvoked;
            if (File.Exists(IconPath))
            {
                manager.Register(AppIdentity.DisplayName, new Uri(IconPath));
            }
            else
            {
                // The name is then the executable's, the icon extracted from it.
                LogNoIcon(logger);
                manager.Register();
            }
            registered = true;
        }
#pragma warning disable CA1031 // An optional feature: the app starts without the clicks.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogNotRegistered(logger, e.HResult, e);
        }
    }

    private void OnNotificationInvoked(AppNotificationManager sender, AppNotificationActivatedEventArgs args)
    {
        try
        {
            onActivated(NotificationArguments.Parse(args.Arguments));
        }
#pragma warning disable CA1031 // Nothing may be thrown back into the notification platform's thread.
        catch (Exception e)
#pragma warning restore CA1031
        {
            LogHandlerFailed(logger, e);
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "notifications: not supported here (an elevated process); none are shown")]
    private static partial void LogNotSupported(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "notifications: IsSupported failed (0x{HResult:X8}); none are shown")]
    private static partial void LogNotSupportedError(ILogger logger, int hResult, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "notifications: Assets\\notification.png is missing; registered under the executable's name")]
    private static partial void LogNoIcon(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "notifications: Register failed (0x{HResult:X8}); clicks will not reach the app")]
    private static partial void LogNotRegistered(ILogger logger, int hResult, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "notifications: Unregister failed (0x{HResult:X8})")]
    private static partial void LogNotUnregistered(ILogger logger, int hResult, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "notifications: a notification was not shown (0x{HResult:X8})")]
    private static partial void LogNotShown(ILogger logger, int hResult, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "notifications: the click handler failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception error);
}

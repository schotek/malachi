// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (deliver); GTK: ui/internal/window/notify.go (notifyNewMessage) and
// window.go (playNewMailSound). The rules are GTK's: nothing at all while
// the user is looking at the main window (Window.IsActive; Swift
// isMainWindowKey), then the notification behind desktop-notifications and,
// independently of it, the sound behind notification-sound. Moved out of the
// view layer, where macOS keeps it untested (docs/windows-port.md §7.4); the
// toast and the sound are behind IDesktopNotifier and INewMailSound.
// Windows-only: the sound is also skipped while Windows asks applications
// not to disturb the user (INewMailSound.IsQuietTime), which Windows does
// for toasts by itself.

using System;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Presentation;

/// <summary>
/// Decides what a new message sounds and shows like: the desktop
/// notification and the new-mail sound, each behind its own setting, and
/// neither while the main window is the active window.
/// </summary>
public sealed class NotificationPolicy
{
    private readonly SettingsStore settings;
    private readonly Func<bool> isMainWindowActive;
    private readonly IDesktopNotifier notifier;
    private readonly INewMailSound sound;

    /// <summary>A policy over the user's <paramref name="settings"/>.</summary>
    /// <param name="settings">desktop-notifications and notification-sound, read at every message.</param>
    /// <param name="isMainWindowActive">
    /// Whether the main window is the active (foreground) window right now:
    /// the user is looking at it, and nothing is shown or played (notify.go
    /// <c>w.IsActive()</c>).
    /// </param>
    /// <param name="notifier">Shows the notification.</param>
    /// <param name="sound">Plays the sound.</param>
    public NotificationPolicy(SettingsStore settings, Func<bool> isMainWindowActive, IDesktopNotifier notifier, INewMailSound sound)
    {
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(isMainWindowActive);
        ArgumentNullException.ThrowIfNull(notifier);
        ArgumentNullException.ThrowIfNull(sound);
        this.settings = settings;
        this.isMainWindowActive = isMainWindowActive;
        this.notifier = notifier;
        this.sound = sound;
    }

    /// <summary>
    /// Shows a desktop notification (and plays the sound) for a new message
    /// unless the user is looking at the main window right now (Swift
    /// <c>deliver</c>, notify.go <c>notifyNewMessage</c>).
    /// </summary>
    public void Deliver(NewMessageNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        if (isMainWindowActive())
        {
            return;
        }
        if (settings.DesktopNotifications)
        {
            notifier.Show(DesktopNotification.For(n));
        }
        if (settings.NotificationSound && !sound.IsQuietTime)
        {
            sound.Play();
        }
    }
}

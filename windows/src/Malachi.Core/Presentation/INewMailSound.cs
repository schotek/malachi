// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the seam between NotificationPolicy and the platform's
// new-mail sound, which macOS keeps inside NotificationService.swift
// (playSound: NSSound "Glass") and GTK in window.go (playNewMailSound: the
// sound theme's message-new-email). The Windows implementation is
// Malachi.Platform.Windows.Sound.NewMailSound (PlaySound "MailBeep").

namespace Malachi.Core.Presentation;

/// <summary>The sound of new mail.</summary>
public interface INewMailSound
{
    /// <summary>
    /// Whether Windows asks applications not to disturb the user now (quiet
    /// time, a presentation, a full-screen application): the sound is
    /// skipped then. Toasts are left to Windows, which holds them back
    /// itself.
    /// </summary>
    bool IsQuietTime { get; }

    /// <summary>
    /// Starts the sound and returns without waiting for it. Never throws: a
    /// sound that cannot play is logged and forgotten, as GTK's is.
    /// </summary>
    void Play();
}

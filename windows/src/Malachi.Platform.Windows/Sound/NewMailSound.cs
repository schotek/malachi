// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Windows new-mail sound: GTK ui/internal/window/window.go
// (playNewMailSound: the sound theme's message-new-email, a failure logged
// at debug level) and ui/internal/sound; macOS
// macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (playSound: NSSound "Glass"). Windows plays the user's own sound for the
// system event MailBeep ("Desktop Mail Notification" in Control Panel →
// Sound, Windows Notify Email.wav by default) through PlaySound with
// SND_ALIAS | SND_ASYNC | SND_NODEFAULT | SND_SYSTEM: asynchronous, silent
// when the user set no sound for it instead of the default beep, and in the
// System Sounds session of the volume mixer, so that muting or lowering
// system sounds applies to it as it does to GTK's event sound (canberra's
// event role) (docs/windows-port.md §10, a row of windows/README.md). When
// to stay quiet is QuietHours'.

using System;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Windows.Win32;
using Windows.Win32.Media.Audio;

namespace Malachi.Platform.Windows.Sound;

/// <summary>Plays the user's new-mail system sound.</summary>
public sealed partial class NewMailSound : INewMailSound
{
    /// <summary>The system sound event (HKCU\AppEvents\Schemes\Apps\.Default\MailBeep).</summary>
    public const string Alias = "MailBeep";

    /// <summary>
    /// What PlaySound gets: the event by its alias, asynchronously, nothing
    /// when the event has no sound, at the system sounds' volume.
    /// </summary>
    internal const SND_FLAGS Flags = SND_FLAGS.SND_ALIAS | SND_FLAGS.SND_ASYNC | SND_FLAGS.SND_NODEFAULT | SND_FLAGS.SND_SYSTEM;

    private readonly Func<bool> isQuiet;
    private readonly Func<string, SND_FLAGS, bool> play;
    private readonly ILogger logger;

    /// <summary>The sound through PlaySound, quiet while <see cref="QuietHours"/> says so.</summary>
    public NewMailSound(ILogger? logger = null)
        : this(QuietHours.IsQuiet, PlaySound, logger)
    {
    }

    /// <summary>A sound over other calls: <paramref name="play"/> takes the alias and the flags and says whether it plays.</summary>
    internal NewMailSound(Func<bool> isQuiet, Func<string, SND_FLAGS, bool> play, ILogger? logger)
    {
        this.isQuiet = isQuiet;
        this.play = play;
        this.logger = logger ?? NullLogger.Instance;
    }

    /// <inheritdoc/>
    public bool IsQuietTime => isQuiet();

    /// <inheritdoc/>
    public void Play()
    {
        if (!play(Alias, Flags))
        {
            // No sound set for the event, or no audio device: GTK logs its
            // sound failures at debug level too.
            LogNotPlayed(logger, Alias);
        }
    }

    /// <summary>PlaySound of <paramref name="alias"/> with <paramref name="flags"/>.</summary>
    internal static unsafe bool PlaySound(string alias, SND_FLAGS flags)
    {
        fixed (char* name = alias)
        {
            return PInvoke.PlaySound(name, default, flags);
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "new-mail sound: {Alias} did not play")]
    private static partial void LogNotPlayed(ILogger logger, string alias);
}

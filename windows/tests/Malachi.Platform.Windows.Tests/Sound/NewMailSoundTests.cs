// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of NewMailSound, the counterpart of window.go playNewMailSound: the
// MailBeep event through PlaySound, asynchronous, silent when the user set
// no sound for it and in the System Sounds session of the volume mixer, a
// failure logged at debug level and never thrown;
// quiet hours asked of QuietHours. The real PlaySound is called only with an
// alias that no scheme has, which plays nothing.

using System;
using System.Collections.Generic;
using Malachi.Platform.Windows.Sound;
using Microsoft.Extensions.Logging;
using Windows.Win32.Media.Audio;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Sound;

public sealed class NewMailSoundTests
{
    [Fact]
    public void PlaysTheMailBeepEventAsynchronouslyAsASystemSoundAndNeverTheDefaultBeep()
    {
        var calls = new List<(string Alias, SND_FLAGS Flags)>();
        var sound = new NewMailSound(() => false, (alias, flags) =>
        {
            calls.Add((alias, flags));
            return true;
        }, null);
        sound.Play();
        var (alias, flags) = Assert.Single(calls);
        Assert.Equal("MailBeep", alias);
        Assert.Equal(SND_FLAGS.SND_ALIAS | SND_FLAGS.SND_ASYNC | SND_FLAGS.SND_NODEFAULT | SND_FLAGS.SND_SYSTEM, flags);
    }

    [Fact]
    public void ASoundThatDoesNotPlayIsLoggedAtDebugLevel()
    {
        var logger = new ListLogger();
        var sound = new NewMailSound(() => false, (_, _) => false, logger);
        sound.Play();
        Assert.Equal([(LogLevel.Debug, "new-mail sound: MailBeep did not play")], logger.Entries);
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void QuietHoursAreAskedEachTime(bool quiet)
    {
        var asked = 0;
        var sound = new NewMailSound(() =>
        {
            asked++;
            return quiet;
        }, (_, _) => true, null);
        Assert.Equal(quiet, sound.IsQuietTime);
        Assert.Equal(quiet, sound.IsQuietTime);
        Assert.Equal(2, asked);
    }

    [Fact]
    public void TheRealCallsAnswer()
    {
        // An event without a sound: PlaySound says no and, without
        // SND_ASYNC (which answers yes once it queued the request), says so
        // at once; SND_NODEFAULT keeps the default beep from playing.
        Assert.False(NewMailSound.PlaySound("MalachiMailTestsNoSuchEvent", NewMailSound.Flags & ~SND_FLAGS.SND_ASYNC));
        _ = new NewMailSound().IsQuietTime;
    }

    private sealed class ListLogger : ILogger
    {
        public List<(LogLevel Level, string Message)> Entries { get; } = [];

        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            ArgumentNullException.ThrowIfNull(formatter);
            Entries.Add((logLevel, formatter(state, exception)));
        }
    }
}

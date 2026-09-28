// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/main.go (newLogger: slog's text handler on stderr, the level
// from MALACHI_LOG_LEVEL) and of the os.Logger of the macOS app. A WinUI
// app started from the Start menu has no stderr, so the lines go to
// logs\MalachiMail.log in the data directory (rotated as the daemon's log,
// RotatingLogFile) and, when the app is attached to the terminal it was
// started from (make run-windows), to that terminal too
// (docs/windows-port.md §1, §5). What reaches a logger is what the code
// hands it: method names, codes, ids and reasons, never mail content, keys
// or tokens (§3.1); an exception is written with its type and message, and
// its stack at debug level.

using System;
using System.Globalization;
using System.Text;
using Malachi.Core.Daemon;
using Malachi.Platform.Windows.Consoles;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The app's log: a file, and the terminal when there is one.</summary>
public sealed class AppLog : ILoggerFactory
{
    /// <summary>The app's log file in the logs directory.</summary>
    public const string FileName = "MalachiMail.log";

    /// <summary>The level variable of ui/main.go.</summary>
    public const string LevelEnv = "MALACHI_LOG_LEVEL";

    private readonly RotatingLogFile? file;
    private readonly TerminalWriter? terminal;

    /// <summary>A log into <paramref name="logDirectory"/> (none when null) and <paramref name="terminal"/> (none when null).</summary>
    public AppLog(string? logDirectory, TerminalWriter? terminal, LogLevel? minimum = null)
    {
        file = logDirectory is null ? null : new RotatingLogFile(System.IO.Path.Combine(logDirectory, FileName));
        this.terminal = terminal;
        Minimum = minimum ?? LevelOf(Environment.GetEnvironmentVariable(LevelEnv));
    }

    /// <summary>The lowest level written.</summary>
    public LogLevel Minimum { get; }

    /// <summary>MALACHI_LOG_LEVEL: debug, info (the default), warn or warning, error.</summary>
    public static LogLevel LevelOf(string? value) => value?.Trim().ToLowerInvariant() switch
    {
        "debug" => LogLevel.Debug,
        "warn" or "warning" => LogLevel.Warning,
        "error" => LogLevel.Error,
        _ => LogLevel.Information,
    };

    /// <inheritdoc/>
    public ILogger CreateLogger(string categoryName) => new Category(this, Short(categoryName));

    /// <inheritdoc/>
    public void AddProvider(ILoggerProvider provider)
    {
        // One destination, the app's own.
    }

    /// <inheritdoc/>
    public void Dispose() => file?.Dispose();

    // Malachi.Core.Controllers.MailboxController → MailboxController.
    private static string Short(string category)
    {
        var dot = category.LastIndexOf('.');
        return dot >= 0 && dot + 1 < category.Length ? category[(dot + 1)..] : category;
    }

    private static string LevelName(LogLevel level) => level switch
    {
        LogLevel.Trace => "TRACE",
        LogLevel.Debug => "DEBUG",
        LogLevel.Information => "INFO",
        LogLevel.Warning => "WARN",
        LogLevel.Error => "ERROR",
        _ => "CRIT",
    };

    private void Write(LogLevel level, string category, string message, Exception? error)
    {
        var line = new StringBuilder()
            .Append(DateTimeOffset.Now.ToString("yyyy-MM-ddTHH:mm:ss.fffzzz", CultureInfo.InvariantCulture))
            .Append(' ').Append(LevelName(level))
            .Append(' ').Append(category).Append(": ").Append(message);
        if (error is not null)
        {
            line.Append(" (").Append(error.GetType().Name).Append(": ").Append(error.Message).Append(')');
            if (Minimum <= LogLevel.Debug && error.StackTrace is { } stack)
            {
                line.Append(Environment.NewLine).Append(stack);
            }
        }
        var text = line.ToString();
        file?.WriteLine(text);
        terminal?.WriteLine(text);
    }

    private sealed class Category(AppLog log, string name) : ILogger
    {
        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => logLevel != LogLevel.None && logLevel >= log.Minimum;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            if (!IsEnabled(logLevel))
            {
                return;
            }
            log.Write(logLevel, name, formatter(state, exception), exception);
        }
    }
}

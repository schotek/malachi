// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The views' log lines (WebViewLog, through WebViewEnvironment.LoggerFactory)
// as events of the run, so a test sees what a view decided (its recovery,
// a document that did not load) and a hung run's progress file says where
// it stopped. Information and above; the views log no content.

using System;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Canary.Host;

/// <summary>Hands every log line of the views to <paramref name="write"/> (category, message).</summary>
internal sealed class HostLoggerFactory(Action<string, string> write) : ILoggerFactory
{
    public ILogger CreateLogger(string categoryName) => new HostLogger(categoryName, write);

    public void AddProvider(ILoggerProvider provider)
    {
    }

    public void Dispose()
    {
    }

    private sealed class HostLogger(string category, Action<string, string> write) : ILogger
    {
        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => logLevel >= LogLevel.Information;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            if (IsEnabled(logLevel))
            {
                write(category, logLevel + " " + formatter(state, exception));
            }
        }
    }
}

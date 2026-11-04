// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (ToastRouter); GTK:
// window.go Toast (the main window's overlay, for the application's life).
// The toasts the application hands out go to the overlay of the window
// that was activated last (WindowTracker sets it), so a toast from a
// window-local action lands over the pane the user is looking at; when
// that window is gone the main window's overlay takes it. Without either
// the text is logged at debug level only as its length (a toast may carry
// an address or a file name) and dropped.

using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.App.Shell;

/// <summary>The application's toasts, routed to a window's overlay.</summary>
public sealed partial class ToastRouter : IToasts
{
    private readonly ILogger logger;

    /// <summary>A router with no overlay yet.</summary>
    public ToastRouter(ILogger? logger = null)
    {
        this.logger = logger ?? NullLogger.Instance;
    }

    /// <summary>The overlay of the window activated last; WindowTracker sets and clears it.</summary>
    public IToasts? Presenter { get; set; }

    /// <summary>The main window's overlay, for when the last window's is gone.</summary>
    public IToasts? Fallback { get; set; }

    /// <inheritdoc/>
    public void Show(string text) => Show(text, Core.Presentation.ToastPresenter.DefaultSeconds);

    /// <inheritdoc/>
    public void Show(string text, int seconds)
    {
        if ((Presenter ?? Fallback) is not { } target)
        {
            LogDropped(logger, text?.Length ?? 0);
            return;
        }
        target.Show(text ?? "", seconds);
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "a toast of {Length} characters without an overlay was dropped")]
    private static partial void LogDropped(ILogger logger, int length);
}

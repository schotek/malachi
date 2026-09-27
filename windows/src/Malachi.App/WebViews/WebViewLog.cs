// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The log lines of the web views: the counterparts of macOS's
// Logger(category: "htmlview" / "editor") and GTK's slog lines in
// htmlview/view.go and editor/editor.go. Kinds, statuses and counts only:
// never a URL, a link, a file name or any other content of a message
// (docs/windows-port.md §3.1).

using System;
using Microsoft.Extensions.Logging;

namespace Malachi.App.WebViews;

/// <summary>Log messages of the WebView2 layer.</summary>
internal static partial class WebViewLog
{
    [LoggerMessage(Level = LogLevel.Error, Message = "web view unavailable; nothing is loaded")]
    public static partial void Unavailable(ILogger logger, Exception? error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "navigation refused")]
    public static partial void NavigationRefused(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "request refused with {Status}")]
    public static partial void Refused(ILogger logger, int status);

    [LoggerMessage(Level = LogLevel.Warning, Message = "request gate failed; refused")]
    public static partial void GateFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "navigation to the view's document failed")]
    public static partial void NavigateFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "script failed")]
    public static partial void ScriptFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "closing the web view failed")]
    public static partial void CloseFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "context menu could not be reduced; not shown")]
    public static partial void ContextMenuFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "web view process failed: {Kind}")]
    public static partial void ProcessFailed(ILogger logger, string kind);

    [LoggerMessage(Level = LogLevel.Debug, Message = "picture not served")]
    public static partial void PictureFailed(ILogger logger, Exception? error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "bad bridge message")]
    public static partial void BadBridgeMessage(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "bridge message from another document dropped")]
    public static partial void ForeignBridgeMessage(ILogger logger);
}

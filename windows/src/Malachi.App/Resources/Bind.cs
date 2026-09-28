// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the small functions {x:Bind} calls in the XAML of the
// shell and the screens (x:Bind takes a static function by its namespace:
// {x:Bind res:Bind.Visible(Sync.Line.Spinning), Mode=OneWay}). Nothing
// here formats mail data; the texts come from Core.

using Malachi.Core;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Resources;

/// <summary>Functions for x:Bind.</summary>
public static class Bind
{
    /// <summary>The application's name (window.blp's title, not translated).</summary>
    public static string AppName => AppIdentity.DisplayName;

    /// <summary>Visible for true, collapsed for false.</summary>
    public static Visibility Visible(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>Collapsed for an empty string.</summary>
    public static Visibility VisibleIfText(string? text) => string.IsNullOrEmpty(text) ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>The connection's sentence (status.go statusLineFor's texts).</summary>
    public static string ConnectionText(ConnView connection) => SyncController.ConnectionStatusLine(connection.State).Text;
}

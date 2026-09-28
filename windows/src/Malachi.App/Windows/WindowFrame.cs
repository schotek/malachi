// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what macos/Sources/MalachiMail/Windows/MessageWindowController.swift
// and EmbeddedWindowController.swift do to their NSWindow (the size, the
// minimum size, the unified title bar); GTK: message_window.blp and
// embedded_window.blp (default-width 820, default-height 620,
// width-request 360, height-request 294, an Adw.HeaderBar). The frame of
// the reader's secondary windows, as the main window's (docs/windows-port.md
// §11.1): the WinUI TitleBar on Mica (or the page background where Mica is
// not available), the size in effective pixels at the window's DPI, and
// the smallest size kept through WM_GETMINMAXINFO (the main window's hook,
// whose session end does nothing here: the main window stops the daemon).
//
// The folder is Windows/, the namespace MessageWindows: a namespace
// Malachi.App.Windows would hide the Windows.* namespaces (Windows.UI,
// Windows.ApplicationModel) from every file of the app.

using System;
using System.IO;
using Malachi.App.Shell;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.MessageWindows;

/// <summary>The frame of a message, attached-message or preview window.</summary>
internal static class WindowFrame
{
    /// <summary>message_window.blp default-width.</summary>
    public const int DefaultWidth = 820;

    /// <summary>message_window.blp default-height.</summary>
    public const int DefaultHeight = 620;

    /// <summary>message_window.blp width-request.</summary>
    public const int MinWidth = 360;

    /// <summary>message_window.blp height-request.</summary>
    public const int MinHeight = 294;

    /// <summary>
    /// Sets up <paramref name="window"/> with <paramref name="titleBar"/> and
    /// its size; <paramref name="solidBackground"/> shows where Mica is not
    /// available. The returned hook keeps the smallest size until disposed.
    /// </summary>
    public static IDisposable Apply(Window window, TitleBar titleBar, UIElement solidBackground, int width = DefaultWidth, int height = DefaultHeight)
    {
        window.ExtendsContentIntoTitleBar = true;
        window.SetTitleBar(titleBar);
        window.AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            window.AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            solidBackground.Visibility = Visibility.Visible;
        }
        var hwnd = WindowPresenter.Handle(window);
        var dpi = PInvoke.GetDpiForWindow((HWND)hwnd);
        var scale = dpi == 0 ? 1.0 : dpi / 96.0;
        window.AppWindow.Resize(new SizeInt32((int)Math.Round(width * scale), (int)Math.Round(height * scale)));
        return new MainWindowHook(hwnd, MinWidth, MinHeight, static () => { });
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (showMainWindow:
// showWindow, makeKeyAndOrderFront, NSApp.activate); GTK: Present(). The
// show path of docs/windows-port.md §10, measured in APP-SPIKES.md §5.2:
// AppWindow.Show() and Activate() never bring a window to the front by
// themselves, SetForegroundWindow(hwnd) does whenever the activation came
// with the right to (a click on a notification or the tray icon, a second
// instance the user started, whose redirect passes the right on). Every
// path that shows a window goes through here.

using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Windows.Win32;
using Windows.Win32.Foundation;
using WinRT.Interop;

namespace Malachi.App.Shell;

/// <summary>Shows a window and brings it to the front.</summary>
internal static class WindowPresenter
{
    /// <summary>Show, activate, then SetForegroundWindow; a minimised window is restored first.</summary>
    public static void Present(Window window)
    {
        if (window.AppWindow.Presenter is OverlappedPresenter { State: OverlappedPresenterState.Minimized } overlapped)
        {
            overlapped.Restore();
        }
        window.AppWindow.Show();
        window.Activate();
        PInvoke.SetForegroundWindow((HWND)WindowNative.GetWindowHandle(window));
    }

    /// <summary>The window's handle.</summary>
    public static nint Handle(Window window) => WindowNative.GetWindowHandle(window);
}

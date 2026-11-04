// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §5, §11.1): two window messages
// of the main window that WinUI does not surface. WM_ENDSESSION: the
// session ends (log off, shut down, restart), and a process with windows
// gets no CTRL_LOGOFF or CTRL_SHUTDOWN, so this is where the daemon the app
// started is stopped; the process may be ended as soon as the message
// returns, so the stop is waited for here. WM_GETMINMAXINFO: the smallest
// size of window.blp (width-request 360, height-request 294), in
// effective pixels at the window's DPI. The hook is a subclass of the
// window's procedure (comctl32 SetWindowSubclass) on the UI thread.

using System;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.Shell;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.App.Shell;

/// <summary>The main window's session end and minimum size.</summary>
internal sealed unsafe class MainWindowHook : IDisposable
{
    private const nuint SubclassId = 0x4D414C41; // "MALA"

    private readonly HWND hwnd;
    private readonly SUBCLASSPROC proc;
    private readonly int minWidth;
    private readonly int minHeight;
    private readonly Action sessionEnding;
    private bool installed;

    /// <param name="hwnd">The main window.</param>
    /// <param name="minWidth">Its smallest width in effective pixels.</param>
    /// <param name="minHeight">Its smallest height in effective pixels.</param>
    /// <param name="sessionEnding">Runs, and is waited for, when the session ends.</param>
    public MainWindowHook(nint hwnd, int minWidth, int minHeight, Action sessionEnding)
    {
        this.hwnd = (HWND)hwnd;
        this.minWidth = minWidth;
        this.minHeight = minHeight;
        this.sessionEnding = sessionEnding;
        // Kept in a field: the procedure is called for as long as the window lives.
        proc = WindowProc;
        installed = PInvoke.SetWindowSubclass(this.hwnd, proc, SubclassId, 0);
    }

    /// <summary>Whether the hook is in place.</summary>
    public bool Installed => installed;

    /// <summary>Removes the hook.</summary>
    public void Dispose()
    {
        if (installed)
        {
            PInvoke.RemoveWindowSubclass(hwnd, proc, SubclassId);
            installed = false;
        }
    }

    private LRESULT WindowProc(HWND window, uint message, WPARAM wParam, LPARAM lParam, nuint id, nuint data)
    {
        switch (message)
        {
            case PInvoke.WM_GETMINMAXINFO:
                // WinUI's own procedure fills the rest; the smallest size is ours.
                var result = PInvoke.DefSubclassProc(window, message, wParam, lParam);
                var dpi = PInvoke.GetDpiForWindow(window);
                var scale = dpi == 0 ? 1.0 : dpi / 96.0;
                var info = (MINMAXINFO*)lParam.Value;
                info->ptMinTrackSize.X = Math.Max(info->ptMinTrackSize.X, (int)Math.Ceiling(minWidth * scale));
                info->ptMinTrackSize.Y = Math.Max(info->ptMinTrackSize.Y, (int)Math.Ceiling(minHeight * scale));
                return result;
            case PInvoke.WM_ENDSESSION when wParam.Value != 0:
                sessionEnding();
                break;
        }
        return PInvoke.DefSubclassProc(window, message, wParam, lParam);
    }
}

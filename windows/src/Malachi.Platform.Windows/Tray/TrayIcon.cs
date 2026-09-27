// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the notification-area icon of docs/windows-port.md §10,
// the class measured in APP-SPIKES §4.3 (spikes2/app/rec/TrayIcon.cs) with
// its two bugs fixed as the report notes (the icon is re-added on every
// TaskbarCreated; the menu's anchor is read as signed coordinates), one
// instance per window instead of a static current one, and a 32-bit guard.
// GTK and macOS have no counterpart: GNOME has no tray and macOS has the
// Dock; the row "a notification-area icon while running in the background"
// of windows/README.md.
//
// The host is a hidden top-level WS_EX_TOOLWINDOW window, not a
// message-only one: only top-level windows receive the TaskbarCreated
// broadcast that Explorer sends after a restart (measured). The icon is
// version 4 (NIN_SELECT for a click or Enter, WM_CONTEXTMENU with the
// anchor in wParam), taken from the executable. The menu is the native
// TrackPopupMenuEx: a WinUI MenuFlyout opened from the tray lands behind
// other windows, gets no keyboard and shows nothing while its owner is
// hidden (measured). New icons land in the Windows 11 overflow.

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Runtime.InteropServices;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;
using static Malachi.Platform.Windows.Tray.NotifyIconInterop;

namespace Malachi.Platform.Windows.Tray;

/// <summary>
/// An icon in the notification area with a native context menu.
/// </summary>
/// <remarks>
/// Thread-affine: create it, show its menu and dispose it on one thread
/// with a message loop (the UI thread), which is where its events are
/// raised. An exception of a handler is logged, never thrown into the
/// window procedure.
/// </remarks>
public sealed unsafe partial class TrayIcon : IDisposable
{
    /// <summary>The message Explorer broadcasts to top-level windows after it (re)starts.</summary>
    internal static readonly uint TaskbarCreatedMessage = PInvoke.RegisterWindowMessage("TaskbarCreated");

    /// <summary>The icon's callback message.</summary>
    internal const uint CallbackMessage = PInvoke.WM_APP + 1;

    /// <summary>NIN_KEYSELECT: Enter or Space on the icon (NIN_SELECT | NINF_KEY).</summary>
    internal const uint NinKeySelect = PInvoke.NIN_SELECT | 1;

    private const uint IconId = 1;
    private const int ClassAlreadyExists = 1410;
    private const string ClassName = "MalachiMailTrayIcon";
    private const uint TpmRightButton = 0x0002;
    private const uint TpmBottomAlign = 0x0020;
    private const uint TpmReturnCmd = 0x0100;

    // The window procedure of every icon's window, kept alive for the
    // process; the windows find their icon in Icons.
    private static readonly WNDPROC Procedure = WindowProcedure;
    private static readonly ConcurrentDictionary<nint, TrayIcon> Icons = new();
    private static readonly Lazy<HINSTANCE> WindowClass = new(RegisterWindowClass);

    private readonly HWND window;
    private readonly HICON icon;
    private readonly bool ownsIcon;
    private readonly string tooltip;
    private readonly ILogger logger;
    private bool disposed;

    /// <summary>
    /// Adds an icon with <paramref name="tooltip"/> to the notification
    /// area, its image the small icon of <paramref name="iconFile"/> (the
    /// running executable when null; the system's application icon when the
    /// file has none).
    /// </summary>
    /// <exception cref="PlatformNotSupportedException">In a 32-bit process.</exception>
    /// <exception cref="InvalidOperationException">When the host window cannot be created.</exception>
    public TrayIcon(string tooltip, string? iconFile = null, ILogger? logger = null)
    {
        ArgumentNullException.ThrowIfNull(tooltip);
        if (!Environment.Is64BitProcess)
        {
            throw new PlatformNotSupportedException("the notification-area interop is declared for 64-bit processes");
        }
        this.tooltip = tooltip.Length < TipLength ? tooltip : tooltip[..(TipLength - 1)];
        this.logger = logger ?? NullLogger.Instance;
        var instance = WindowClass.Value;
        fixed (char* cls = ClassName)
        fixed (char* title = "Malachi Mail tray")
        {
            // Hidden (no WS_VISIBLE), top-level (TaskbarCreated), no taskbar
            // button (WS_EX_TOOLWINDOW).
            window = PInvoke.CreateWindowEx(
                WINDOW_EX_STYLE.WS_EX_TOOLWINDOW, cls, title, WINDOW_STYLE.WS_OVERLAPPED, 0, 0, 0, 0, HWND.Null, HMENU.Null, instance, null);
        }
        if (window.IsNull)
        {
            throw new InvalidOperationException($"the tray window was not created ({Marshal.GetLastPInvokeError()})");
        }
        Icons[(nint)window.Value] = this;
        (icon, ownsIcon) = LoadSmallIcon(iconFile ?? Environment.ProcessPath);
        Add();
    }

    /// <summary>The icon was clicked, or Enter or Space pressed on it.</summary>
    public event EventHandler? Activated;

    /// <summary>
    /// The icon's context menu was asked for (a right click, Shift+F10 or
    /// the menu key), at the given screen point; call <see cref="ShowMenu"/>.
    /// </summary>
    public event EventHandler<TrayMenuRequestedEventArgs>? MenuRequested;

    /// <summary>The hidden window that owns the icon.</summary>
    public nint WindowHandle => (nint)window.Value;

    /// <summary>
    /// Shows <paramref name="items"/> as a native menu at the screen point
    /// (<paramref name="x"/>, <paramref name="y"/>), with the default item
    /// in bold, and returns the chosen command once the menu has closed
    /// (<see cref="TrayCommand.None"/> when it was dismissed).
    /// </summary>
    public TrayCommand ShowMenu(int x, int y, IReadOnlyList<TrayMenuItem> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        ObjectDisposedException.ThrowIf(disposed, this);
        var menu = PInvoke.CreatePopupMenu();
        if (menu.IsNull)
        {
            return TrayCommand.None;
        }
        try
        {
            foreach (var item in items)
            {
                if (item.IsSeparator)
                {
                    PInvoke.AppendMenu(menu, MENU_ITEM_FLAGS.MF_SEPARATOR, 0, default(PCWSTR));
                    continue;
                }
                fixed (char* text = item.Text)
                {
                    PInvoke.AppendMenu(menu, MENU_ITEM_FLAGS.MF_STRING, (nuint)item.Command, text);
                }
                if (item.IsDefault)
                {
                    PInvoke.SetMenuDefaultItem(menu, (uint)item.Command, 0);
                }
            }
            // KB135788: without the foreground the menu does not close on a
            // click elsewhere. The click on the icon granted the right.
            PInvoke.SetForegroundWindow(window);
            var chosen = PInvoke.TrackPopupMenuEx(menu, TpmReturnCmd | TpmRightButton | TpmBottomAlign, x, y, window, null).Value;
            // The same article: a message after the menu, so that the next
            // one opens properly.
            PInvoke.PostMessage(window, PInvoke.WM_NULL, 0, 0);
            return Enum.IsDefined((TrayCommand)chosen) ? (TrayCommand)chosen : TrayCommand.None;
        }
        finally
        {
            PInvoke.DestroyMenu(menu);
        }
    }

    /// <summary>
    /// Whether the notification area shows the icon (or the overflow chevron
    /// that holds it): <c>Shell_NotifyIconGetRect</c> finds it.
    /// </summary>
    public bool IsShown
    {
        get
        {
            if (disposed)
            {
                return false;
            }
            var id = new NOTIFYICONIDENTIFIER { cbSize = (uint)sizeof(NOTIFYICONIDENTIFIER), hWnd = window, uID = IconId };
            IconRect rect;
            return Shell_NotifyIconGetRect(&id, &rect) >= 0;
        }
    }

    /// <summary>
    /// Removes the icon from the notification area, destroys the window and
    /// frees the icon. Call it on the thread that created the icon.
    /// </summary>
    public void Dispose()
    {
        if (disposed)
        {
            return;
        }
        disposed = true;
        Remove();
        Icons.TryRemove((nint)window.Value, out _);
        if (!PInvoke.DestroyWindow(window))
        {
            LogNotDestroyed(logger, Marshal.GetLastPInvokeError());
        }
        if (ownsIcon)
        {
            PInvoke.DestroyIcon(icon);
        }
    }

    /// <summary>Takes the icon out of the notification area, as an Explorer restart does (tests).</summary>
    internal bool Remove()
    {
        var data = Data();
        return Shell_NotifyIcon(NimDelete, &data);
    }

    // NIM_ADD, or NIM_MODIFY for an icon that is still there (TaskbarCreated
    // is also broadcast when the taskbar only changed), then version 4.
    private void Add()
    {
        var data = Data();
        if (!Shell_NotifyIcon(NimAdd, &data) && !Shell_NotifyIcon(NimModify, &data))
        {
            LogNotAdded(logger);
            return;
        }
        data.uVersion = PInvoke.NOTIFYICON_VERSION_4;
        if (!Shell_NotifyIcon(NimSetVersion, &data))
        {
            LogNoVersion(logger);
        }
    }

    private NOTIFYICONDATAW Data()
    {
        var data = new NOTIFYICONDATAW
        {
            cbSize = (uint)sizeof(NOTIFYICONDATAW),
            hWnd = window,
            uID = IconId,
            uFlags = NifMessage | NifIcon | NifTip | NifShowTip,
            uCallbackMessage = CallbackMessage,
            hIcon = icon,
        };
        tooltip.AsSpan().CopyTo(new Span<char>(data.szTip, TipLength - 1));
        return data;
    }

    // The small icon of file, or the shared application icon (never
    // destroyed) when the file has none.
    private static (HICON Icon, bool Owned) LoadSmallIcon(string? file)
    {
        if (!string.IsNullOrEmpty(file))
        {
            HICON small = default;
            fixed (char* path = file)
            {
                if (PInvoke.ExtractIconEx(path, 0, null, &small, 1) > 0 && !small.IsNull)
                {
                    return (small, true);
                }
            }
        }
        return (PInvoke.LoadIcon(HINSTANCE.Null, PInvoke.IDI_APPLICATION), false);
    }

    private static HINSTANCE RegisterWindowClass()
    {
        var module = PInvoke.GetModuleHandle(default(PCWSTR));
        var instance = new HINSTANCE(module.Value);
        fixed (char* cls = ClassName)
        {
            var wc = new WNDCLASSEXW
            {
                // sizeof would not compile: the procedure is a managed delegate (CS8500).
                cbSize = (uint)Marshal.SizeOf<WNDCLASSEXW>(),
                lpfnWndProc = Procedure,
                hInstance = instance,
                lpszClassName = cls,
            };
            // ERROR_CLASS_ALREADY_EXISTS: registered by this module before
            // (the class lives as long as the process); its procedure is this one.
            if (PInvoke.RegisterClassEx(wc) == 0 && Marshal.GetLastPInvokeError() is var error && error != ClassAlreadyExists)
            {
                throw new InvalidOperationException($"the tray window class was not registered ({error})");
            }
        }
        return instance;
    }

    [SuppressMessage("Design", "CA1031", Justification = "Nothing may be thrown into the window procedure's native caller.")]
    private static LRESULT WindowProcedure(HWND hwnd, uint message, WPARAM wParam, LPARAM lParam)
    {
        if (!Icons.TryGetValue((nint)hwnd.Value, out var tray))
        {
            return PInvoke.DefWindowProc(hwnd, message, wParam, lParam);
        }
        try
        {
            if (message == CallbackMessage)
            {
                // Version 4: LOWORD(lParam) is the event, wParam the anchor
                // as two signed 16-bit screen coordinates.
                var ev = (uint)(lParam.Value & 0xFFFF);
                var x = (short)(wParam.Value & 0xFFFF);
                var y = (short)((wParam.Value >> 16) & 0xFFFF);
                if (ev is PInvoke.NIN_SELECT or NinKeySelect)
                {
                    tray.Activated?.Invoke(tray, EventArgs.Empty);
                }
                else if (ev == PInvoke.WM_CONTEXTMENU)
                {
                    tray.MenuRequested?.Invoke(tray, new TrayMenuRequestedEventArgs(x, y));
                }
                return (LRESULT)0;
            }
            if (message == TaskbarCreatedMessage)
            {
                // Always: after an Explorer restart the icon is gone even
                // if nothing here noticed.
                tray.Add();
                return (LRESULT)0;
            }
        }
        catch (Exception e)
        {
            LogHandlerFailed(tray.logger, e);
            return (LRESULT)0;
        }
        return PInvoke.DefWindowProc(hwnd, message, wParam, lParam);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "tray: the icon was not added")]
    private static partial void LogNotAdded(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "tray: version 4 was refused")]
    private static partial void LogNoVersion(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "tray: the window was not destroyed ({Error})")]
    private static partial void LogNotDestroyed(ILogger logger, int error);

    [LoggerMessage(Level = LogLevel.Error, Message = "tray: a handler failed")]
    private static partial void LogHandlerFailed(ILogger logger, Exception error);
}

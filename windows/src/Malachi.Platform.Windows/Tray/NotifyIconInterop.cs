// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: Shell_NotifyIconW and Shell_NotifyIconGetRect with
// their structures, declared by hand because CsWin32 refuses them in an Any
// CPU library (PInvoke005: shellapi.h packs NOTIFYICONDATAW differently on
// 32-bit x86). The layouts below are those of the Windows SDK for 64-bit
// processes, the only ones the app ships (x64, ARM64); NotifyIconInteropTests
// checks their sizes, and TrayIcon refuses to run in a 32-bit process.

using System.Runtime.InteropServices;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.Platform.Windows.Tray;

/// <summary>The notification-area calls of shell32 for a 64-bit process.</summary>
internal static unsafe partial class NotifyIconInterop
{
    /// <summary>NIM_ADD.</summary>
    internal const uint NimAdd = 0;

    /// <summary>NIM_MODIFY.</summary>
    internal const uint NimModify = 1;

    /// <summary>NIM_DELETE.</summary>
    internal const uint NimDelete = 2;

    /// <summary>NIM_SETVERSION.</summary>
    internal const uint NimSetVersion = 4;

    /// <summary>NIF_MESSAGE: <see cref="NOTIFYICONDATAW.uCallbackMessage"/> is valid.</summary>
    internal const uint NifMessage = 0x1;

    /// <summary>NIF_ICON: <see cref="NOTIFYICONDATAW.hIcon"/> is valid.</summary>
    internal const uint NifIcon = 0x2;

    /// <summary>NIF_TIP: <see cref="NOTIFYICONDATAW.szTip"/> is valid.</summary>
    internal const uint NifTip = 0x4;

    /// <summary>NIF_SHOWTIP: version 4 shows the standard tooltip.</summary>
    internal const uint NifShowTip = 0x80;

    /// <summary>The characters of <see cref="NOTIFYICONDATAW.szTip"/>, its terminating null included.</summary>
    internal const int TipLength = 128;

    /// <summary>Adds, changes or removes an icon (<c>Shell_NotifyIconW</c>).</summary>
    [LibraryImport("shell32.dll", EntryPoint = "Shell_NotifyIconW")]
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [return: MarshalAs(UnmanagedType.Bool)]
    internal static partial bool Shell_NotifyIcon(uint message, NOTIFYICONDATAW* data);

    /// <summary>The screen rectangle of an icon, or of the overflow chevron that holds it (an HRESULT).</summary>
    [LibraryImport("shell32.dll")]
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    internal static partial int Shell_NotifyIconGetRect(NOTIFYICONIDENTIFIER* identifier, IconRect* iconLocation);

    /// <summary>NOTIFYICONDATAW as a 64-bit process lays it out (976 bytes).</summary>
    [StructLayout(LayoutKind.Sequential)]
    internal struct NOTIFYICONDATAW
    {
        internal uint cbSize;
        internal HWND hWnd;
        internal uint uID;
        internal uint uFlags;
        internal uint uCallbackMessage;
        internal HICON hIcon;
        internal fixed char szTip[TipLength];
        internal uint dwState;
        internal uint dwStateMask;
        internal fixed char szInfo[256];
        internal uint uVersion; // union with uTimeout
        internal fixed char szInfoTitle[64];
        internal uint dwInfoFlags;
        internal System.Guid guidItem;
        internal HICON hBalloonIcon;
    }

    /// <summary>
    /// RECT, declared here: the source generator of LibraryImport does not
    /// see the types CsWin32 generates.
    /// </summary>
    [StructLayout(LayoutKind.Sequential)]
    internal struct IconRect
    {
        internal int left;
        internal int top;
        internal int right;
        internal int bottom;
    }

    /// <summary>NOTIFYICONIDENTIFIER as a 64-bit process lays it out (40 bytes).</summary>
    [StructLayout(LayoutKind.Sequential)]
    internal struct NOTIFYICONIDENTIFIER
    {
        internal uint cbSize;
        internal HWND hWnd;
        internal uint uID;
        internal System.Guid guidItem;
    }
}

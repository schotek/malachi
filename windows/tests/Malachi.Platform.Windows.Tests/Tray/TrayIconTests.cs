// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of the notification-area icon (docs/windows-port.md §10, measured in
// APP-SPIKES §4): the hand-declared 64-bit interop against the Windows SDK's
// sizes; a real icon on a hidden window of the test's thread, added,
// removed as an Explorer restart removes it and re-added by TaskbarCreated,
// then disposed; its callback turned into Activated and MenuRequested with
// signed coordinates; a handler that throws kept out of the window
// procedure; the icon's image at the small-icon size of the taskbar's DPI,
// read from a file's icons or the system's application icon when it has
// none. The window procedure is driven with SendMessage from the same
// thread, so no message loop is needed. The native menu is modal and is
// checked by hand (docs/windows-port.md §10). Where no taskbar runs (a
// service session) the icon tests are skipped. Explorer remembers the icon
// of the test host under HKCU\Control Panel\NotifyIconSettings, one entry
// per executable path.

using System;
using System.Collections.Generic;
using System.IO;
using Malachi.Platform.Windows.Tray;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Graphics.Gdi;
using Windows.Win32.UI.WindowsAndMessaging;
using Xunit;
using static Malachi.Platform.Windows.Tray.NotifyIconInterop;

namespace Malachi.Platform.Windows.Tests.Tray;

public sealed unsafe class TrayIconTests
{
    [Fact]
    public void TheInteropHasTheSdksLayoutFor64BitProcesses()
    {
        // The sizes and offsets shellapi.h gives a 64-bit process.
        Assert.True(Environment.Is64BitProcess);
        Assert.Equal(976, sizeof(NOTIFYICONDATAW));
        Assert.Equal(40, sizeof(NOTIFYICONIDENTIFIER));
        Assert.Equal(16, sizeof(IconRect));
        var d = default(NOTIFYICONDATAW);
        var start = (byte*)&d;
        Assert.Equal(8, (int)((byte*)&d.hWnd - start));
        Assert.Equal(16, (int)((byte*)&d.uID - start));
        Assert.Equal(24, (int)((byte*)&d.uCallbackMessage - start));
        Assert.Equal(32, (int)((byte*)&d.hIcon - start));
        Assert.Equal(40, (int)((byte*)d.szTip - start));
        Assert.Equal(296, (int)((byte*)&d.dwState - start));
        Assert.Equal(304, (int)((byte*)d.szInfo - start));
        Assert.Equal(816, (int)((byte*)&d.uVersion - start));
        Assert.Equal(820, (int)((byte*)d.szInfoTitle - start));
        Assert.Equal(948, (int)((byte*)&d.dwInfoFlags - start));
        Assert.Equal(952, (int)((byte*)&d.guidItem - start));
        Assert.Equal(968, (int)((byte*)&d.hBalloonIcon - start));
        var id = default(NOTIFYICONIDENTIFIER);
        var idStart = (byte*)&id;
        Assert.Equal(8, (int)((byte*)&id.hWnd - idStart));
        Assert.Equal(16, (int)((byte*)&id.uID - idStart));
        Assert.Equal(20, (int)((byte*)&id.guidItem - idStart));
    }

    [Fact]
    public void IsAddedReAddedAfterAnExplorerRestartAndRemoved()
    {
        SkipWithoutTaskbar();
        TrayIcon? icon = null;
        try
        {
            icon = new TrayIcon("Malachi Mail tests");
            Assert.NotEqual(0, icon.WindowHandle);
            Assert.True(icon.IsShown);
            // An Explorer restart takes every icon away...
            Assert.True(icon.Remove());
            Assert.False(icon.IsShown);
            // ...and broadcasts TaskbarCreated, which puts it back.
            Send(icon, TrayIcon.TaskbarCreatedMessage, 0, 0);
            Assert.True(icon.IsShown);
            // Broadcast again while the icon is there (the taskbar only
            // changed): it stays.
            Send(icon, TrayIcon.TaskbarCreatedMessage, 0, 0);
            Assert.True(icon.IsShown);
        }
        finally
        {
            icon?.Dispose();
        }
        Assert.False(icon!.IsShown);
        Assert.False(PInvoke.IsWindow(new HWND((void*)icon.WindowHandle)));
        icon.Dispose();
    }

    [Fact]
    public void AClickActivatesAndTheMenuIsAskedForAtItsAnchor()
    {
        SkipWithoutTaskbar();
        using var icon = new TrayIcon("Malachi Mail tests");
        var activated = 0;
        var menus = new List<(int X, int Y)>();
        icon.Activated += (_, _) => activated++;
        icon.MenuRequested += (_, e) => menus.Add((e.X, e.Y));

        // Version 4: LOWORD(lParam) is the event, HIWORD the icon's id;
        // wParam the anchor.
        Send(icon, TrayIcon.CallbackMessage, Anchor(1700, 1040), (1 << 16) | PInvoke.NIN_SELECT);
        Send(icon, TrayIcon.CallbackMessage, Anchor(1700, 1040), (1 << 16) | TrayIcon.NinKeySelect);
        Assert.Equal(2, activated);

        // A monitor left of and above the primary one: negative coordinates.
        Send(icon, TrayIcon.CallbackMessage, Anchor(-1200, -30), (1 << 16) | PInvoke.WM_CONTEXTMENU);
        Send(icon, TrayIcon.CallbackMessage, Anchor(1745, 955), (1 << 16) | PInvoke.WM_CONTEXTMENU);
        Assert.Equal([(-1200, -30), (1745, 955)], menus);

        // Mouse moves and button messages are not clicks.
        Send(icon, TrayIcon.CallbackMessage, Anchor(1, 1), (1 << 16) | 0x0200); // WM_MOUSEMOVE
        Send(icon, TrayIcon.CallbackMessage, Anchor(1, 1), (1 << 16) | 0x0202); // WM_LBUTTONUP
        Assert.Equal(2, activated);
        Assert.Equal(2, menus.Count);
    }

    [Fact]
    public void AHandlerThatThrowsStaysOutOfTheWindowProcedure()
    {
        SkipWithoutTaskbar();
        using var icon = new TrayIcon("Malachi Mail tests");
        var after = 0;
        icon.Activated += (_, _) => throw new InvalidOperationException("handler broke");
        Send(icon, TrayIcon.CallbackMessage, Anchor(1, 1), (1 << 16) | PInvoke.NIN_SELECT);
        icon.MenuRequested += (_, _) => after++;
        Send(icon, TrayIcon.CallbackMessage, Anchor(1, 1), (1 << 16) | PInvoke.WM_CONTEXTMENU);
        Assert.Equal(1, after);
        Assert.True(icon.IsShown);
    }

    [Fact]
    public void SeveralIconsKeepTheirOwnWindows()
    {
        SkipWithoutTaskbar();
        using var one = new TrayIcon("one");
        using var two = new TrayIcon("two", Environment.ProcessPath);
        Assert.NotEqual(one.WindowHandle, two.WindowHandle);
        var got = new List<string>();
        one.Activated += (_, _) => got.Add("one");
        two.Activated += (_, _) => got.Add("two");
        Send(two, TrayIcon.CallbackMessage, 0, (1 << 16) | PInvoke.NIN_SELECT);
        Send(one, TrayIcon.CallbackMessage, 0, (1 << 16) | PInvoke.NIN_SELECT);
        Assert.Equal(["two", "one"], got);
    }

    [Fact]
    public void ADisposedIconShowsNoMenu()
    {
        SkipWithoutTaskbar();
        var icon = new TrayIcon("Malachi Mail tests", "C:\\no\\such\\file.exe");
        icon.Dispose();
        Assert.Throws<ObjectDisposedException>(() => icon.ShowMenu(0, 0, TrayMenu.Items()));
    }

    [Theory]
    [InlineData(96u, 16)]
    [InlineData(120u, 20)]
    [InlineData(144u, 24)]
    [InlineData(192u, 32)]
    public void TheSmallIconSizeFollowsTheDpi(uint dpi, int size)
    {
        Assert.Equal(size, TrayIcon.SmallIconSize(dpi));
    }

    [Theory]
    [InlineData(16)]
    [InlineData(20)]
    [InlineData(24)]
    [InlineData(32)]
    public void TheIconIsReadAtTheSizeAskedFor(int size)
    {
        var (icon, owned) = TrayIcon.LoadSmallIcon(Path.Combine(Environment.SystemDirectory, "shell32.dll"), size);
        try
        {
            Assert.True(owned);
            Assert.Equal(size, IconWidth(icon));
        }
        finally
        {
            if (owned)
            {
                PInvoke.DestroyIcon(icon);
            }
        }
    }

    [Theory]
    [InlineData(@"C:\no\such\file.exe")]
    [InlineData(null)]
    public void WithoutAnIconTheSystemsApplicationIconIsSharedNotOwned(string? file)
    {
        var (icon, owned) = TrayIcon.LoadSmallIcon(file, 16);
        Assert.False(owned);
        Assert.False(icon.IsNull);
        Assert.Equal(PInvoke.LoadIcon(HINSTANCE.Null, PInvoke.IDI_APPLICATION), icon);
    }

    [Fact]
    public void TheTaskbarsDpiIsRead()
    {
        SkipWithoutTaskbar();
        Assert.InRange(TrayIcon.TaskbarDpi(), 96u, 96u * 5);
    }

    private static int IconWidth(HICON icon)
    {
        ICONINFO info;
        Assert.True(PInvoke.GetIconInfo(icon, &info));
        try
        {
            var bitmap = info.hbmColor.IsNull ? info.hbmMask : info.hbmColor;
            BITMAP bm;
            Assert.NotEqual(0, PInvoke.GetObject(bitmap, sizeof(BITMAP), &bm));
            return bm.bmWidth;
        }
        finally
        {
            PInvoke.DeleteObject(info.hbmColor);
            PInvoke.DeleteObject(info.hbmMask);
        }
    }

    private static void SkipWithoutTaskbar() =>
        Assert.SkipWhen(PInvoke.FindWindow("Shell_TrayWnd", null).IsNull, "no taskbar in this session");

    private static nuint Anchor(int x, int y) => (nuint)(uint)(((y & 0xFFFF) << 16) | (x & 0xFFFF));

    private static void Send(TrayIcon icon, uint message, nuint wParam, uint lParam) =>
        PInvoke.SendMessage(new HWND((void*)icon.WindowHandle), message, wParam, (nint)lParam);
}

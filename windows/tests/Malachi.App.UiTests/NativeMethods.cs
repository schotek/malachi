// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The few Win32 calls the UI tests need beside UI Automation: the top-level
// windows of a process (a WinUI window the app owns, the wizard, is not a
// child of the desktop in the UIA tree), and the processes a process
// started (the app's daemon), one key pressed for a test that needs the
// keyboard (the board panel's Escape, Uia.PressKey), a real double click
// for what only the gesture shows (Uia.DoubleClick: the calendar's New
// Event, a message row's window; DoubleTapped comes with the second press,
// its release follows), and where a window lies (above another, on a
// display's work area).

using System;
using System.Runtime.InteropServices;

namespace Malachi.App.UiTests;

/// <summary>P/Invoke declarations.</summary>
internal static partial class NativeMethods
{
    public const uint Th32csSnapProcess = 0x00000002;

    [LibraryImport("user32.dll", EntryPoint = "FindWindowExW", StringMarshalling = StringMarshalling.Utf16)]
    public static partial nint FindWindowEx(nint parent, nint childAfter, string? className, string? windowName);

    [LibraryImport("user32.dll")]
    public static partial uint GetWindowThreadProcessId(nint window, out uint processId);

    [LibraryImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool IsWindowVisible(nint window);

    [LibraryImport("user32.dll")]
    public static partial nint GetForegroundWindow();

    /// <summary>KEYEVENTF_KEYUP.</summary>
    public const uint KeyEventKeyUp = 0x0002;

    [LibraryImport("user32.dll", EntryPoint = "keybd_event")]
    public static partial void KeybdEvent(byte key, byte scan, uint flags, nint extraInfo);

    /// <summary>MOUSEEVENTF_LEFTDOWN.</summary>
    public const uint MouseEventLeftDown = 0x0002;

    /// <summary>MOUSEEVENTF_LEFTUP.</summary>
    public const uint MouseEventLeftUp = 0x0004;

    [LibraryImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool SetCursorPos(int x, int y);

    [LibraryImport("user32.dll", EntryPoint = "mouse_event")]
    public static partial void MouseEvent(uint flags, int dx, int dy, int data, nint extraInfo);

    /// <summary>GW_HWNDPREV: the window above in the Z order.</summary>
    public const uint WindowAbove = 3;

    [LibraryImport("user32.dll")]
    public static partial nint GetWindow(nint window, uint command);

    /// <summary>MONITOR_DEFAULTTONEAREST.</summary>
    public const uint MonitorDefaultToNearest = 2;

    [LibraryImport("user32.dll")]
    public static partial nint MonitorFromWindow(nint window, uint flags);

    [LibraryImport("user32.dll", EntryPoint = "GetMonitorInfoW")]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool GetMonitorInfo(nint monitor, ref MonitorInfo info);

    /// <summary>Whether <paramref name="window"/> lies above <paramref name="other"/> in the Z order.</summary>
    public static bool IsAbove(nint window, nint other)
    {
        for (var at = GetWindow(other, WindowAbove); at != 0; at = GetWindow(at, WindowAbove))
        {
            if (at == window)
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>The work area of the display <paramref name="window"/> is on (the taskbar left out).</summary>
    public static Rect WorkAreaOf(nint window)
    {
        var info = new MonitorInfo { Size = (uint)Marshal.SizeOf<MonitorInfo>() };
        if (!GetMonitorInfo(MonitorFromWindow(window, MonitorDefaultToNearest), ref info))
        {
            throw new InvalidOperationException("no display for the window");
        }
        return info.Work;
    }

    [LibraryImport("user32.dll", EntryPoint = "GetClassNameW", StringMarshalling = StringMarshalling.Utf16)]
    public static unsafe partial int GetClassName(nint window, char* className, int maxCount);

    [LibraryImport("kernel32.dll", SetLastError = true)]
    public static partial nint CreateToolhelp32Snapshot(uint flags, uint processId);

    [LibraryImport("kernel32.dll", EntryPoint = "Process32FirstW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool Process32First(nint snapshot, ref ProcessEntry entry);

    [LibraryImport("kernel32.dll", EntryPoint = "Process32NextW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool Process32Next(nint snapshot, ref ProcessEntry entry);

    [LibraryImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static partial bool CloseHandle(nint handle);

    /// <summary>The class name of <paramref name="window"/>.</summary>
    public static unsafe string ClassOf(nint window)
    {
        var buffer = stackalloc char[256];
        var length = GetClassName(window, buffer, 256);
        return length > 0 ? new string(buffer, 0, length) : "";
    }

    /// <summary>RECT.</summary>
    [StructLayout(LayoutKind.Sequential)]
    public struct Rect
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    /// <summary>MONITORINFO.</summary>
    [StructLayout(LayoutKind.Sequential)]
    public struct MonitorInfo
    {
        public uint Size;
        public Rect Monitor;
        public Rect Work;
        public uint Flags;
    }

    /// <summary>PROCESSENTRY32W.</summary>
    [StructLayout(LayoutKind.Sequential)]
    public unsafe struct ProcessEntry
    {
        public uint Size;
        public uint Usage;
        public uint ProcessId;
        public nint DefaultHeapId;
        public uint ModuleId;
        public uint Threads;
        public uint ParentProcessId;
        public int PriorityClassBase;
        public uint Flags;

        // UTF-16 code units (char would not be blittable for LibraryImport).
        public fixed ushort ExeFile[260];

        /// <summary>The executable's file name.</summary>
        public string ExeName()
        {
            fixed (ushort* name = ExeFile)
            {
                return new string((char*)name);
            }
        }
    }
}

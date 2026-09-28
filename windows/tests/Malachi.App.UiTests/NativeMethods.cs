// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The few Win32 calls the UI tests need beside UI Automation: the top-level
// windows of a process (a WinUI window the app owns, the wizard, is not a
// child of the desktop in the UIA tree), and the processes a process
// started (the app's daemon).

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

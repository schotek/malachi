// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The processes of a session: the daemon the app started is the app's child
// named malachid.exe (a toolhelp snapshot: the daemon of another app, or
// the user's, is never taken for it).

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.App.UiTests;

/// <summary>The processes of this session: who started whom.</summary>
internal static class ProcessTree
{
    /// <summary>The ids of the running processes named <paramref name="exeName"/> that <paramref name="parent"/> started.</summary>
    public static List<int> Children(int parent, string exeName)
    {
        var found = new List<int>();
        var snapshot = NativeMethods.CreateToolhelp32Snapshot(NativeMethods.Th32csSnapProcess, 0);
        if (snapshot == -1)
        {
            return found;
        }
        try
        {
            var entry = new NativeMethods.ProcessEntry { Size = (uint)System.Runtime.InteropServices.Marshal.SizeOf<NativeMethods.ProcessEntry>() };
            for (var ok = NativeMethods.Process32First(snapshot, ref entry); ok; ok = NativeMethods.Process32Next(snapshot, ref entry))
            {
                if (entry.ParentProcessId == parent && string.Equals(entry.ExeName(), exeName, StringComparison.OrdinalIgnoreCase))
                {
                    found.Add((int)entry.ProcessId);
                }
            }
        }
        finally
        {
            NativeMethods.CloseHandle(snapshot);
        }
        return found.Where(id => id != parent).ToList();
    }
}

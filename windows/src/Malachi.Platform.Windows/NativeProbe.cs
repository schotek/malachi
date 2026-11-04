// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): proves that CsWin32 generates what NativeMethods.txt
// lists and that the call works. It goes when the first real service lands
// (phase C3).

using Windows.Win32;

namespace Malachi.Platform.Windows;

/// <summary>The smallest Win32 call through the generated bindings.</summary>
public static class NativeProbe
{
    /// <summary>The process ID, from <c>GetCurrentProcessId</c>.</summary>
    public static uint CurrentProcessId => PInvoke.GetCurrentProcessId();
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the console control events the app's handler gets when it
// is attached to the terminal it was started from (docs/windows-port.md §5
// Console; the SIGINT and SIGTERM that reach ui/main.go and the macOS app
// from a terminal). The namespace is not Console, which would hide
// System.Console in every Malachi.Platform.Windows namespace.

namespace Malachi.Platform.Windows.Consoles;

/// <summary>A console control event (CTRL_*_EVENT).</summary>
public enum ConsoleControl
{
    /// <summary>Ctrl+C in the terminal (CTRL_C_EVENT).</summary>
    Interrupt,

    /// <summary>Ctrl+Break (CTRL_BREAK_EVENT).</summary>
    Break,

    /// <summary>
    /// The terminal (its tab or window) is closing (CTRL_CLOSE_EVENT): the
    /// process ends when the handler returns, at the latest about five
    /// seconds later. The daemon, on the same console, gets it too and
    /// stops by itself.
    /// </summary>
    Close,

    /// <summary>The user logs off (CTRL_LOGOFF_EVENT; not delivered to a process with windows).</summary>
    Logoff,

    /// <summary>The system shuts down (CTRL_SHUTDOWN_EVENT; not delivered to a process with windows).</summary>
    Shutdown,
}

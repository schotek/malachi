// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §0, §10): why the app quits,
// which decides whether QuitSequence may ask anything.

namespace Malachi.Core.Presentation;

/// <summary>Why the app quits.</summary>
public enum QuitReason
{
    /// <summary>
    /// Quit (Ctrl+Q, the menu, the notification-area icon, Ctrl+C in the
    /// terminal, the last window closed): the drafts are saved and a window
    /// whose save failed asks its question, which may abandon the Quit.
    /// </summary>
    User,

    /// <summary>
    /// The session or the terminal ends (WM_ENDSESSION, CTRL_CLOSE): the
    /// system gives the app seconds, so nothing is saved or asked; the
    /// daemon is stopped and the app exits.
    /// </summary>
    SessionEnd,
}

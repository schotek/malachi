// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): which keys a window
// listens to depends on what it is, as each GTK window has its own action
// group (win.*, msg.*, compose.*) besides the application's.

namespace Malachi.Core.Presentation;

/// <summary>The kind of a window, for its keyboard shortcuts.</summary>
public enum WindowKind
{
    /// <summary>The main window (window.blp).</summary>
    Main,

    /// <summary>A message in its own window (message_window.blp).</summary>
    Message,

    /// <summary>An attached message's window (embedded_window.blp).</summary>
    Embedded,

    /// <summary>A compose window (compose.blp).</summary>
    Compose,

    /// <summary>The Preferences window (preferences.blp).</summary>
    Preferences,

    /// <summary>The account wizard (account_wizard.blp).</summary>
    Wizard,

    /// <summary>Any other window (the attachment previewer): the application's keys only.</summary>
    Other,
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1): what RendererRecovery tells a
// view to do about a failed process.

namespace Malachi.Core.Presentation;

/// <summary>A web view's answer to a failed process.</summary>
public enum RecoveryAction
{
    /// <summary>Nothing now (a first report of a hang; a dead renderer with no document).</summary>
    None,

    /// <summary>Show the document again in the same control (a new renderer starts with it).</summary>
    Reload,

    /// <summary>A new control, with nothing to show again.</summary>
    Replace,

    /// <summary>A new control, then the document again.</summary>
    ReplaceAndReload,

    /// <summary>Drop the document for good and say the view is unavailable.</summary>
    GiveUp,

    /// <summary>A new control without the document, and say the view is unavailable.</summary>
    ReplaceAndGiveUp,
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the two Adw.Breakpoints of ui/data/ui/window.blp (max-width 900sp
// collapses outer_split, max-width 600sp inner_split as well); macOS has
// the same classes in MainSplitViewController.swift (widthClass 0, 1, 2).

namespace Malachi.Core.Presentation;

/// <summary>How the main window's panes are laid out for its width (<see cref="PaneLayout"/>).</summary>
public enum PaneMode
{
    /// <summary>Wider than 900: sidebar, list and message side by side.</summary>
    Wide,

    /// <summary>
    /// 900 or less (window.blp's first breakpoint): the sidebar folds away,
    /// an overlay opened from the title bar's pane button.
    /// </summary>
    Medium,

    /// <summary>
    /// 600 or less (the second breakpoint): list and message are one stack
    /// as well, with the title bar's back button.
    /// </summary>
    Narrow,
}

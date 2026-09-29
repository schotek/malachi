// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the layout of ui/data/ui/window.blp (outer_split: the sidebar at
// 200 to 320; inner_split: the list at 280 to 460; the breakpoints at 900sp
// and 600sp; assistant_split: the assistant panel, 0.28 of the width at 280
// to 480, an overlay at 1180sp or less) and of window.go's navigation between its pages
// (outerSplit.SetShowContent after a folder was chosen, innerSplit
// .SetShowContent after a message was, search.go startSearch back to the
// list); macOS keeps the same in AppKit (MainSplitViewController.swift:
// widthClass, the minimums, the pane widths kept only from a wide,
// uncollapsed layout), Windows in Core (docs/windows-port.md §7.4). The
// Windows form (§11.1, windows/README.md): at 900 or less the sidebar is an
// overlay opened from the title bar's pane button, at 600 or less list and
// message are one stack with the title bar's back button, as GTK's
// collapsed split views navigate. A folder chosen in the overlay closes it
// and shows the folder's list (GTK shows the content page, which in a
// stack is whatever it showed last, an empty message page after a new
// folder cleared the selection).
//
// The widths are the gschema's folder-pane-width and message-list-width
// (GTK declares them and never writes them), clamped to window.blp's
// minimums and maximums; a window too narrow for the stored widths takes
// width from the list first, as macOS's holding priorities do, and the
// message pane keeps at least 300.

using System;

namespace Malachi.Core.Presentation;

/// <summary>The main window's panes for its width, and where the narrow layouts navigate.</summary>
public sealed class PaneLayout
{
    /// <summary>window.blp's first breakpoint: at this width or less the sidebar folds away.</summary>
    public const double SidebarBreakpoint = 900;

    /// <summary>window.blp's second breakpoint: at this width or less list and message are one stack.</summary>
    public const double ListBreakpoint = 600;

    /// <summary>outer_split min-sidebar-width.</summary>
    public const int SidebarMinimum = 200;

    /// <summary>outer_split max-sidebar-width.</summary>
    public const int SidebarMaximum = 320;

    /// <summary>inner_split min-sidebar-width.</summary>
    public const int ListMinimum = 280;

    /// <summary>inner_split max-sidebar-width.</summary>
    public const int ListMaximum = 460;

    /// <summary>The message pane's minimum (MainSplitViewController: 300, the rest of the window).</summary>
    public const int MessageMinimum = 300;

    /// <summary>
    /// window.blp's breakpoint of the assistant panel (max-width: 1180sp): at
    /// this width or less the panel only overlays the panes, wider it is a
    /// pane of its own on the right (assistant_split).
    /// </summary>
    public const double AssistantBreakpoint = 1180;

    /// <summary>assistant_split min-sidebar-width.</summary>
    public const int AssistantMinimum = 280;

    /// <summary>assistant_split max-sidebar-width.</summary>
    public const int AssistantMaximum = 480;

    /// <summary>assistant_split sidebar-width-fraction.</summary>
    public const double AssistantFraction = 0.28;

    /// <summary>The layout for the window's width (wide until the first <see cref="Resize"/>).</summary>
    public PaneMode Mode { get; private set; } = PaneMode.Wide;

    /// <summary>The sidebar's overlay is open (never in the wide layout).</summary>
    public bool SidebarOpen { get; private set; }

    /// <summary>The stack of the narrow layout shows the message (GTK inner_split's show-content).</summary>
    public bool ShowingMessage { get; private set; }

    /// <summary>The sidebar is a pane beside the list.</summary>
    public bool SidebarInline => Mode == PaneMode.Wide;

    /// <summary>The sidebar is shown over the other panes.</summary>
    public bool SidebarOverlay => Mode != PaneMode.Wide && SidebarOpen;

    /// <summary>The list pane is shown.</summary>
    public bool ListVisible => Mode != PaneMode.Narrow || !ShowingMessage;

    /// <summary>The message pane is shown.</summary>
    public bool MessageVisible => Mode != PaneMode.Narrow || ShowingMessage;

    /// <summary>The title bar's back button: the message of the narrow stack is shown.</summary>
    public bool BackVisible => Mode == PaneMode.Narrow && ShowingMessage;

    /// <summary>The title bar's pane button, which opens the folded sidebar.</summary>
    public bool PaneToggleVisible => Mode != PaneMode.Wide;

    /// <summary>
    /// Whether the widths the user drags are kept (folder-pane-width,
    /// message-list-width): only from the wide layout, where nothing is
    /// folded (MainSplitViewController.rememberPaneWidths).
    /// </summary>
    public bool KeepsWidths => Mode == PaneMode.Wide;

    /// <summary>The layout for a window <paramref name="width"/> effective pixels wide (window.blp's max-width conditions).</summary>
    public static PaneMode ModeFor(double width) =>
        width <= ListBreakpoint ? PaneMode.Narrow : width <= SidebarBreakpoint ? PaneMode.Medium : PaneMode.Wide;

    /// <summary>
    /// Whether the assistant panel of a window <paramref name="width"/> wide
    /// is a pane beside the others (wider than <see cref="AssistantBreakpoint"/>)
    /// rather than an overlay; the panes' own layout still follows the
    /// window's width, as window.blp's breakpoints do.
    /// </summary>
    public static bool AssistantInline(double width) => width > AssistantBreakpoint;

    /// <summary>
    /// The assistant panel's width in a window <paramref name="width"/> wide
    /// (Adw.OverlaySplitView's sidebar: the fraction of the width within its
    /// range, and never wider than the window).
    /// </summary>
    public static double AssistantWidth(double width) =>
        Math.Max(0, Math.Min(Math.Clamp(width * AssistantFraction, AssistantMinimum, AssistantMaximum), width));

    /// <summary>A stored sidebar width within outer_split's range (a width not yet stored, 0, is the minimum).</summary>
    public static int ClampSidebar(int width) => Math.Clamp(width, SidebarMinimum, SidebarMaximum);

    /// <summary>A stored list width within inner_split's range.</summary>
    public static int ClampList(int width) => Math.Clamp(width, ListMinimum, ListMaximum);

    /// <summary>
    /// The widths for a window <paramref name="window"/> wide in
    /// <paramref name="mode"/>, from the stored ones: each clamped to its
    /// range, the list narrowed (not below its minimum) so that the message
    /// keeps <see cref="MessageMinimum"/>. The overlay of a folded sidebar
    /// has the sidebar's width; the narrow stack gives the list the whole
    /// window.
    /// </summary>
    public static PaneWidths Widths(PaneMode mode, double window, int sidebar, int list)
    {
        var s = ClampSidebar(sidebar);
        switch (mode)
        {
            case PaneMode.Narrow:
                return new PaneWidths(s, Math.Max(window, 0));
            case PaneMode.Medium:
                return new PaneWidths(s, Math.Max(Math.Min(ClampList(list), window - MessageMinimum), ListMinimum));
            default:
                return new PaneWidths(s, Math.Max(Math.Min(ClampList(list), window - MessageMinimum - s), ListMinimum));
        }
    }

    /// <summary>
    /// The window is <paramref name="width"/> wide now; true when that
    /// changed the layout. The wide layout has no overlay to keep open;
    /// the narrow stack keeps the page it showed, as a split view keeps
    /// show-content.
    /// </summary>
    public bool Resize(double width)
    {
        var mode = ModeFor(width);
        if (mode == Mode)
        {
            return false;
        }
        Mode = mode;
        if (mode == PaneMode.Wide)
        {
            SidebarOpen = false;
        }
        return true;
    }

    /// <summary>The title bar's pane button: opens or closes the overlay (nothing in the wide layout).</summary>
    public void ToggleSidebar()
    {
        if (Mode != PaneMode.Wide)
        {
            SidebarOpen = !SidebarOpen;
        }
    }

    /// <summary>The overlay went away (a click outside it, Escape).</summary>
    public void CloseSidebar() => SidebarOpen = false;

    /// <summary>The user chose a folder (window.go: outerSplit.SetShowContent): the overlay closes, the stack shows the list.</summary>
    public void FolderChosen()
    {
        SidebarOpen = false;
        ShowingMessage = false;
    }

    /// <summary>The user chose a message (window.go onMessageRowSelected: innerSplit.SetShowContent).</summary>
    public void MessageChosen() => ShowingMessage = true;

    /// <summary>Back, or a search starting (search.go startSearch): the stack shows the list, the overlay closes.</summary>
    public void ShowList()
    {
        SidebarOpen = false;
        ShowingMessage = false;
    }
}

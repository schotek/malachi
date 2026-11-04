// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: a UserControl has no automation peer of its own, so
// the calendar's regions (the bar, the sidebar, the mini month, the Week,
// Month and Agenda views) would not be elements a screen reader or the UI
// smoke tests could find by their AutomationId. Each region names itself
// with this peer (macOS: the views are accessibility groups of AppKit).

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;

namespace Malachi.App.Calendars;

/// <summary>The automation peer of a region of the calendar page: a pane with its AutomationId and name.</summary>
internal sealed partial class CalendarRegionPeer(FrameworkElement owner, string className) : FrameworkElementAutomationPeer(owner)
{
    /// <inheritdoc/>
    protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Pane;

    /// <inheritdoc/>
    protected override string GetClassNameCore() => className;

    /// <inheritdoc/>
    protected override bool IsControlElementCore() => true;
}

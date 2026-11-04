// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: a UserControl has no automation peer of its own, so
// the conversation's layouts (the timeline, the chat) and the chat's
// bubbles would not be elements a screen reader or the UI smoke tests could
// find by their AutomationId (as Calendars/CalendarRegionPeer.cs says of
// the calendar's regions). Each names itself with this peer: a layout as a
// pane, a bubble as a group (macOS: accessibility groups of AppKit; GTK: a
// bubble is a Gtk.Frame, whose accessible role is a group).

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;

namespace Malachi.App.Reader.Conversation;

/// <summary>The automation peer of a part of the conversation view: its AutomationId, name and control type.</summary>
internal sealed partial class ConversationGroupPeer(FrameworkElement owner, string className, AutomationControlType controlType)
    : FrameworkElementAutomationPeer(owner)
{
    /// <inheritdoc/>
    protected override AutomationControlType GetAutomationControlTypeCore() => controlType;

    /// <inheritdoc/>
    protected override string GetClassNameCore() => className;

    /// <inheritdoc/>
    protected override bool IsControlElementCore() => true;
}

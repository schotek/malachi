// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition for UI Automation: GTK's attachment chip is a box that
// Orca reads with its label; a WinUI Border or panel has no automation peer,
// so the name given to it reaches no one and the chip's Remove button reads
// alone. A chip is this Grid instead, a named group (its name is the
// attachment's file name, AutomationProperties.Name): Narrator says which
// attachment the Remove button inside belongs to.

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>A panel that UI Automation sees as a named group.</summary>
public sealed partial class ChipGroup : Grid
{
    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new GroupPeer(this);

    private sealed partial class GroupPeer(FrameworkElement owner) : FrameworkElementAutomationPeer(owner)
    {
        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Group;

        protected override string GetClassNameCore() => nameof(ChipGroup);

        protected override bool IsControlElementCore() => true;
    }
}

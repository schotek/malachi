// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/CRM/CRMToolbarItem.swift; GTK: the
// crm_button of window.blp and message_window.blp (a Gtk.Button in state
// Save, a Gtk.MenuButton with crm_menu in state Saved; docs/eway.md §2.6,
// written in T7). The eWay-CRM button of a message's command row in its
// states (Core's CrmText.ButtonFor): hidden; Save to eWay-CRM, which opens
// the dialog (for several rows its label counts them, "Save 3 Messages to
// eWay-CRM…"); Saving, off with a ProgressRing over the symbol; In
// eWay-CRM, a DropDownButton with Open in eWay-CRM, Edit Links… and Remove
// from eWay-CRM (CrmText.SavedMenu). Icon-only buttons named by their
// labels, as the command row's others are.

using System;
using Malachi.App.Resources;
using Malachi.Core.Crm;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Crm;

/// <summary>The eWay-CRM button of a command row.</summary>
public sealed partial class CrmToolbarButton : UserControl
{
    private readonly Button save;
    private readonly ProgressRing ring;
    private readonly DropDownButton saved;

    /// <summary>A hidden button.</summary>
    public CrmToolbarButton()
    {
        IsTabStop = false;
        Visibility = Visibility.Collapsed;
        var style = Application.Current.Resources.TryGetValue("SubtleButtonStyle", out var s) ? s as Style : null;
        ring = new ProgressRing { Width = 16, Height = 16, IsActive = false, Visibility = Visibility.Collapsed };
        var face = new Grid();
        face.Children.Add(Icons.Create(CrmRules.Icon, 16));
        face.Children.Add(ring);
        save = new Button { Width = 36, Height = 32, Padding = new Thickness(0), Content = face };
        if (style is not null)
        {
            save.Style = style;
        }
        AutomationProperties.SetAutomationId(save, "CrmButton");
        save.Click += (_, _) => SaveClicked?.Invoke(this, EventArgs.Empty);

        var menu = new MenuFlyout { Placement = Microsoft.UI.Xaml.Controls.Primitives.FlyoutPlacementMode.BottomEdgeAlignedRight };
        foreach (var item in CrmText.SavedMenu())
        {
            var entry = new MenuFlyoutItem { Text = item.Label };
            AutomationProperties.SetAutomationId(entry, item.Id switch
            {
                CrmMenuId.Open => "CrmOpenItem",
                CrmMenuId.EditLinks => "CrmEditLinksItem",
                _ => "CrmRemoveItem",
            });
            if (item.Destructive)
            {
                entry.Style = (Style)Application.Current.Resources["CriticalMenuFlyoutItemStyle"];
            }
            var id = item.Id;
            entry.Click += (_, _) => MenuChosen?.Invoke(this, id);
            if (item.Destructive)
            {
                menu.Items.Add(new MenuFlyoutSeparator());
            }
            menu.Items.Add(entry);
        }
        saved = new DropDownButton
        {
            Height = 32,
            Padding = new Thickness(8, 0, 4, 0),
            Content = Icons.Create(CrmRules.Icon, 16),
            Flyout = menu,
            Visibility = Visibility.Collapsed,
        };
        AutomationProperties.SetAutomationId(saved, "CrmSavedButton");
        var both = new Grid();
        both.Children.Add(save);
        both.Children.Add(saved);
        Content = both;
    }

    /// <summary>Save to eWay-CRM was clicked: the window's CrmController opens the dialog.</summary>
    public event EventHandler? SaveClicked;

    /// <summary>An item of the saved message's menu was chosen.</summary>
    public event EventHandler<CrmMenuId>? MenuChosen;

    /// <summary>Shows <paramref name="b"/> (CrmController.Button).</summary>
    public void Show(CrmButton b)
    {
        ArgumentNullException.ThrowIfNull(b);
        Visibility = b.State == CrmButtonState.Hidden ? Visibility.Collapsed : Visibility.Visible;
        var menu = b.State == CrmButtonState.Saved;
        save.Visibility = menu ? Visibility.Collapsed : Visibility.Visible;
        saved.Visibility = menu ? Visibility.Visible : Visibility.Collapsed;
        var saving = b.State == CrmButtonState.Saving;
        ring.IsActive = saving;
        ring.Visibility = saving ? Visibility.Visible : Visibility.Collapsed;
        save.IsEnabled = b.Enabled;
        var target = menu ? (Control)saved : save;
        ToolTipService.SetToolTip(target, b.Tooltip);
        AutomationProperties.SetName(target, b.Label);
        AutomationProperties.SetHelpText(target, b.Tooltip);
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: see WizardPageHost.xaml. The page to show is the
// navigation's parameter; the host that goes away gives it up before the
// next host takes it, as a page can have one parent only.

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace Malachi.App.Wizard;

/// <summary>Shows one of the wizard's pages inside its Frame.</summary>
public sealed partial class WizardPageHost : Page
{
    /// <summary>An empty host; the Frame makes one per navigation.</summary>
    public WizardPageHost()
    {
        InitializeComponent();
    }

    /// <inheritdoc/>
    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        Content = e?.Parameter as UIElement;
    }

    /// <inheritdoc/>
    protected override void OnNavigatedFrom(NavigationEventArgs e)
    {
        Content = null;
        base.OnNavigatedFrom(e);
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the eWay-CRM dialog (Crm/CrmSaveDialog, the macOS
// sheet CRMDialogController) goes through the window's DialogScheduler like
// every other ContentDialog of the app, so it never meets another dialog
// on the same window.

using System;
using System.Threading.Tasks;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Shell;

/// <content>A dialog the caller makes.</content>
internal sealed partial class AlertService
{
    /// <inheritdoc/>
    public async Task<bool> ShowDialogAsync(Window? window, Func<ContentDialog> make) =>
        await ShowAsync(window, make) == ContentDialogResult.Primary;
}

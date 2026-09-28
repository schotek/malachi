// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/WizardWidgets.swift
// (WizardEntryField.hasError) and WizardStrings.swift (wizardLabel); GTK:
// the "error" style class the wizard puts on a row it flags
// (wizard.go onNext, onTest, askPassword, requirePassword), and the
// use-underline labels of account_wizard.blp. Small helpers of the pages.

using CommunityToolkit.WinUI.Controls;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Wizard;

/// <summary>The rows' error look and the buttons' mnemonics.</summary>
internal static class WizardRows
{
    /// <summary>
    /// Flags <paramref name="row"/> as the thing to fix (the GTK error
    /// class): its title in the critical colour of its window's theme
    /// (<c>WizardErrorCardStyle</c> of <paramref name="page"/>'s resources).
    /// Typing in it clears it.
    /// </summary>
    public static void SetError(FrameworkElement page, SettingsCard row, bool on)
    {
        if (on && page.Resources.TryGetValue("WizardErrorCardStyle", out var style) && style is Style error)
        {
            row.Style = error;
            return;
        }
        row.ClearValue(FrameworkElement.StyleProperty);
    }

    /// <summary>
    /// The label with its GTK mnemonic of one the controller hands out
    /// without it (<see cref="WizardController.WithoutMnemonic"/>): the
    /// translated <paramref name="candidates"/> it was made from, so that
    /// the button keeps its access key; the label itself when none matches.
    /// </summary>
    public static string WithMnemonic(string stripped, params string[] candidates)
    {
        foreach (var candidate in candidates)
        {
            if (WizardController.WithoutMnemonic(candidate) == stripped)
            {
                return candidate;
            }
        }
        return stripped;
    }
}

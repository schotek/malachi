// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Edit… sheet of macos/Sources/MalachiMail/Assistant/AssistantProposalView.swift
// (edit) and Preferences/AboutYouGroup.swift; GTK: window/assistant_panel_profile.go
// and preferences_profile.go (the Adw.AlertDialog with the proposal's text).
// A proposal the assistant made for the user's profile, changed before it is
// saved: its text in a field capped at the daemon's limit, Save (the
// default) and Cancel in the Windows order of a ContentDialog.

using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Assistants;

/// <summary>The dialogs of the user's profile.</summary>
internal static class ProfileDialogs
{
    /// <summary>
    /// The proposal's <paramref name="text"/> to change on
    /// <paramref name="window"/>; the edited text, or null when the user
    /// cancelled.
    /// </summary>
    public static async Task<string?> EditProposalAsync(IAlerts alerts, Window? window, string text, int limit)
    {
        var t = Assistant.ProfileTexts();
        var field = new TextBox
        {
            Text = text,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            MaxLength = limit > 0 ? limit : 0,
            MinWidth = 320,
            MinHeight = 80,
        };
        AutomationProperties.SetAutomationId(field, "ProfileProposalEdit");
        AutomationProperties.SetName(field, t.ProposalTitle);
        var saved = await alerts.ShowDialogAsync(window, () =>
        {
            var dialog = new ContentDialog
            {
                Title = t.ProposalTitle,
                Content = field,
                PrimaryButtonText = t.Save,
                CloseButtonText = Mnemonic.Strip(L10n.T("_Cancel")),
                DefaultButton = ContentDialogButton.Primary,
            };
            dialog.Opened += (_, _) =>
            {
                field.Focus(FocusState.Programmatic);
                field.SelectAll();
            };
            return dialog;
        });
        return saved ? field.Text : null;
    }

    /// <summary>The line naming a proposal's account: its name, else its address, else its id.</summary>
    public static string AccountLine(UserProfile profile, string? accountId)
    {
        if (string.IsNullOrEmpty(accountId))
        {
            return "";
        }
        var label = accountId;
        foreach (var n in profile.Accounts)
        {
            if (n.AccountId == accountId)
            {
                label = n.Name.Length > 0 ? n.Name : n.Email.Length > 0 ? n.Email : accountId;
                break;
            }
        }
        return L10n.Format(Assistant.ProfileTexts().AccountProposal, label);
    }
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first view of ui/internal/signature (KeyRows, KeyRow,
// SecurityPageTexts, ConfirmKeyQuestion, RemoveKeyQuestion); the macOS port
// is Preferences/SecurityPaneViewController.swift (written at the same
// time). Preferences → Security over Core's PgpKeysController: the stored
// OpenPGP keys, each a card with its first user ID, its fingerprint in
// monospace, where and when it came from, the pills (Confirmed or Not
// confirmed; Revoked or Expired when it can verify nothing new) and a menu:
// Confirm Key… (a question with the fingerprint to compare first) or Remove
// Confirmation, and Remove Key… (a destructive question). Import Key…
// opens the dialog for a pasted key or a file (ImportKeyDialog). The list is
// read whenever the page comes up and on notify.signaturesChanged (the
// daemon sends it after every change, this client's own included); a change's
// toast goes over the window. Every user ID, address and fingerprint is from
// a key and set as TextBlock.Text only.

using System;
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Controls;
using Malachi.App.Resources;
using Malachi.App.Shell;
using Malachi.App.Signatures;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Signatures;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Preferences;

/// <summary>Preferences → Security.</summary>
public sealed partial class SecurityPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly PgpKeysController keys;
    private readonly IDisposable subscription;
    private readonly SignatureSecurityPage texts = SignatureText.SecurityPageTexts();
    private bool closed;

    /// <summary>The page of <paramref name="window"/>, its toasts in <paramref name="toasts"/>.</summary>
    public SecurityPage(AppState state, Window window, IToasts toasts)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(toasts);
        this.state = state;
        this.window = window;
        InitializeComponent();
        GroupHeading.Text = texts.Group;
        GroupDescription.Text = texts.Description;
        ImportButton.Content = texts.Import;
        LoadFailedText.Text = texts.LoadFailed;
        EmptyText.Text = texts.Empty;
        keys = new PgpKeysController(state.Client, state.Logs.CreateLogger<PgpKeysController>());
        keys.Changed += (_, _) => Render();
        keys.ToastRequested += (_, text) => toasts.Show(text);
        subscription = state.Notifications.AddSignaturesChanged(keys.Refresh);
        Render();
        keys.Refresh();
    }

    /// <summary>The window closes: no answer is taken any more.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        subscription.Dispose();
        keys.Dispose();
    }

    /// <summary>The keys anew (the page came up).</summary>
    public void Refresh() => keys.Refresh();

    private void Render()
    {
        if (closed)
        {
            return;
        }
        LoadFailedText.Visibility = keys.LoadFailed ? Visibility.Visible : Visibility.Collapsed;
        var rows = keys.Rows(DateTimeOffset.UtcNow, t => Format.FormatDate(t, DateTimeOffset.UtcNow));
        EmptyText.Visibility = keys.Loaded && !keys.LoadFailed && rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        KeysHost.Children.Clear();
        foreach (var row in rows)
        {
            KeysHost.Children.Add(Card(row));
        }
    }

    // One key: the card of its row (KeyRowFor).
    private SettingsCard Card(SignatureKeyRow row)
    {
        var print = new TextBlock
        {
            Text = row.Subtitle,
            Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            FontFamily = (FontFamily)Application.Current.Resources["MonospaceFontFamily"],
            TextWrapping = TextWrapping.Wrap,
            IsTextSelectionEnabled = true,
        };
        var origin = new TextBlock
        {
            Text = row.Origin,
            Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            TextWrapping = TextWrapping.Wrap,
        };
        var description = new StackPanel { Spacing = 2 };
        description.Children.Add(print);
        description.Children.Add(origin);

        var trailing = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, VerticalAlignment = VerticalAlignment.Center };
        trailing.Children.Add(new IssuePill
        {
            Text = row.TrustPill,
            StatusStyle = row.Verified ? JiraStatusStyle.Done : JiraStatusStyle.Plain,
        });
        if (row.StatePill.Length > 0)
        {
            // Revoked in red, Expired in orange: the pills of a draft and an internal comment.
            trailing.Children.Add(new IssuePill
            {
                Text = row.StatePill,
                IsDraft = row.StatePill == SignatureText.KeyStatePill(revoked: true, expired: false),
                IsInternal = row.StatePill != SignatureText.KeyStatePill(revoked: true, expired: false),
            });
        }
        var menu = new MenuFlyout();
        foreach (var action in row.Actions)
        {
            var item = new MenuFlyoutItem { Text = action.Label };
            AutomationProperties.SetAutomationId(item, "PrefsSecurity" + action.Action);
            var fingerprint = row.Fingerprint;
            var shown = row.Subtitle;
            var which = action.Action;
            item.Click += (_, _) => _ = Act(which, fingerprint, shown);
            menu.Items.Add(item);
        }
        var more = new Button
        {
            Content = MoreIcon(),
            Flyout = menu,
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
        };
        AutomationProperties.SetAutomationId(more, "PrefsSecurityKeyMenu");
        AutomationProperties.SetName(more, row.Title);
        trailing.Children.Add(more);

        var card = new SettingsCard { Header = row.Title, Description = description, Content = trailing };
        AutomationProperties.SetAutomationId(card, "PrefsSecurityKey:" + row.Fingerprint);
        AutomationProperties.SetName(card, row.Title);
        return card;
    }

    private static FontIcon MoreIcon() => Icons.Create("view-more", 16);

    private async System.Threading.Tasks.Task Act(SignatureAction action, string fingerprint, string formatted)
    {
        switch (action)
        {
            case SignatureAction.ConfirmKey:
                var confirm = SignatureText.ConfirmKeyQuestion();
                if (await state.Alerts.ShowDialogAsync(window, () => SignatureDetailsPresenter.KeyQuestion(confirm, formatted)) && !closed)
                {
                    keys.Confirm(fingerprint);
                }
                break;
            case SignatureAction.UnconfirmKey:
                keys.Unconfirm(fingerprint);
                break;
            case SignatureAction.RemoveKey:
                var remove = SignatureText.RemoveKeyQuestion();
                if (await state.Alerts.ConfirmDestructiveAsync(window, remove.Heading, remove.Body, remove.Accept) && !closed)
                {
                    keys.Remove(fingerprint);
                }
                break;
        }
    }

    private async void OnImportClick(object sender, RoutedEventArgs e) =>
        await state.Alerts.ShowDialogAsync(window, () => new ImportKeyDialog(keys, window));
}

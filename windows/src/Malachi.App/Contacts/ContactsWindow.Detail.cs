// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Contacts/ContactsDetailViewController.swift
// (update, the card: the avatar, the name, "Job · Company", the source line,
// Read Only, the rows; Write Email with a menu for several addresses, Show
// Mail, Copy; the websites through Open This Link?); GTK:
// ui/internal/window/contacts_detail.go (buildDetail, detailActions,
// detailRow, writeEmail, copyText, openURL). The detail of the Contacts
// window: the CommandBar's actions over Core's ContactBook.ContactDetail,
// the card built in code with every text a TextBlock's Text (never markup),
// and the empty states. A phone number is only copied (no dialling), a
// website opens only http(s) (ContactBook.OpenUrl) and only after the
// app's "Open This Link?" has shown where it leads.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Runtime.InteropServices;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Windows.ApplicationModel.DataTransfer;

namespace Malachi.App.Contacts;

/// <summary>The detail of the Contacts window.</summary>
public sealed partial class ContactsWindow
{
    // Copy: another program may hold the clipboard open for a moment; tried
    // this many times, this far apart (as the address chips' Copy Address).
    private const int ClipboardAttempts = 5;
    private static readonly TimeSpan ClipboardRetryDelay = TimeSpan.FromMilliseconds(50);

    // What the card was built from ("" none).
    private string detailKey = "";

    // The detail controller changed: the card or the form, the error, the
    // empty state and the commands.
    private void RenderDetail()
    {
        if (closed)
        {
            return;
        }
        var lines = new List<string>();
        if (detail.Error.Length > 0)
        {
            lines.Add(detail.Error);
        }
        lines.AddRange(detail.Problems.Select(p => p.Text).Where(t => t.Length > 0).Distinct());
        ErrorBar.Message = string.Join('\n', lines);
        ErrorBar.IsOpen = lines.Count > 0;
        NewButton.IsEnabled = list.CanCreate && !detail.Saving;

        if (detail.Form is { } f)
        {
            detailKey = "";
            RenderForm(f);
        }
        else if (detail.Card is { } card)
        {
            ForgetForm();
            RenderCard(card);
        }
        else
        {
            ForgetForm();
            detailKey = "";
            DetailHost.Children.Clear();
        }

        var empty = detail.Editing || detail.Card is not null || detail.Id is not null ? ContactsEmpty.None : list.DetailEmpty;
        var (title, body) = ContactsText.EmptyText(empty);
        DetailEmpty.Visibility = empty == ContactsEmpty.None ? Visibility.Collapsed : Visibility.Visible;
        DetailEmptyTitle.Text = title;
        DetailEmptyBody.Text = body;
        DetailEmptyBody.Visibility = body.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        OpenPreferencesButton.Visibility = empty == ContactsEmpty.NoBooks ? Visibility.Visible : Visibility.Collapsed;
        DetailScroller.Visibility = empty == ContactsEmpty.None ? Visibility.Visible : Visibility.Collapsed;
        var busy = (detail.Loading && detail.Card is null) || detail.Saving || detail.Deleting;
        Busy.IsActive = busy;
        Busy.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
        UpdateCommands();
    }

    // The CommandBar: the card's actions, or Done and Cancel of the form.
    private void UpdateCommands()
    {
        var card = detail.Card;
        var editing = detail.Editing;
        var d = detail.Detail;
        Commands.Visibility = editing || card is not null ? Visibility.Visible : Visibility.Collapsed;
        var viewing = !editing && card is not null;
        WriteEmailButton.Visibility = Show(viewing);
        ShowMailButton.Visibility = Show(viewing);
        CopyButton.Visibility = Show(viewing);
        WriteEmailButton.IsEnabled = d?.Actions.WriteEmail == true;
        if (d is { Actions.Emails.Count: > 1 } several)
        {
            // Several addresses: the button is a choice of them.
            var menu = new MenuFlyout { Placement = FlyoutPlacementMode.BottomEdgeAlignedLeft };
            var n = 0;
            foreach (var row in several.Actions.Emails)
            {
                var item = new MenuFlyoutItem { Text = ContactsText.TwoParts(row.Label, row.Value) };
                AutomationProperties.SetAutomationId(item, "ContactsWriteEmail:" + n++);
                var address = row.Address;
                var name = several.Title;
                item.Click += (_, _) => WriteEmail(address, name);
                menu.Items.Add(item);
            }
            WriteEmailButton.Flyout = menu;
        }
        else
        {
            WriteEmailButton.Flyout = null;
        }
        ShowMailButton.IsEnabled = d is { Actions.ShowMail.Length: > 0 };
        CopyButton.IsEnabled = card is not null;

        var writable = card is { ReadOnly: false };
        EditButton.Visibility = Show(viewing && writable);
        EditButton.IsEnabled = detail.CanEdit;
        var deletable = writable && !(detail.Form?.IsNew ?? false);
        DeleteButton.Visibility = Show(deletable);
        DeleteButton.IsEnabled = detail.CanDelete;
        EditSeparator.Visibility = Show(viewing && writable);
        SaveButton.Visibility = Show(editing);
        CancelButton.Visibility = Show(editing);
        SaveButton.IsEnabled = editing && !detail.Saving;
        CancelButton.IsEnabled = editing && !detail.Saving;
    }

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // The card: the head (avatar, name, "Job · Company", the source line,
    // Read Only) and a row per detail; built again when the card changed.
    private void RenderCard(ContactCard card)
    {
        var key = card.Id.Value + "|" + card.Revision + "|" + detail.SourceLine;
        if (key == detailKey || detail.Detail is not { } d)
        {
            return;
        }
        detailKey = key;
        DetailHost.Children.Clear();

        var head = new Grid { ColumnSpacing = 14 };
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var avatar = new Main.Avatar { Text = d.Title, Size = 64, VerticalAlignment = VerticalAlignment.Center };
        AutomationProperties.SetAccessibilityView(avatar, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        head.Children.Add(avatar);
        var texts = new StackPanel { Spacing = 2, VerticalAlignment = VerticalAlignment.Center };
        texts.Children.Add(Label(d.Title, "Title2LabelStyle", "ContactsDetailTitle"));
        if (d.Subtitle.Length > 0)
        {
            texts.Children.Add(Label(d.Subtitle, "DimLabelStyle", "ContactsDetailSubtitle"));
        }
        if (detail.SourceLine is { Length: > 0 } source)
        {
            texts.Children.Add(Label(source, "DimCaptionLabelStyle", "ContactsDetailSource"));
        }
        Grid.SetColumn(texts, 1);
        head.Children.Add(texts);
        if (d.ReadOnly)
        {
            var ro = new Border
            {
                Style = (Style)Application.Current.Resources["KindBadgeBorderStyle"],
                VerticalAlignment = VerticalAlignment.Center,
                Child = new TextBlock { Style = (Style)Application.Current.Resources["KindBadgeTextStyle"], Text = ContactsText.ReadOnly },
            };
            AutomationProperties.SetAutomationId(ro, "ContactsReadOnly");
            AutomationProperties.SetName(ro, ContactsText.ReadOnly);
            Grid.SetColumn(ro, 2);
            head.Children.Add(ro);
        }
        DetailHost.Children.Add(head);

        if (d.Rows.Count > 0)
        {
            var rows = new StackPanel
            {
                Spacing = 0,
                Padding = new Thickness(12, 4, 6, 4),
                CornerRadius = new CornerRadius(8),
                Style = (Style)Root.Resources["ContactsDetailCard"],
                BorderThickness = new Thickness(1),
            };
            for (var i = 0; i < d.Rows.Count; i++)
            {
                rows.Children.Add(DetailRow(d, d.Rows[i], i));
            }
            DetailHost.Children.Add(rows);
        }
    }

    private static TextBlock Label(string text, string style, string automationId)
    {
        var t = new TextBlock
        {
            Text = text,
            Style = (Style)Application.Current.Resources[style],
            TextWrapping = TextWrapping.Wrap,
            IsTextSelectionEnabled = true,
        };
        AutomationProperties.SetAutomationId(t, automationId);
        return t;
    }

    // A row of label and value, with its action: Write Email for an address,
    // Copy for a phone number, the website through Open This Link?.
    private Grid DetailRow(ContactBook.ContactDetail d, ContactBook.DetailRow r, int index)
    {
        var row = new Grid { ColumnSpacing = 12, MinHeight = 40, Padding = new Thickness(0, 4, 0, 4) };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(110) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var label = new TextBlock
        {
            Text = r.Label,
            Style = (Style)Application.Current.Resources["DimLabelStyle"],
            VerticalAlignment = VerticalAlignment.Center,
            TextWrapping = TextWrapping.Wrap,
        };
        row.Children.Add(label);
        var value = new TextBlock
        {
            Text = r.Value,
            VerticalAlignment = VerticalAlignment.Center,
            TextWrapping = TextWrapping.Wrap,
            IsTextSelectionEnabled = true,
        };
        AutomationProperties.SetAutomationId(value, "ContactsDetailRow:" + index);
        AutomationProperties.SetName(value, ContactsText.TwoParts(r.Label, r.Value));
        Grid.SetColumn(value, 1);
        row.Children.Add(value);

        (string Icon, string Tip, Action Act)? action = r.Kind switch
        {
            ContactBook.RowKind.Email when r.Address.Length > 0 => ("mail-message-new", ContactsText.WriteEmail, () => WriteEmail(r.Address, d.Title)),
            ContactBook.RowKind.Phone => ("edit-copy", ContactsText.Copy, () => _ = CopyAsync(r.Value, ContactsText.Copied)),
            ContactBook.RowKind.Url when r.Url.Length > 0 => ("adw-external-link", Mnemonic.Strip(L10n.T("_Open Link")), () => _ = OpenWebsiteAsync(r.Url)),
            _ => null,
        };
        if (action is { } a)
        {
            var button = new Button
            {
                Content = Resources.Icons.Create(a.Icon, 14),
                Width = 32,
                Height = 32,
                Padding = new Thickness(0),
                VerticalAlignment = VerticalAlignment.Center,
                Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
            };
            ToolTipService.SetToolTip(button, a.Tip);
            AutomationProperties.SetName(button, a.Tip);
            AutomationProperties.SetAutomationId(button, "ContactsDetailAction:" + index);
            button.Click += (_, _) => a.Act();
            Grid.SetColumn(button, 2);
            row.Children.Add(button);
        }
        return row;
    }

    // Write Email: a new message to the address, from the card's account
    // when it writes mail (contacts_detail.go writeEmail).
    private void WriteEmail(string address, string name)
    {
        AccountId? account = null;
        if (detail.Card is { } card && list.Book(card.BookId) is { Account: { } a } && a.Can(Capability.Compose))
        {
            account = a.Id;
        }
        contacts.WriteEmail(address, name, account);
    }

    private void OnWriteEmailClick(object sender, RoutedEventArgs e)
    {
        // Several addresses: the button's menu chooses.
        if (WriteEmailButton.Flyout is not null || detail.Detail is not { } d)
        {
            return;
        }
        if (d.Rows.FirstOrDefault(r => r.Kind == ContactBook.RowKind.Email && r.Address.Length > 0) is { } first)
        {
            WriteEmail(first.Address, d.Title);
        }
    }

    // Show Mail: the main window's search from:address over every account.
    private void OnShowMailClick(object sender, RoutedEventArgs e)
    {
        if (detail.Detail is { Actions.ShowMail.Length: > 0 } d)
        {
            contacts.ShowMail(d.Actions.ShowMail);
        }
    }

    private void OnCopyClick(object sender, RoutedEventArgs e) => _ = CopyAsync(detail.CopyText, ContactsText.Copied);

    private void OnEditClick(object sender, RoutedEventArgs e) => detail.BeginEdit();

    private void OnSaveClick(object sender, RoutedEventArgs e) => detail.Save();

    // Cancel: asks first when the form has changes.
    private void OnCancelClick(object sender, RoutedEventArgs e) => ConfirmLeaving(() => detail.CancelEdit());

    // Delete Contact…: the controller asks (CardDAV has no trash).
    private void OnDeleteClick(object sender, RoutedEventArgs e) => detail.Delete();

    // The text on the clipboard and a toast that says so; another program
    // holding the clipboard is tried again a few times, then said (the
    // log has the kind only, never the text). Flushed, so it stays after
    // the application quits.
    private async Task CopyAsync(string text, string done)
    {
        if (text.Length == 0)
        {
            return;
        }
        for (var attempt = 1; ; attempt++)
        {
            try
            {
                var package = new DataPackage();
                package.SetText(text);
                Clipboard.SetContent(package);
                break;
            }
            catch (Exception e) when (e is COMException or UnauthorizedAccessException)
            {
                if (attempt >= ClipboardAttempts)
                {
                    var kind = e.GetType().Name;
                    LogCopyFailed(logger, kind, e.HResult);
                    // Windows-only string: GTK's clipboard cannot refuse.
                    ToastsHost.Show(L10n.T("The text could not be copied"));
                    return;
                }
            }
            await Task.Delay(ClipboardRetryDelay);
        }
        try
        {
            Clipboard.Flush();
        }
        catch (Exception e) when (e is COMException or UnauthorizedAccessException)
        {
            // On the clipboard all the same, until the application quits.
            var kind = e.GetType().Name;
            LogCopyFailed(logger, kind, e.HResult);
        }
        ToastsHost.Show(done);
    }

    // A website (ContactBook.OpenUrl let through http and https only) after
    // "Open This Link?" has shown where it leads.
    private async Task OpenWebsiteAsync(string url)
    {
        if (state.Launcher.LinkTarget(url) is not { } destination)
        {
            // TRANSLATORS: %s is a technical error message.
            ToastsHost.Show(L10n.T("The link could not be opened: %s", "invalid URL")); // Windows-only string (the technical detail, as on macOS)
            return;
        }
        if (!await state.Alerts.OpenLinkQuestionAsync(this, "", destination))
        {
            return;
        }
        try
        {
            await state.Launcher.OpenLinkAsync(url, WindowPresenter.Handle(this));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            var kind = e.GetType().Name;
            LogOpenFailed(logger, kind);
            // TRANSLATORS: %s is a technical error message.
            ToastsHost.Show(L10n.T("The link could not be opened: %s", e.Message));
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "contacts: the clipboard refused the text ({Kind}, 0x{HResult:X8})")]
    private static partial void LogCopyFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Information, Message = "contacts: a website could not be opened ({Kind})")]
    private static partial void LogOpenFailed(ILogger logger, string kind);
}

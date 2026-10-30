// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Contacts/AddToContactsPopover.swift;
// GTK: ui/internal/window/contacts_chips.go (the Add to Contacts popover).
// Add to Contacts… of an address chip (W4 ④, A12): a flyout under the chip
// with the name the sender wrote — selected, with the warning under it,
// because the sender wrote it —, the address, the address book (the
// message's account's first, ContactBook.AddFromChip), More Details… (the
// Contacts window with the whole form) and Add. Nothing is saved before
// Add; Add runs contact.create through a ContactsDetailController of its own
// (validation with the book's limits, the server-first write and its
// errors), and the flyout closes once the server took the contact, with
// "Contact added" as a toast. Every text is a TextBlock's or a TextBox's
// Text.

using System;
using System.Linq;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Contacts;

/// <summary>Add to Contacts… of an address chip.</summary>
internal sealed class AddToContactsFlyout
{
    private static AddToContactsFlyout? open;

    private readonly AppContacts contacts;
    private readonly IToasts toasts;
    private readonly ContactsDetailController saver;
    private readonly Flyout flyout = new() { Placement = FlyoutPlacementMode.BottomEdgeAlignedLeft };
    private readonly TextBox nameBox = new();
    private readonly ComboBox bookBox = new() { MinWidth = 260, HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly TextBlock errorText = new() { TextWrapping = TextWrapping.Wrap, Visibility = Visibility.Collapsed };
    private readonly Button addButton = new();
    private readonly ProgressRing spinner = new() { Width = 16, Height = 16, IsActive = false, Visibility = Visibility.Collapsed };
    private ContactBook.AddFromChip chip;
    private bool saving;

    private AddToContactsFlyout(AppContacts contacts, Address address, AccountId? account, IToasts toasts)
    {
        this.contacts = contacts;
        this.toasts = toasts;
        chip = new ContactBook.AddFromChip(address, account, contacts.Books, contacts.Accounts);
        saver = new ContactsDetailController(contacts.Options, Resolve);
        saver.Changed += (_, _) => SaverChanged();
        saver.Created += (_, _) => Created();
        flyout.Content = Build();
        flyout.Closed += (_, _) =>
        {
            if (ReferenceEquals(open, this))
            {
                open = null;
            }
            saver.Stop();
        };
        flyout.Opened += (_, _) =>
        {
            nameBox.Focus(FocusState.Programmatic);
            if (chip.SelectName)
            {
                nameBox.SelectAll();
            }
        };
    }

    /// <summary>The flyout of <paramref name="address"/> (from a message of <paramref name="account"/>) under <paramref name="anchor"/>; a second chip's replaces it.</summary>
    public static void Show(AppContacts contacts, Address address, AccountId? account, FrameworkElement anchor, IToasts toasts)
    {
        ArgumentNullException.ThrowIfNull(contacts);
        ArgumentNullException.ThrowIfNull(address);
        ArgumentNullException.ThrowIfNull(anchor);
        ArgumentNullException.ThrowIfNull(toasts);
        open?.flyout.Hide();
        var f = new AddToContactsFlyout(contacts, address, account, toasts);
        open = f;
        f.flyout.ShowAt(anchor);
    }

    private (AddressBook Book, Account? Account)? Resolve(AddressBookId id) =>
        contacts.Books.FirstOrDefault(b => b.Id == id) is { } b ? (b, contacts.Accounts.FirstOrDefault(a => a.Id == b.AccountId)) : null;

    private StackPanel Build()
    {
        var panel = new StackPanel { Width = 340, Spacing = 10 };
        AutomationProperties.SetAutomationId(panel, "AddToContacts");
        AutomationProperties.SetName(panel, ContactsText.AddTitle);
        var title = new TextBlock { Text = ContactsText.AddTitle, Style = (Style)Application.Current.Resources["HeadingLabelStyle"] };
        AutomationProperties.SetHeadingLevel(title, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level1);
        panel.Children.Add(title);

        nameBox.Header = ContactBook.NameLabel;
        nameBox.Text = chip.Name;
        nameBox.IsSpellCheckEnabled = false;
        AutomationProperties.SetAutomationId(nameBox, "AddToContactsName");
        AutomationProperties.SetName(nameBox, ContactBook.NameLabel);
        nameBox.TextChanged += (_, _) => chip = chip with { Name = nameBox.Text };
        panel.Children.Add(nameBox);
        if (chip.Warning.Length > 0)
        {
            var warning = new TextBlock { Text = chip.Warning, TextWrapping = TextWrapping.Wrap, Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"] };
            AutomationProperties.SetAutomationId(warning, "AddToContactsWarning");
            panel.Children.Add(warning);
        }

        var address = new StackPanel { Spacing = 2 };
        address.Children.Add(new TextBlock { Text = ContactBook.EmailLabel, Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"] });
        var addressText = new TextBlock { Text = chip.Address, IsTextSelectionEnabled = true, TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetAutomationId(addressText, "AddToContactsAddress");
        address.Children.Add(addressText);
        panel.Children.Add(address);

        bookBox.Header = ContactBook.BookLabel;
        AutomationProperties.SetAutomationId(bookBox, "AddToContactsBook");
        AutomationProperties.SetName(bookBox, ContactBook.BookLabel);
        for (var i = 0; i < chip.Books.Count; i++)
        {
            // A string content is shown by a TextBlock's Text, never parsed.
            bookBox.Items.Add(chip.Books[i].Text);
            if (chip.Books[i].Book == chip.Book)
            {
                bookBox.SelectedIndex = i;
            }
        }
        bookBox.SelectionChanged += (_, _) =>
        {
            if (bookBox.SelectedIndex is var i && i >= 0 && i < chip.Books.Count)
            {
                chip = chip with { Book = chip.Books[i].Book };
                Refresh();
            }
        };
        panel.Children.Add(bookBox);

        errorText.Foreground = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["SystemFillColorCriticalBrush"];
        AutomationProperties.SetAutomationId(errorText, "AddToContactsError");
        AutomationProperties.SetLiveSetting(errorText, Microsoft.UI.Xaml.Automation.Peers.AutomationLiveSetting.Polite);
        panel.Children.Add(errorText);

        var buttons = new Grid { ColumnSpacing = 8 };
        buttons.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        buttons.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        buttons.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        buttons.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var more = new HyperlinkButton { Content = ContactsText.MoreDetails, Padding = new Thickness(2, 4, 2, 4) };
        AutomationProperties.SetAutomationId(more, "AddToContactsMore");
        more.Click += (_, _) => MoreDetails();
        buttons.Children.Add(more);
        Grid.SetColumn(spinner, 2);
        spinner.VerticalAlignment = VerticalAlignment.Center;
        buttons.Children.Add(spinner);
        addButton.Content = ContactsText.Add;
        addButton.Style = (Style)Application.Current.Resources["AccentButtonStyle"];
        AutomationProperties.SetAutomationId(addButton, "AddToContactsAdd");
        addButton.Click += (_, _) => Add();
        Grid.SetColumn(addButton, 3);
        buttons.Children.Add(addButton);
        panel.Children.Add(buttons);
        Refresh();
        return panel;
    }

    private void Refresh()
    {
        addButton.IsEnabled = chip.CanAdd && !saving;
        nameBox.IsEnabled = !saving;
        bookBox.IsEnabled = !saving;
        spinner.IsActive = saving;
        spinner.Visibility = saving ? Visibility.Visible : Visibility.Collapsed;
    }

    // Add: contact.create through the saver; the flyout stays with the
    // sentence when the server refuses.
    private void Add()
    {
        if (!chip.CanAdd || saving || chip.Book is not { } book)
        {
            return;
        }
        chip = chip with { Name = nameBox.Text };
        saving = true;
        errorText.Visibility = Visibility.Collapsed;
        Refresh();
        saver.BeginNew(book, chip.Form());
        saver.Save();
    }

    private void SaverChanged()
    {
        if (!saving || saver.Saving)
        {
            return;
        }
        var text = saver.Problems.Count > 0 ? saver.Problems[0].Text : saver.Error;
        if (text.Length > 0)
        {
            saving = false;
            errorText.Text = text;
            errorText.Visibility = Visibility.Visible;
            Refresh();
        }
    }

    private void Created()
    {
        saving = false;
        contacts.Lookup.Invalidate();
        contacts.Refresh();
        flyout.Hide();
        toasts.Show(ContactsText.ContactAdded);
    }

    // More Details…: the Contacts window with the whole form, prefilled.
    private void MoreDetails()
    {
        chip = chip with { Name = nameBox.Text };
        var form = chip.Form();
        flyout.Hide();
        contacts.NewContact(form);
    }
}

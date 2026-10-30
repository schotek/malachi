// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Contacts/ContactsWindowController.swift
// (wire, handle, update, applyPending, reveal, beginNew, newContact,
// choose, chooseBook, confirmLeaving, ask, backToList, applyFold,
// windowShouldClose, windowWillClose, ContactsStatusBar),
// ContactsSidebarViewController.swift and ContactsListViewController.swift;
// GTK: ui/internal/window/contacts_window.go and contacts_consent.go. The
// state is Core's ContactsListController (the sidebar, the list, the
// search, the consent, the status line) and ContactsDetailController (the
// contact and its form, ContactsWindow.Detail.cs and .Form.cs); this window
// only shows what they hold after every Changed and asks the questions (an
// unsaved form before another contact, Delete, the conflict).
//
// Windows (docs/windows-port.md §11.12, windows/README.md): the search is
// the title bar's box (Ctrl+E and Ctrl+F, the window's Search command), the
// detail's actions a CommandBar, a list row has a context menu (Write
// Email, Show Mail, Copy Address), Microsoft's consent is an InfoBar per
// account, the questions are ContentDialogs in the order of a Windows dialog
// (AlertService), the window folds at 900 and 600 effective pixels as the
// main window does, and it opens from the "…" menus' Contacts without a
// key. Ctrl+W, the close button and Alt+F4 ask "Discard Changes?" before an
// unsaved form goes; Quit does not.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Input;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Contacts;

/// <summary>The Contacts window.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The timer and the controllers end when the window closes (Closed).")]
public sealed partial class ContactsWindow : Window
{
    /// <summary>The window's width the first time it opens (W1), in effective pixels.</summary>
    public const int DefaultWidth = 980;

    /// <summary>Its height the first time it opens.</summary>
    public const int DefaultHeight = 640;

    /// <summary>The narrowest it gets.</summary>
    public const int MinWidth = 380;

    /// <summary>The lowest it gets.</summary>
    public const int MinHeight = 360;

    // The wireframe's columns (W1).
    private const double SidebarWidth = 240;
    private const double ListWidth = 300;

    // The list asks for its next page this close to its end.
    private const int LoadMoreMargin = 10;

    private readonly AppState state;
    private readonly AppContacts contacts;
    private readonly ContactsListController list;
    private readonly ContactsDetailController detail;
    private readonly ILogger logger;
    private readonly DispatcherQueueTimer statusTimer;
    private readonly CollectionViewSource groups = new() { IsSourceGrouped = true };

    private ContactBook.Fold fold;

    // The user showed the folded sidebar with the title bar's pane button.
    private bool sidebarShownByUser;

    // Show Contact waiting for the first listing (the query) and for the list
    // to hold the contact (the id); More Details… waiting for the books.
    private string? pendingQuery;
    private ContactId? pendingSelect;
    private ContactBook.Form? pendingForm;

    // What the sidebar, the list and the consent were built from.
    private string itemsKey = "";
    private string rowsKey = "";
    private string consentKey = "";

    // The views are being set from the controllers: their events are not the user's.
    private bool syncing;

    // The window closes without asking (after Discard).
    private bool closing;
    private bool closed;

    internal ContactsWindow(AppState state, AppContacts contacts)
    {
        this.state = state;
        this.contacts = contacts;
        logger = state.Logs.CreateLogger<ContactsWindow>();
        InitializeComponent();
        Title = ContactsText.WindowTitle;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = ContactsText.WindowTitle;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            SolidBackground.Visibility = Visibility.Visible;
        }
        Tracked = state.Windows.Track(this, WindowKind.Contacts, Root, ToastsHost);
        Tracked.Commands.Search.Handler = FocusSearch;
        Tracked.Commands.Search.CanExecute = () => true;
        Tracked.Commands.CloseWindow.Handler = RequestClose;

        list = new ContactsListController(contacts.Options);
        detail = new ContactsDetailController(contacts.Options, list.Book);
        BuildTexts();
        Wire();

        statusTimer = DispatcherQueue.CreateTimer();
        // The status line says "3 minutes ago": it ages with the clock.
        statusTimer.Interval = TimeSpan.FromSeconds(30);
        statusTimer.Tick += (_, _) => UpdateStatus();
        statusTimer.Start();

        AppWindow.Closing += OnAppWindowClosing;
        Root.SizeChanged += (_, _) => ApplyFold();
        Closed += (_, _) =>
        {
            closed = true;
            statusTimer.Stop();
            list.Stop();
            detail.Stop();
            LogClosed(logger);
        };
        list.Start();
        Update();
        LogOpened(logger);
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The window's toasts.</summary>
    internal IToasts Toasts => ToastsHost;

    // Whether the detail covers the list (the narrow window with a contact or a form).
    private bool DetailCoversList => fold.DetailOverList && (list.SelectedContact is not null || detail.Editing);

    /// <summary>notify.contactsChanged: the list reloads, the shown contact too unless a form is open.</summary>
    public void Handle(ContactsChangedNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        if (closed)
        {
            return;
        }
        list.Handle(API.Notify.ContactsChanged);
        detail.ContactsChanged(n);
    }

    /// <summary>notify.accountsChanged: the books and the consent anew.</summary>
    public void HandleAccountsChanged()
    {
        if (!closed)
        {
            list.Handle(API.Notify.AccountsChanged);
        }
    }

    /// <summary>Show Contact of an address chip: the address searched in All Contacts and the contact selected once listed.</summary>
    public void Reveal(ContactId id, string address) => ConfirmLeaving(() =>
    {
        pendingQuery = address;
        pendingSelect = id;
        Update();
    });

    /// <summary>More Details… of Add to Contacts: the form, prefilled.</summary>
    public void NewContactWith(ContactBook.Form form) => ConfirmLeaving(() =>
    {
        pendingForm = form;
        Update();
    });

    /// <summary>
    /// The default size at the display's scale, centred in the work area of
    /// the display <paramref name="near"/> is on; never smaller than the minimum.
    /// </summary>
    internal void Place(Window? near)
    {
        var from = near ?? this;
        var dpi = PInvoke.GetDpiForWindow((HWND)WindowPresenter.Handle(from));
        var scale = dpi == 0 ? 1.0 : dpi / 96.0;
        if (AppWindow.Presenter is OverlappedPresenter overlapped)
        {
            overlapped.PreferredMinimumWidth = (int)Math.Round(MinWidth * scale);
            overlapped.PreferredMinimumHeight = (int)Math.Round(MinHeight * scale);
        }
        var area = DisplayArea.GetFromWindowId(from.AppWindow.Id, DisplayAreaFallback.Primary).WorkArea;
        var size = new SizeInt32(
            Math.Min((int)Math.Round(DefaultWidth * scale), area.Width),
            Math.Min((int)Math.Round(DefaultHeight * scale), area.Height));
        AppWindow.MoveAndResize(new RectInt32(
            area.X + ((area.Width - size.Width) / 2), area.Y + ((area.Height - size.Height) / 2), size.Width, size.Height));
    }

    // The texts that never change.
    private void BuildTexts()
    {
        SearchBox.PlaceholderText = ContactsText.SearchContacts;
        AutomationProperties.SetName(SearchBox, ContactsText.SearchContacts);
        AutomationProperties.SetName(BookList, ContactsText.WindowTitle);
        AutomationProperties.SetName(ContactList, ContactsText.WindowTitle);
        ToolTipService.SetToolTip(NewButton, ContactsText.NewContact);
        AutomationProperties.SetName(NewButton, ContactsText.NewContact);
        WriteEmailButton.Label = ContactsText.WriteEmail;
        ShowMailButton.Label = ContactsText.ShowMail;
        CopyButton.Label = ContactsText.Copy;
        EditButton.Label = ContactsText.Edit;
        SaveButton.Label = ContactsText.Done;
        CancelButton.Label = ContactsText.Cancel;
        DeleteButton.Label = ContactsText.DeleteContact;
        OpenPreferencesButton.Content = ContactsText.OpenPreferences;
    }

    private void Wire()
    {
        list.Changed += (_, _) => Update();
        list.Selected += (_, id) =>
        {
            detail.Open(id);
            ShowSelection(id);
            ApplyFold();
        };
        list.SignInRequested += (_, id) => contacts.SignIn(id, this, ToastsHost);
        detail.Changed += (_, _) =>
        {
            RenderDetail();
            ApplyFold();
        };
        detail.AskQuestion = AskAsync;
        detail.Created += (_, id) =>
        {
            pendingSelect = id;
            contacts.Lookup.Invalidate();
            list.Reload();
        };
        detail.Deleted += (_, _) =>
        {
            contacts.Lookup.Invalidate();
            list.Select(null);
            list.Reload();
        };
    }

    // The list controller changed: every pane reads it anew.
    private void Update()
    {
        if (closed)
        {
            return;
        }
        ApplyPending();
        if (pendingSelect is { } id && list.Contacts.Any(c => c.Id == id))
        {
            pendingSelect = null;
            if (list.SelectedContact != id)
            {
                list.Select(id);
            }
        }
        RenderSidebar();
        RenderList();
        RenderConsent();
        RenderDetail();
        UpdateStatus();
        ApplyFold();
    }

    // Show Contact and More Details… arriving before the first listing wait
    // for it: the first answer resets the search.
    private void ApplyPending()
    {
        if (!list.Loaded)
        {
            return;
        }
        if (pendingQuery is { } q)
        {
            pendingQuery = null;
            if (list.Items.FirstOrDefault(i => i.Kind == ContactBook.ItemKind.All)?.Book is { } all && list.SelectedBook != all)
            {
                list.SelectBook(all);
            }
            syncing = true;
            SearchBox.Text = q;
            syncing = false;
            list.SetQuery(q);
        }
        if (pendingForm is { } f)
        {
            pendingForm = null;
            Begin(f);
        }
    }

    private void UpdateStatus()
    {
        var error = list.Error;
        StatusText.Text = error.Length > 0 ? error : list.Status;
        StatusIcon.Glyph = Resources.Icons.Glyph(error.Length > 0 ? "dialog-warning" : "view-refresh");
        StatusIcon.Visibility = StatusText.Text.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
    }

    // The sidebar: headings with their capsules, books with their counts;
    // built again only when the items changed, the selection follows.
    private void RenderSidebar()
    {
        var items = list.Items;
        var key = string.Join('\n', items.Select(i => $"{i.Kind}|{i.Book?.Value}|{i.Title}|{i.Badge}|{i.Count}"));
        syncing = true;
        try
        {
            if (key != itemsKey)
            {
                itemsKey = key;
                BookList.Items.Clear();
                foreach (var item in items)
                {
                    BookList.Items.Add(SidebarItem(item));
                }
            }
            BookList.SelectedItem = BookList.Items.OfType<ListViewItem>().FirstOrDefault(
                i => i.Tag is AddressBookId b && list.SelectedBook is { } s && b == s);
        }
        finally
        {
            syncing = false;
        }
    }

    private static ListViewItem SidebarItem(ContactBook.Item item)
    {
        if (!item.Selectable)
        {
            // An account's heading with its kind capsule, or On This Computer.
            var panel = new Main.CapsulePanel { Margin = new Thickness(0, 10, 0, 2) };
            var label = new TextBlock { Text = item.Title, Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"] };
            panel.Children.Add(label);
            var capsule = new Border
            {
                Style = (Style)Application.Current.Resources["KindBadgeBorderStyle"],
                Visibility = item.Badge.Length == 0 ? Visibility.Collapsed : Visibility.Visible,
                Child = new TextBlock { Style = (Style)Application.Current.Resources["KindBadgeTextStyle"], Text = item.Badge },
            };
            AutomationProperties.SetAccessibilityView(capsule, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
            panel.Children.Add(capsule);
            var heading = new ListViewItem { Content = panel, IsEnabled = false, IsTabStop = false };
            AutomationProperties.SetName(heading, item.Badge.Length == 0 ? item.Title : ContactsText.TwoParts(item.Title, item.Badge));
            AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level2);
            if (item.AccountId is { } acc)
            {
                AutomationProperties.SetAutomationId(heading, "ContactsAccount:" + acc.Value);
            }
            return heading;
        }
        var row = new Grid { Height = 32, ColumnSpacing = 8 };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var glyph = Resources.Icons.Create(item.Kind == ContactBook.ItemKind.Recent ? "document-open-recent" : "x-office-address-book", 14);
        glyph.VerticalAlignment = VerticalAlignment.Center;
        AutomationProperties.SetAccessibilityView(glyph, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        row.Children.Add(glyph);
        var title = new TextBlock
        {
            Text = item.Title,
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
        };
        Grid.SetColumn(title, 1);
        row.Children.Add(title);
        if (item.Count >= 0)
        {
            var count = new TextBlock
            {
                Text = item.Count.ToString(System.Globalization.CultureInfo.CurrentCulture),
                VerticalAlignment = VerticalAlignment.Center,
                Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            };
            AutomationProperties.SetAccessibilityView(count, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
            Grid.SetColumn(count, 2);
            row.Children.Add(count);
        }
        var book = new ListViewItem { Content = row, Tag = item.Book };
        AutomationProperties.SetAutomationId(book, "ContactsBook:" + item.Book?.Value);
        AutomationProperties.SetName(book, item.Count >= 0
            ? ContactsText.TwoParts(item.Title, item.Count.ToString(System.Globalization.CultureInfo.CurrentCulture))
            : item.Title);
        return book;
    }

    private void OnBookSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || BookList.SelectedItem is not ListViewItem { Tag: AddressBookId id } || id == list.SelectedBook)
        {
            return;
        }
        ConfirmLeaving(
            () =>
            {
                if (detail.Editing)
                {
                    detail.CancelEdit();
                }
                list.SelectBook(id);
            },
            RenderSidebarSelection);
    }

    private void RenderSidebarSelection()
    {
        itemsKey = "";
        RenderSidebar();
    }

    // The list: letter sections (flat while searching), built again only
    // when the rows changed; the selection follows the controller.
    private void RenderList()
    {
        ListTitle.Text = list.Subtitle;
        NewButton.IsEnabled = list.CanCreate && !detail.Saving;
        var rows = list.Rows;
        var key = (list.Query.Length == 0 ? "g\n" : "f\n") + string.Join('\n', rows.Select(r => r.IsHeading ? "#" + r.Heading : r.Contact!.Id.Value + "|" + r.Title + "|" + r.Detail));
        syncing = true;
        try
        {
            if (key != rowsKey)
            {
                rowsKey = key;
                if (list.Query.Length == 0)
                {
                    var built = new List<ContactsListGroup>();
                    ContactsListGroup? current = null;
                    foreach (var r in rows)
                    {
                        if (r.IsHeading)
                        {
                            current = new ContactsListGroup(r.Heading);
                            built.Add(current);
                        }
                        else if (r.Contact is not null)
                        {
                            if (current is null)
                            {
                                current = new ContactsListGroup("#");
                                built.Add(current);
                            }
                            current.Add(new ContactRowItem(r));
                        }
                    }
                    groups.Source = built;
                    ContactList.ItemsSource = groups.View;
                }
                else
                {
                    ContactList.ItemsSource = rows.Where(r => r.Contact is not null).Select(r => new ContactRowItem(r)).ToList();
                }
            }
            SelectRow(list.SelectedContact);
        }
        finally
        {
            syncing = false;
        }
        var empty = list.ListEmpty;
        var (title, body) = ContactsText.EmptyText(empty);
        ListEmpty.Visibility = empty == ContactsEmpty.None ? Visibility.Collapsed : Visibility.Visible;
        ListEmptyTitle.Text = title;
        ListEmptyBody.Text = body;
        ListEmptyBody.Visibility = body.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
    }

    // The list's selection as the controller has it.
    private void ShowSelection(ContactId? id)
    {
        syncing = true;
        try
        {
            SelectRow(id);
        }
        finally
        {
            syncing = false;
        }
    }

    private void SelectRow(ContactId? id)
    {
        ContactRowItem? found = null;
        if (id is { } s)
        {
            found = ShownItems().FirstOrDefault(i => i.Id == s);
        }
        if (!ReferenceEquals(ContactList.SelectedItem, found))
        {
            ContactList.SelectedItem = found;
        }
    }

    private IEnumerable<ContactRowItem> ShownItems() => ContactList.ItemsSource switch
    {
        IEnumerable<ContactRowItem> flat => flat,
        _ => (groups.Source as IEnumerable<ContactsListGroup> ?? []).SelectMany(g => g),
    };

    private void OnContactSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        var id = (ContactList.SelectedItem as ContactRowItem)?.Id;
        if (id is null && list.SelectedContact is not null)
        {
            // The list lost its selection on a rebuild: the controller's stays.
            ShowSelection(list.SelectedContact);
            return;
        }
        Choose(id);
    }

    // The list's click on a contact: an unsaved form asks first; Cancel keeps
    // the form and the list's selection on what it shows.
    private void Choose(ContactId? id)
    {
        if (id == list.SelectedContact && !detail.Editing)
        {
            return;
        }
        ConfirmLeaving(
            () =>
            {
                if (id == list.SelectedContact)
                {
                    detail.Open(id);
                }
                else
                {
                    list.Select(id);
                }
            },
            () => ShowSelection(list.SelectedContact));
    }

    // The rows' automation names, and the next page near the end.
    private void OnContainerContentChanging(ListViewBase sender, ContainerContentChangingEventArgs args)
    {
        if (args.Item is ContactRowItem item)
        {
            AutomationProperties.SetAutomationId(args.ItemContainer, "ContactsRow:" + item.Id.Value);
            AutomationProperties.SetName(args.ItemContainer, item.Spoken);
        }
        if (list.HasMore && args.ItemIndex >= ShownItems().Count() - LoadMoreMargin)
        {
            list.LoadMore();
        }
    }

    // Microsoft's consent: an InfoBar per account, Allow… when a new sign-in
    // can give it, else the Online Accounts advice.
    private void RenderConsent()
    {
        var needs = list.Consent;
        var key = string.Join('\n', needs.Select(n => $"{n.AccountId.Value}|{n.Account}|{n.SignIn}"));
        if (key == consentKey)
        {
            return;
        }
        consentKey = key;
        ConsentBars.Children.Clear();
        foreach (var need in needs)
        {
            var bar = new InfoBar
            {
                IsOpen = true,
                IsClosable = false,
                Severity = InfoBarSeverity.Informational,
                Title = ContactsText.ConsentBanner(need.Account),
                Message = need.SignIn ? "" : ContactsText.ConsentOnline,
            };
            AutomationProperties.SetAutomationId(bar, "ContactsConsent:" + need.AccountId.Value);
            if (need.SignIn)
            {
                var allow = new Button { Content = ContactsText.Allow };
                AutomationProperties.SetAutomationId(allow, "ContactsAllow:" + need.AccountId.Value);
                var account = need.AccountId;
                allow.Click += (_, _) => list.AllowConsent(account);
                bar.ActionButton = allow;
            }
            ConsentBars.Children.Add(bar);
        }
    }

    // A list row's context menu: Write Email, Show Mail, Copy Address for
    // its address (W6; the selection stays).
    private void OnListContextRequested(UIElement sender, ContextRequestedEventArgs args)
    {
        if (FindItem(args.OriginalSource as DependencyObject) is not { } found)
        {
            return;
        }
        var (container, item) = found;
        args.Handled = true;
        var address = item.Contact.Email ?? "";
        var menu = new MenuFlyout();
        var write = new MenuFlyoutItem { Text = ContactsText.WriteEmail, IsEnabled = address.Length > 0, Icon = Resources.Icons.Create("mail-message-new", 16) };
        AutomationProperties.SetAutomationId(write, "ContactsMenuWriteEmail");
        write.Click += (_, _) => contacts.WriteEmail(address, item.Title, null);
        var show = new MenuFlyoutItem { Text = ContactsText.ShowMail, IsEnabled = address.Length > 0, Icon = Resources.Icons.Create("mail-inbox", 16) };
        AutomationProperties.SetAutomationId(show, "ContactsMenuShowMail");
        show.Click += (_, _) => contacts.ShowMail(ContactBook.ShowMailQuery(address));
        var copy = new MenuFlyoutItem { IsEnabled = address.Length > 0, Icon = Resources.Icons.Create("edit-copy", 16) };
        Localization.MnemonicLabel.Apply(copy, L10n.T("_Copy Address"));
        AutomationProperties.SetAutomationId(copy, "ContactsMenuCopyAddress");
        copy.Click += (_, _) => _ = CopyAsync(address, L10n.T("Address copied"));
        menu.Items.Add(write);
        menu.Items.Add(show);
        menu.Items.Add(copy);
        if (args.TryGetPosition(container, out var point))
        {
            menu.ShowAt(container, point);
        }
        else
        {
            menu.ShowAt(container);
        }
    }

    private static (ListViewItem Container, ContactRowItem Item)? FindItem(DependencyObject? from)
    {
        for (var d = from; d is not null; d = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetParent(d))
        {
            if (d is ListViewItem { Content: ContactRowItem item } container)
            {
                return (container, item);
            }
        }
        return null;
    }

    // The search: the daemon is asked from the first character.
    private void OnSearchTextChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        if (!syncing && args.Reason == AutoSuggestionBoxTextChangeReason.UserInput)
        {
            list.SetQuery(sender.Text);
        }
    }

    // Ctrl+E and Ctrl+F: the box takes the keyboard with its text selected
    // (the sidebar shows first when the detail covers the list).
    private void FocusSearch()
    {
        SearchBox.Focus(FocusState.Keyboard);
        if (FindDescendant<TextBox>(SearchBox) is { } box)
        {
            box.SelectAll();
        }
    }

    private static T? FindDescendant<T>(DependencyObject root)
        where T : DependencyObject
    {
        var n = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetChildrenCount(root);
        for (var i = 0; i < n; i++)
        {
            var child = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetChild(root, i);
            if (child is T t)
            {
                return t;
            }
            if (FindDescendant<T>(child) is { } deeper)
            {
                return deeper;
            }
        }
        return null;
    }

    // New Contact (+): in the selected book when writable, else the default one.
    private void OnNewClick(object sender, RoutedEventArgs e)
    {
        if (list.CanCreate)
        {
            ConfirmLeaving(() => Begin(null));
        }
    }

    private void Begin(ContactBook.Form? form)
    {
        list.Select(null);
        detail.BeginNew(form?.Book ?? list.NewContactBook, form);
        ApplyFold();
    }

    private void OnOpenPreferencesClick(object sender, RoutedEventArgs e) => contacts.OpenPreferences();

    /// <summary>
    /// Runs <paramref name="go"/> when no unsaved form is in the way, else
    /// after "Discard Changes?" answered with Discard (the form closes
    /// unsaved); <paramref name="cancelled"/> after Cancel.
    /// </summary>
    private void ConfirmLeaving(Action go, Action? cancelled = null)
    {
        if (!detail.Dirty)
        {
            go();
            return;
        }
        _ = ConfirmLeavingAsync(go, cancelled);
    }

    private async Task ConfirmLeavingAsync(Action go, Action? cancelled)
    {
        if (await AskAsync(ContactsText.DiscardQuestion))
        {
            if (closed)
            {
                return;
            }
            detail.CancelEdit();
            go();
        }
        else
        {
            cancelled?.Invoke();
        }
    }

    /// <summary>
    /// Quit's step (QuitSteps.ContactForm): with an unsaved form the window
    /// comes to the front and asks "Discard Changes?"; Discard closes the
    /// form unsaved (true, the Quit goes on), Cancel keeps it (false, the
    /// Quit is abandoned). Without one, true at once.
    /// </summary>
    internal async Task<bool> ConfirmQuitAsync()
    {
        if (closed || !detail.Dirty)
        {
            return true;
        }
        WindowPresenter.Present(this);
        if (!await AskAsync(ContactsText.DiscardQuestion))
        {
            return false;
        }
        if (!closed)
        {
            detail.CancelEdit();
        }
        return true;
    }

    // A question of the window or the detail controller: Delete and Discard
    // as destructive dialogs with Cancel the default, the conflict with
    // Reload the default.
    private Task<bool> AskAsync(ContactsQuestion q)
    {
        if (q.Destructive)
        {
            return state.Alerts.ConfirmDestructiveAsync(this, q.Heading, q.Body, q.Confirm);
        }
        return state.Alerts.ConfirmAsync(this, q.Heading, q.Body, q.Confirm, ContactsText.Cancel);
    }

    // The narrow window's back button: the list again.
    private void OnBackRequested(TitleBar sender, object args) => ConfirmLeaving(() =>
    {
        if (detail.Editing)
        {
            detail.CancelEdit();
        }
        list.Select(null);
        ApplyFold();
    });

    // The pane button of a folded window: shows or hides the sidebar until
    // the window is wide again (or folds further).
    private void OnPaneToggleRequested(TitleBar sender, object args)
    {
        sidebarShownByUser = !sidebarShownByUser;
        ApplyFold();
    }

    // W5 d: below 900 the sidebar folds away, below 600 the detail covers the
    // list (ContactBook.FoldFor with the main window's breakpoints).
    private void ApplyFold()
    {
        if (Root.ActualWidth <= 0)
        {
            return;
        }
        var f = ContactBook.FoldFor(Root.ActualWidth, ContactBook.GtkBreakpoints);
        if (f.SidebarCollapsed != fold.SidebarCollapsed || f.DetailOverList != fold.DetailOverList)
        {
            sidebarShownByUser = false;
        }
        fold = f;
        var covers = DetailCoversList;
        // Folded, the books stay reachable from the list through the pane
        // button (below 600 too); the detail over the list has Back instead.
        var sidebar = !(f.DetailOverList && covers) && (!f.SidebarCollapsed || sidebarShownByUser);
        SidebarColumn.Width = new GridLength(sidebar ? SidebarWidth : 0);
        BookList.Visibility = sidebar ? Visibility.Visible : Visibility.Collapsed;
        if (f.DetailOverList)
        {
            ListColumn.Width = covers ? new GridLength(0) : new GridLength(1, GridUnitType.Star);
            DetailColumn.Width = covers ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
        }
        else
        {
            ListColumn.Width = new GridLength(ListWidth);
            DetailColumn.Width = new GridLength(1, GridUnitType.Star);
        }
        ListPane.Visibility = f.DetailOverList && covers ? Visibility.Collapsed : Visibility.Visible;
        DetailPane.Visibility = f.DetailOverList && !covers ? Visibility.Collapsed : Visibility.Visible;
        Header.IsBackButtonVisible = covers;
        Header.IsPaneToggleButtonVisible = f.SidebarCollapsed && !covers;
    }

    // Ctrl+W: an unsaved form asks first.
    private void RequestClose()
    {
        if (closing || !detail.Dirty)
        {
            closing = true;
            Close();
            return;
        }
        ConfirmLeaving(() =>
        {
            closing = true;
            Close();
        });
    }

    // The close button and Alt+F4.
    private void OnAppWindowClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (closing || !detail.Dirty)
        {
            return;
        }
        args.Cancel = true;
        RequestClose();
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "Contacts opened")]
    private static partial void LogOpened(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "Contacts closed")]
    private static partial void LogClosed(ILogger logger);
}

// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarPageViewController.swift
// (the page's slots, controllerChanged, open(_:anchoredTo:), anchor(for:),
// the assistant panel's slot; CalendarEmptyView.set), of the menu part of
// MainWindowController+Mode.swift (View ▸ Mail, Board, Calendar; File ▸ New
// Event) and of the calendar's keys (MalachiCore's CalendarController
// .handleKey, which macOS reaches through its menus); GTK: the calendar
// page follows (docs/calendar.md §6.4), window/board.go's mode switch is
// the reference of the menu.
//
// The main window's Calendar mode, the page that takes the panes' place
// (MainWindow.Mode.cs, MainWindow.Calendar.cs, MainWindow.CalendarPage.cs).
// Its bar holds CalendarBar (the sidebar toggle, Today, ‹ ›, the views, the
// title, New Event and ✦) and the "…" menu (the window's modes, New Event,
// Add Account…, Preferences, Quit); its content CalendarContentView (the
// sidebar, the views, the assistant panel's slot), with the empty state of
// Core's EmptyKind over the views (Open Preferences for No Calendars and
// Calendar Sync Is Off, as macOS CalendarEmptyView), the toast layer and
// the calendar's query as the title bar's search box holds it. Until the
// window's controller exists the page shows the mode's title and the
// empty state alone.
//
// The keys (the Board's pattern, BoardPage.OnPageKeyDown): every key the
// page sees goes to CalendarController.HandleKey as a Cal.Press
// (Press.FromVirtualKey: Ctrl, and Shift for the keys' Alt and Control):
// Ctrl+T Today, Ctrl+Left and Ctrl+Right, Ctrl+Shift+1 to 4 the views,
// Return the selection's detail, Delete its removal (the detail's question),
// Escape in two steps (an open flyout closes first, then the search
// clears; Cal.EscapeFor), the arrows the selection in time order (only
// with the keyboard in the views: the sidebar's controls keep theirs). New
// Event (Ctrl+Shift+N) and Ctrl+3 are ShortcutMap's (the window's
// CommandRouter, from a WebView2 too), so the page leaves them alone and
// each runs once; Ctrl+F and Ctrl+E give the keyboard to the title bar's
// box, which is the calendar's search here. DetailOpen is the event
// detail's to set while its flyout shows.

using System;
using System.Linq;
using Malachi.App.Commands;
using Malachi.App.Main;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.UI.Core;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Calendars;

/// <summary>The main window's Calendar mode.</summary>
public sealed partial class CalendarPage : UserControl
{
    // The bar's title, shown while no calendar controls are in the bar.
    private readonly UIElement titleBar;
    private WindowCommands? commands;
    private CalendarController? controller;
    private CalendarBar? bar;
    private CalendarContentView? content;
    private bool assistantAvailable;

    /// <summary>An empty page: the title, and the empty state of no calendars.</summary>
    public CalendarPage()
    {
        InitializeComponent();
        titleBar = (UIElement)BarSlot.Content;
        ShowTitle(Cal.Text.ModeName, "");
        ShowEmpty(Cal.EmptyKind.NoAccounts);
    }

    /// <summary>The empty state's Open Preferences (the window opens them).</summary>
    public event EventHandler? OpenPreferencesRequested;

    /// <summary>✦ in the bar: the window shows or folds its assistant panel here (D28).</summary>
    public event EventHandler? AssistantToggleRequested;

    /// <summary>The sidebar's Add Calendar… ▸ Add CalDAV Calendar….</summary>
    public event EventHandler? AddCalDAVRequested;

    /// <summary>The sidebar's Add Calendar… ▸ Sync a Microsoft 365 account's calendars….</summary>
    public event EventHandler? SyncMicrosoftRequested;

    /// <summary>Ctrl+F or Ctrl+E in the page: the title bar's box (the calendar's search) takes the keyboard.</summary>
    public event EventHandler? SearchFocusRequested;

    /// <summary>Escape with the detail open (the controller's ClosePopoverRequested): the event detail closes its flyout.</summary>
    public event EventHandler? CloseDetailRequested;

    /// <summary>Where the window's toasts lie while the calendar shows.</summary>
    public Panel ToastLayer => ToastLayerGrid;

    /// <summary>
    /// Whether the event detail's flyout is open (set by the detail while
    /// it shows): Escape closes it first (Cal.EscapeFor).
    /// </summary>
    public bool DetailOpen { get; set; }

    /// <summary>
    /// The calendar's controls in the bar (Today, the arrows, the views and
    /// the title); null shows the mode's title alone.
    /// </summary>
    public UIElement? BarContent
    {
        get => ReferenceEquals(BarSlot.Content, titleBar) ? null : BarSlot.Content as UIElement;
        set => BarSlot.Content = value ?? titleBar;
    }

    /// <summary>The calendar's sidebar and views, under the empty state while that shows.</summary>
    public UIElement? PageContent
    {
        get => ContentSlot.Content as UIElement;
        set => ContentSlot.Content = value;
    }

    /// <summary>
    /// The calendar's query, as the title bar's search box holds it while
    /// the calendar shows; the box gets it back when the calendar shows
    /// again (MainWindow.Calendar.cs).
    /// </summary>
    public string SearchText { get; set; } = "";

    /// <summary>The empty state's kind, or null while the content shows.</summary>
    public Cal.EmptyKind? Empty { get; private set; }

    /// <summary>Whether the window's assistant panel is in the page and unfolded.</summary>
    public bool AssistantShown => content?.AssistantShown == true;

    /// <summary>
    /// Connects the "…" menu to the window's commands: the modes (their
    /// keys Ctrl+1, Ctrl+2 and Ctrl+3), New Event, Add Account…,
    /// Preferences and Quit.
    /// </summary>
    public void Attach(WindowCommands commands)
    {
        ArgumentNullException.ThrowIfNull(commands);
        this.commands = commands;
        CommandBinding.Bind(MenuMail, commands.ShowMail);
        CommandBinding.Bind(MenuBoard, commands.ShowBoard);
        CommandBinding.Bind(MenuCalendar, commands.ShowCalendar);
        CommandBinding.Bind(MenuNewEvent, commands.NewEvent);
        CommandBinding.Bind(MenuAddAccount, commands.AddAccount);
        CommandBinding.Bind(MenuPreferences, commands.Preferences);
        CommandBinding.Bind(MenuQuit, commands.Quit);
    }

    /// <summary>
    /// The window's calendar exists: the bar's controls and the content
    /// over it, which follow its state from now on (<see cref="Update"/>).
    /// </summary>
    public void AttachCalendar(CalendarController calendar)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        if (controller is not null)
        {
            return;
        }
        controller = calendar;
        bar = new CalendarBar();
        content = new CalendarContentView();
        bar.SidebarToggleRequested += (_, _) => content.ToggleSidebar();
        bar.AssistantToggleRequested += (_, _) =>
        {
            AssistantToggleRequested?.Invoke(this, EventArgs.Empty);
            // ✦ shows whether the panel is here, whatever the window did.
            SyncBar();
        };
        if (commands is not null)
        {
            CommandBinding.Bind(bar.NewEvent, commands.NewEvent);
        }
        content.LayoutChanged += (_, _) => SyncBar();
        content.SidebarView.AddCalDAVRequested += (_, _) => AddCalDAVRequested?.Invoke(this, EventArgs.Empty);
        content.SidebarView.SyncMicrosoftRequested += (_, _) => SyncMicrosoftRequested?.Invoke(this, EventArgs.Empty);
        calendar.ClosePopoverRequested += (_, _) => ClosePopups();
        // The empty state lies over the views from now on, the sidebar beside it.
        ContentHost.Children.Remove(EmptyState);
        content.EmptyHost.Children.Add(EmptyState);
        BarContent = bar;
        PageContent = content;
        Update();
    }

    /// <summary>After every change of the controller (its Changed): the bar, the content and the empty state read it again.</summary>
    public void Update()
    {
        if (controller is not { } c || bar is null || content is null)
        {
            return;
        }
        bar.Update(c);
        content.Update(c);
        ShowEmpty(CalendarContentView.ShowsEmpty(c) ? c.EmptyKind : null);
        ShowTitle(c.Title, c.Subtitle);
        SyncBar();
    }

    /// <summary>The window's mode, for the menu's Mail, Board and Calendar.</summary>
    public void ShowMode(Board.Mode mode)
    {
        MenuMail.IsChecked = mode == Board.Mode.Mail;
        MenuBoard.IsChecked = mode == Board.Mode.Board;
        MenuCalendar.IsChecked = mode == Board.Mode.Calendar;
    }

    /// <summary>The page's "…" menu (F10 while the calendar shows).</summary>
    public void ShowMenu()
    {
        if (!PageMenu.IsOpen)
        {
            PageMenu.ShowAt(MoreButton);
        }
    }

    /// <summary>The bar's title and subtitle (the window's title and subtitle on macOS); an empty subtitle hides.</summary>
    public void ShowTitle(string title, string subtitle)
    {
        TitleText.Text = title ?? "";
        SubtitleText.Text = subtitle ?? "";
        SubtitleText.Visibility = string.IsNullOrEmpty(subtitle) ? Visibility.Collapsed : Visibility.Visible;
    }

    /// <summary>
    /// The empty state of <paramref name="kind"/> (macOS
    /// CalendarEmptyView.set): its title and explanation, Open Preferences
    /// where the remedy is there; null shows the content. Before the
    /// content came it covers the page; then it lies over the views, the
    /// sidebar (with Add Calendar…) beside it, as macOS's.
    /// </summary>
    public void ShowEmpty(Cal.EmptyKind? kind)
    {
        Empty = kind;
        if (kind is not { } k)
        {
            EmptyState.Visibility = Visibility.Collapsed;
            ContentSlot.Visibility = Visibility.Visible;
            return;
        }
        EmptyTitle.Text = Cal.Text.EmptyTitle(k);
        var body = Cal.Text.EmptyBody(k);
        EmptyBody.Text = body;
        EmptyBody.Visibility = body.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        OpenPreferencesButton.Visibility = k is Cal.EmptyKind.SyncOff or Cal.EmptyKind.NoAccounts ? Visibility.Visible : Visibility.Collapsed;
        EmptyState.Visibility = Visibility.Visible;
        ContentSlot.Visibility = content is null ? Visibility.Collapsed : Visibility.Visible;
    }

    /// <summary>
    /// Gives the keyboard to the page: the view's selection (or its first
    /// occurrence) while the content shows, else the empty state's button,
    /// else the first stop of the content, else the "…" button.
    /// </summary>
    public bool FocusContent()
    {
        if (Empty is null && content?.FocusView() == true)
        {
            return true;
        }
        if (Empty is not null && OpenPreferencesButton.Visibility == Visibility.Visible
            && OpenPreferencesButton.Focus(FocusState.Programmatic))
        {
            return true;
        }
        if (FocusManager.FindFirstFocusableElement(ContentSlot) is Control first && first.Focus(FocusState.Programmatic))
        {
            return true;
        }
        return MoreButton.Focus(FocusState.Programmatic);
    }

    /// <summary>
    /// The view of the occurrence with key <paramref name="occurrenceKey"/>
    /// (Cal.OccurrenceKey) to anchor its detail on (a click, Return, Show in
    /// Calendar, an alert's click): its block, bar, pill or agenda row
    /// (macOS anchor(for:)); null when no view shows it, and the detail is
    /// then anchored on the page.
    /// </summary>
    public FrameworkElement? AnchorFor(string occurrenceKey)
    {
        ArgumentNullException.ThrowIfNull(occurrenceKey);
        return content?.ElementFor(occurrenceKey);
    }

    /// <summary>✦ may show (the Assistant shown, In App chosen); a panel no longer allowed folds.</summary>
    public void SetAssistantAvailable(bool available)
    {
        assistantAvailable = available;
        if (!available)
        {
            content?.HideAssistant();
        }
        SyncBar();
    }

    /// <summary>Takes the window's assistant panel in and unfolds it (D28).</summary>
    public void ShowAssistant(UIElement panel)
    {
        content?.ShowAssistant(panel);
        SyncBar();
    }

    /// <summary>Folds the panel; it stays in the page until <see cref="ReleaseAssistant"/>.</summary>
    public void HideAssistant()
    {
        content?.HideAssistant();
        SyncBar();
    }

    /// <summary>Gives the panel back (the window leaves the Calendar).</summary>
    public UIElement? ReleaseAssistant()
    {
        var panel = content?.ReleaseAssistant();
        SyncBar();
        return panel;
    }

    // The bar's sidebar toggle and ✦ follow the content's layout.
    private void SyncBar()
    {
        if (bar is null || content is null)
        {
            return;
        }
        bar.SetSidebarShown(content.SidebarShown);
        bar.SetAssistant(assistantAvailable, content.AssistantShown);
    }

    // Escape's first step: the Month's "+N more" flyout and the detail close.
    private void ClosePopups()
    {
        content?.CloseFlyouts();
        CloseDetailRequested?.Invoke(this, EventArgs.Empty);
    }

    private void OnOpenPreferencesClick(object sender, RoutedEventArgs e) =>
        OpenPreferencesRequested?.Invoke(this, EventArgs.Empty);

    // The calendar's keys (CalendarController.HandleKey).
    private void OnPagePreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Handled || controller is not { } c || content is null)
        {
            return;
        }
        var ctrl = IsDown(WinKey.Control);
        var shift = IsDown(WinKey.Shift);
        var alt = IsDown(WinKey.Menu) || IsDown(WinKey.LeftWindows) || IsDown(WinKey.RightWindows);
        if (ctrl && !shift && !alt && e.Key is WinKey.F or WinKey.E)
        {
            e.Handled = true;
            SearchFocusRequested?.Invoke(this, EventArgs.Empty);
            return;
        }
        var press = Cal.Press.FromVirtualKey((int)e.Key, ctrl, shift, alt);
        var inText = KeyboardInText();
        var popup = DetailOpen || PopupOpen();
        var action = Cal.KeyFor(press, new Cal.KeyContext(Board.Mode.Calendar, inText, c.SelectedOccurrence is not null));
        // Ctrl+3 and New Event are the window's (ShortcutMap): running New
        // Event here too would open a second editor.
        if (action is null or Cal.KeyAction.ShowCalendar or Cal.KeyAction.NewEvent)
        {
            return;
        }
        // The selection's keys and the arrows act in the views only: the
        // sidebar's checkboxes, buttons and the bar keep theirs.
        var inViews = XamlRoot is { } root && content.InViews(FocusManager.GetFocusedElement(root) as DependencyObject);
        if (action is Cal.KeyAction.OpenDetail or Cal.KeyAction.Remove or Cal.KeyAction.SelectPrevious or Cal.KeyAction.SelectNext
            && !inViews)
        {
            return;
        }
        if (action == Cal.KeyAction.Remove && e.KeyStatus.WasKeyDown)
        {
            return;
        }
        if (c.HandleKey(press, inText, popup))
        {
            e.Handled = true;
            if (action is Cal.KeyAction.SelectPrevious or Cal.KeyAction.SelectNext)
            {
                // The keyboard follows the selection (the views have it after their update).
                DispatcherQueue.TryEnqueue(() => content.FocusView());
            }
        }
    }

    // A text field has the keyboard (Cal.KeyContext.InText).
    private bool KeyboardInText()
    {
        if (XamlRoot is not { } root)
        {
            return false;
        }
        var focused = FocusManager.GetFocusedElement(root);
        return focused is TextBox or PasswordBox or RichEditBox or AutoSuggestBox;
    }

    // A popup the user opened (a flyout, a menu); a tooltip is none.
    private bool PopupOpen() =>
        XamlRoot is { } root && VisualTreeHelper.GetOpenPopupsForXamlRoot(root).Any(p => p.Child is not ToolTip);

    private static bool IsDown(WinKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);
}

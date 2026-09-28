// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/PreferencesWindowController.swift
// (show, the pages built at once, windowWillClose); GTK:
// ui/internal/window/preferences.go (NewPreferences, the closed handler
// that undoes every binding) and ui/main.go (app.preferences, Ctrl+,).
//
// Windows (docs/windows-port.md §0 and §11.3, windows/README.md): one
// window for the whole app (Show brings it to the front), named
// Preferences as GTK names it, with no search field; the pages are built
// when it opens, as GTK builds them, so the General page asks the daemon
// at once, and the AI page asks the bridge whenever it comes up. On the
// General page Windows decides some things (the Run value and the
// default mail app): the page reads them again whenever it comes up and
// whenever the window is activated (after a visit to Settings). Ctrl+Up
// and Ctrl+Down are the window's MoveUp and MoveDown commands, which move
// the selected account while the Accounts list has the focus. Closing the
// window ends every binding and drops every late reply.

using System;
using System.IO;
using Malachi.App.Shell;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Preferences;

/// <summary>The Preferences window.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The bindings are disposed when the window closes (Closed).")]
public sealed partial class PreferencesWindow : Window
{
    /// <summary>The window's width the first time it opens, in effective pixels.</summary>
    public const int DefaultWidth = 880;

    /// <summary>Its height the first time it opens.</summary>
    public const int DefaultHeight = 680;

    /// <summary>The narrowest it gets (the pages' rows wrap below that).</summary>
    public const int MinWidth = 480;

    /// <summary>The lowest it gets.</summary>
    public const int MinHeight = 400;

    private static PreferencesWindow? open;

    private readonly SettingBindings bindings;
    private readonly AccountsPage accounts;
    private readonly GeneralPage general;
    private readonly AppearancePage appearance;
    private readonly AiPage ai;

    private PreferencesWindow(AppState state)
    {
        InitializeComponent();
        Title = L10n.T("Preferences");
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            SolidBackground.Visibility = Visibility.Visible;
        }
        bindings = new SettingBindings(state.Settings);
        Tracked = state.Windows.Track(this, WindowKind.Preferences, Root, ToastsHost);

        // Every page at once, as GTK builds them.
        accounts = new AccountsPage(state, this, ToastsHost);
        general = new GeneralPage(state, bindings, ToastsHost);
        appearance = new AppearancePage(state, bindings);
        ai = new AiPage(state, ToastsHost);

        var commands = Tracked.Commands;
        commands.MoveUp.Handler = () => accounts.MoveSelected(-1);
        commands.MoveUp.CanExecute = () => ReferenceEquals(PageHost.Content, accounts) && accounts.CanMove;
        commands.MoveDown.Handler = () => accounts.MoveSelected(1);
        commands.MoveDown.CanExecute = () => ReferenceEquals(PageHost.Content, accounts) && accounts.CanMove;

        Activated += (_, e) =>
        {
            if (e.WindowActivationState != WindowActivationState.Deactivated && ReferenceEquals(PageHost.Content, general))
            {
                general.Refresh();
            }
        };
        Closed += (_, _) =>
        {
            if (ReferenceEquals(open, this))
            {
                open = null;
            }
            bindings.Dispose();
            accounts.Close();
            general.Close();
            ai.Close();
        };
        Navigation.SelectedItem = AccountsItem;
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The Accounts page.</summary>
    public AccountsPage Accounts => accounts;

    /// <summary>
    /// Opens the Preferences, or brings the open window to the front
    /// (app.preferences). Null past Quit's point of no return.
    /// </summary>
    public static PreferencesWindow? Show(AppState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (open is { } w)
        {
            WindowPresenter.Present(w);
            return w;
        }
        if (state.IsStopping)
        {
            return null;
        }
        var created = new PreferencesWindow(state);
        open = created;
        created.Place(state.Windows.Active?.Window ?? state.MainWindow);
        WindowPresenter.Present(created);
        return created;
    }

    // The default size at the display's scale, centred on the window it
    // was opened from; never smaller than the minimum.
    private void Place(Window? near)
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

    private void OnNavigationSelectionChanged(NavigationView sender, NavigationViewSelectionChangedEventArgs args)
    {
        UIElement page = ((args.SelectedItem as NavigationViewItem)?.Tag as string) switch
        {
            "general" => general,
            "appearance" => appearance,
            "ai" => ai,
            _ => accounts,
        };
        if (ReferenceEquals(PageHost.Content, page))
        {
            return;
        }
        PageHost.Content = page;
        if (ReferenceEquals(page, general))
        {
            general.Refresh();
        }
        else if (ReferenceEquals(page, ai))
        {
            // The status is asked whenever the page comes up.
            ai.Refresh();
        }
    }
}

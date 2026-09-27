// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController.swift
// (the frame: title, size, the toast overlay, install(sidebar:list:
// message:statusBar:), folderTitle, the close that hides with Run in
// Background) and MainContentViewController.swift (the status bar slot);
// GTK: ui/data/ui/window.blp and window.go (New, the close-request
// handler, Toast). The shell's part of docs/windows-port.md §11.1:
//
// - the WinUI TitleBar on Mica, tall (48 px, room for the search box),
//   set with SetTitleBar; the caption is "<folder> – Malachi Mail" once a
//   folder is selected (window.blp's title is "Malachi Mail");
// - 1200×760 and at least 360×294 (window.blp), the size and the
//   maximised state kept in the gschema keys GTK declares
//   (window-width, window-height, window-maximized; U10);
// - the regions the wave-2 screens fill (Sidebar, MessageList, Reader,
//   StatusBar), the toast overlay over the message pane, and a provisional
//   summary and status line until they are filled;
// - closing never destroys the window (one main window per process): it
//   asks the app, which hides it (docs/windows-port.md §10).

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Globalization;
using System.IO;
using Malachi.App.Commands;
using Malachi.App.Shell;
using Malachi.Core;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App;

/// <summary>The main window of the application.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window procedure's hook is removed when the window closes.")]
public sealed partial class MainWindow : Window, INotifyPropertyChanged
{
    /// <summary>window.blp default-width.</summary>
    public const int DefaultWidth = 1200;

    /// <summary>window.blp default-height.</summary>
    public const int DefaultHeight = 760;

    /// <summary>window.blp width-request.</summary>
    public const int MinWidth = 360;

    /// <summary>window.blp height-request.</summary>
    public const int MinHeight = 294;

    private readonly AppState state;
    private readonly MainWindowHook hook;
    private SyncController? sync;
    private string headingText = "";
    private string countsText = "";
    private int accounts;
    private int folders;
    private bool maximizeOnShow;

    /// <summary>Builds the (hidden) main window; <see cref="AppState.ShowMainWindow"/> shows it.</summary>
    public MainWindow(AppState state, Action sessionEnding)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(sessionEnding);
        this.state = state;
        InitializeComponent();
        // The caption the taskbar and Alt+Tab show until a folder is selected.
        Title = AppIdentity.DisplayName;
        AppTitleBar.Title = AppIdentity.DisplayName;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(AppTitleBar);
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            SolidBackground.Visibility = Visibility.Visible;
        }
        hook = new MainWindowHook(WindowPresenter.Handle(this), MinWidth, MinHeight, sessionEnding);
        RestoreGeometry();
        Tracked = state.Windows.Track(this, WindowKind.Main, Root, ToastsHost);
        state.Toasts.Fallback = ToastsHost;
        AppWindow.Closing += OnClosing;
        AppWindow.Changed += OnAppWindowChanged;
        Closed += (_, _) => hook.Dispose();
        UpdateSummary();
    }

    /// <inheritdoc/>
    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>The user asked to close the window (the caption's button, Alt+F4); the app decides.</summary>
    public event EventHandler? CloseRequested;

    /// <summary>The window was shown (true) or hidden (false).</summary>
    public event EventHandler<bool>? ShownChanged;

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The main window's commands (bind the command rows to them; wave 2 sets the per-message handlers).</summary>
    public WindowCommands Commands => Tracked.Commands;

    /// <summary>The toast overlay over the message pane (window.go Toast).</summary>
    public IToasts Toasts => ToastsHost;

    /// <summary>The title bar's search box (window.blp search_entry); the list's search wires it (wave 2).</summary>
    public Microsoft.UI.Xaml.Controls.AutoSuggestBox Search => SearchBox;

    /// <summary>The status line and the banners' state, once the Integration exists.</summary>
    public SyncController? Sync
    {
        get => sync;
        private set
        {
            sync = value;
            Changed(nameof(Sync));
        }
    }

    /// <summary>The selected folder and its counts (provisional summary).</summary>
    public string HeadingText
    {
        get => headingText;
        private set
        {
            headingText = value;
            Changed(nameof(HeadingText));
        }
    }

    /// <summary>How many accounts and folders the mailbox loaded (provisional summary).</summary>
    public string CountsText
    {
        get => countsText;
        private set
        {
            countsText = value;
            Changed(nameof(CountsText));
        }
    }

    /// <summary>Wave 2 (E3): the folder sidebar, in the first column.</summary>
    public UIElement? Sidebar
    {
        get => SidebarRegion.Content as UIElement;
        set => Install(SidebarRegion, value);
    }

    /// <summary>Wave 2 (E3): the message list, in the second column.</summary>
    public UIElement? MessageList
    {
        get => ListRegion.Content as UIElement;
        set => Install(ListRegion, value);
    }

    /// <summary>Wave 2 (E4): the reader (or the No Accounts page), under the toast overlay.</summary>
    public UIElement? Reader
    {
        get => ReaderRegion.Content as UIElement;
        set => Install(ReaderRegion, value);
    }

    /// <summary>Wave 2 (E3): the status line with its flyout, across the bottom edge.</summary>
    public UIElement? StatusBar
    {
        get => StatusBarRegion.Content as UIElement;
        set => StatusBarRegion.Content = value ?? ProvisionalStatusLine;
    }

    /// <summary>The columns of the sidebar and the list (wave 2 sizes them from folder-pane-width and message-list-width).</summary>
    public (Microsoft.UI.Xaml.Controls.ColumnDefinition Sidebar, Microsoft.UI.Xaml.Controls.ColumnDefinition List) PaneColumns =>
        (SidebarColumn, ListColumn);

    /// <summary>The Integration exists: the window shows what its controllers hold.</summary>
    public void Attach(Integration integration)
    {
        ArgumentNullException.ThrowIfNull(integration);
        Sync = integration.Sync;
    }

    /// <summary>window.go refreshListTitle: the selected folder in the caption, its counts in the summary.</summary>
    public void ShowListHeading(ListHeading heading)
    {
        // Windows-only string: the caption "<folder> – Malachi Mail" (docs/windows-port.md §11.1).
        Title = string.IsNullOrEmpty(heading.Title) ? AppIdentity.DisplayName : heading.Title + " – " + AppIdentity.DisplayName;
        HeadingText = string.IsNullOrEmpty(heading.Subtitle) ? heading.Title : heading.Title + " · " + heading.Subtitle;
    }

    /// <summary>The accounts were (re)loaded.</summary>
    public void ShowAccounts(IReadOnlyList<Account> loaded)
    {
        ArgumentNullException.ThrowIfNull(loaded);
        accounts = loaded.Count;
        UpdateSummary();
    }

    /// <summary>The folders were (re)loaded.</summary>
    public void ShowFolderCount(int count)
    {
        folders = count;
        UpdateSummary();
    }

    /// <summary>Puts the keyboard in the search box and selects its text (win.search).</summary>
    public void FocusSearch()
    {
        SearchBox.Focus(FocusState.Keyboard);
    }

    /// <summary>
    /// Called before every show: a window kept maximised the last time
    /// opens maximised (the first show; later shows keep what it is).
    /// </summary>
    public void PrepareShow()
    {
        if (maximizeOnShow && AppWindow.Presenter is OverlappedPresenter overlapped)
        {
            maximizeOnShow = false;
            overlapped.Maximize();
        }
    }

    /// <summary>
    /// Keeps the size and the maximised state (window-width, window-height,
    /// window-maximized); a minimised window keeps what it had.
    /// </summary>
    public void SaveGeometry()
    {
        if (AppWindow.Presenter is not OverlappedPresenter overlapped || !AppWindow.IsVisible)
        {
            return;
        }
        switch (overlapped.State)
        {
            case OverlappedPresenterState.Maximized:
                state.Settings.WindowMaximized = true;
                break;
            case OverlappedPresenterState.Restored:
                var scale = Scale();
                state.Settings.WindowMaximized = false;
                state.Settings.WindowWidth = (int)Math.Round(AppWindow.Size.Width / scale);
                state.Settings.WindowHeight = (int)Math.Round(AppWindow.Size.Height / scale);
                break;
        }
    }

    private void Install(Microsoft.UI.Xaml.Controls.ContentControl region, UIElement? content)
    {
        region.Content = content;
        UpdateSummary();
    }

    // The size of last time, in effective pixels at this window's DPI,
    // centred on the display it opens on; never smaller than window.blp's.
    private void RestoreGeometry()
    {
        var settings = state.Settings;
        var scale = Scale();
        var width = Math.Max(settings.WindowWidth > 0 ? settings.WindowWidth : DefaultWidth, MinWidth);
        var height = Math.Max(settings.WindowHeight > 0 ? settings.WindowHeight : DefaultHeight, MinHeight);
        var size = new SizeInt32((int)Math.Round(width * scale), (int)Math.Round(height * scale));
        var area = DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Primary).WorkArea;
        size.Width = Math.Min(size.Width, area.Width);
        size.Height = Math.Min(size.Height, area.Height);
        AppWindow.MoveAndResize(new RectInt32(
            area.X + ((area.Width - size.Width) / 2), area.Y + ((area.Height - size.Height) / 2), size.Width, size.Height));
        maximizeOnShow = settings.WindowMaximized;
    }

    private double Scale()
    {
        var dpi = PInvoke.GetDpiForWindow((HWND)WindowPresenter.Handle(this));
        return dpi == 0 ? 1.0 : dpi / 96.0;
    }

    private void UpdateSummary()
    {
        var empty = SidebarRegion.Content is null && ListRegion.Content is null && ReaderRegion.Content is null;
        ShellSummary.Visibility = empty ? Visibility.Visible : Visibility.Collapsed;
        // Windows-only string: the provisional summary of the shell.
        CountsText = string.Format(CultureInfo.CurrentCulture, "Accounts: {0} · Folders: {1}", accounts, folders);
    }

    private void OnClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        // One main window for the process: it hides; the app decides whether it quits.
        args.Cancel = true;
        SaveGeometry();
        CloseRequested?.Invoke(this, EventArgs.Empty);
    }

    private void OnAppWindowChanged(AppWindow sender, AppWindowChangedEventArgs args)
    {
        if (args.DidVisibilityChange)
        {
            ShownChanged?.Invoke(this, sender.IsVisible);
        }
    }

    private void Changed(string property) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(property));
}

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
// - the panes (MainWindow.Panes.cs: the sidebar, the list, the message
//   page's header bar, the status line and the adaptive layout), the
//   reader's region the reader fills (wave 2, E4), the toast overlay over
//   the message page's content;
// - closing never destroys the window (one main window per process): it
//   asks the app, which hides it (docs/windows-port.md §10); shown again, or
//   first shown after a start in the background, it takes the keyboard
//   (TakeKeyboard).

using System;
using System.IO;
using Malachi.App.Commands;
using Malachi.App.Shell;
using Malachi.Core;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Input;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.App;

/// <summary>The main window of the application.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window procedure's hook is removed when the window closes.")]
public sealed partial class MainWindow : Window
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
    private bool maximizeOnShow;

    // The window has been shown (its first show may come long after the
    // start: --background).
    private bool shownOnce;

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
        InitializePanes();
    }

    /// <summary>The user asked to close the window (the caption's button, Alt+F4); the app decides.</summary>
    public event EventHandler? CloseRequested;

    /// <summary>The window was shown (true) or hidden (false).</summary>
    public event EventHandler<bool>? ShownChanged;

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The main window's commands (the panes bind their buttons to them and set the per-message handlers).</summary>
    public WindowCommands Commands => Tracked.Commands;

    /// <summary>The toast overlay over the message pane (window.go Toast).</summary>
    public IToasts Toasts => ToastsHost;

    /// <summary>The title bar's search box (window.blp search_entry); the list's search follows it (MainWindow.Panes.cs).</summary>
    public Microsoft.UI.Xaml.Controls.AutoSuggestBox Search => SearchBox;

    /// <summary>Wave 2 (E4): the reader (or the No Accounts page), under the message page's header bar and the toast overlay.</summary>
    public UIElement? Reader
    {
        get => ReaderRegion.Content as UIElement;
        set => ReaderRegion.Content = value;
    }

    /// <summary>The Integration exists: the window shows what its controllers hold.</summary>
    public void Attach(Integration integration)
    {
        ArgumentNullException.ThrowIfNull(integration);
        AttachPanes(integration);
    }

    /// <summary>window.go refreshListTitle: the selected folder in the caption (the list's header shows it with its counts).</summary>
    public void ShowListHeading(ListHeading heading) =>
        // "<folder> – Malachi Mail", the folder isolated (docs/windows-port.md §11.1).
        Title = heading.Caption;

    /// <summary>
    /// Puts the keyboard in the search box and selects its text (win.search,
    /// search.go startSearch: a folded window shows the list first).
    /// </summary>
    public void FocusSearch() => BeginSearch();

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
    /// window-maximized) and the pane widths of a wide layout
    /// (folder-pane-width, message-list-width). The size is the one the
    /// window restores to, so a maximised window still remembers the size
    /// it unmaximises to, as GTK's binding of default-width and
    /// default-height does (window/geometry.go); a minimised window keeps
    /// what it had.
    /// </summary>
    public void SaveGeometry()
    {
        if (AppWindow.Presenter is not OverlappedPresenter overlapped || !AppWindow.IsVisible)
        {
            return;
        }
        SavePaneWidths();
        switch (overlapped.State)
        {
            case OverlappedPresenterState.Maximized:
                state.Settings.WindowMaximized = true;
                SaveRestoredSize();
                break;
            case OverlappedPresenterState.Restored:
                state.Settings.WindowMaximized = false;
                SaveRestoredSize();
                break;
        }
    }

    // The size the window restores to, in effective pixels: the placement's
    // normal rectangle (the window's own size while it is restored), or the
    // current size when Windows does not give it.
    private void SaveRestoredSize()
    {
        var scale = Scale();
        var width = (double)AppWindow.Size.Width;
        var height = (double)AppWindow.Size.Height;
        var placement = new WINDOWPLACEMENT { length = (uint)System.Runtime.InteropServices.Marshal.SizeOf<WINDOWPLACEMENT>() };
        if (PInvoke.GetWindowPlacement((HWND)WindowPresenter.Handle(this), ref placement))
        {
            var normal = placement.rcNormalPosition;
            if (normal.right > normal.left && normal.bottom > normal.top)
            {
                width = normal.right - normal.left;
                height = normal.bottom - normal.top;
            }
        }
        state.Settings.WindowWidth = (int)Math.Round(width / scale);
        state.Settings.WindowHeight = (int)Math.Round(height / scale);
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
            if (sender.IsVisible)
            {
                var first = !shownOnce;
                shownOnce = true;
                // After the window's own activation.
                DispatcherQueue.TryEnqueue(() => TakeKeyboard(first));
            }
            ShownChanged?.Invoke(this, sender.IsVisible);
        }
    }

    // Measured: shown again after AppWindow.Hide() (Run in Background, or a
    // compose window holding the app), from the tray, a second launch or a
    // notification, the window comes to the front with the keyboard on its
    // caption's input window instead of the XAML island, so no key reaches
    // the commands (Ctrl+N, F5, A, J, U, S, Delete) until a click; the island
    // takes it back, and XAML gives it to the element that had it. And shown
    // for the first time after a start in the background (--background),
    // WinUI's own first focus lands in the search box, past OnFirstFocus,
    // where the single keys type: as at a normal start, the sidebar's first
    // tab stop takes it instead.
    private void TakeKeyboard(bool firstShow)
    {
        if (!AppWindow.IsVisible || Root.XamlRoot is not { } xamlRoot || xamlRoot.ContentIsland is not { } island)
        {
            return;
        }
        var input = InputFocusController.GetForIsland(island);
        if (!input.HasFocus)
        {
            input.TrySetFocus();
        }
        var focused = FocusManager.GetFocusedElement(xamlRoot) as DependencyObject;
        if ((focused is null || (firstShow && IsWithin(focused, SearchBox)))
            && FocusManager.FindFirstFocusableElement(SidebarPane) is UIElement first)
        {
            first.Focus(FocusState.Programmatic);
        }
    }
}

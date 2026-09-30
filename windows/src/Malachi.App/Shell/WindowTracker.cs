// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Appearance/AppearanceController.swift
// (the colour scheme on every window) and of what AppDelegate.swift and the
// window controllers do for each window (the application's actions in
// every window, the toast router's presenter set in windowDidBecomeKey,
// applicationShouldTerminateAfterLastWindowClosed); GTK: style.Apply
// (AdwStyleManager's colour scheme), the app.* actions every window of a
// GtkApplication has, and GApplication's life with its windows.
//
// Every window of the app is tracked here, the main window by the shell
// and the others by whoever opens them (wave 2: message, attached-message,
// compose, preferences, wizard, previewer windows call Track right after
// creating theirs, with its root element and its ToastHost):
//
// - the colour scheme: RequestedTheme on the window's root and
//   AppWindow.TitleBar.PreferredTheme, which the caption buttons follow
//   (they ignore RequestedTheme; measured, APP-SPIKES.md §3); a window
//   without Mica gives its root Background="{ThemeResource
//   ApplicationPageBackgroundThemeBrush}" in its XAML;
// - its commands and keys: a WindowCommands with the application's
//   commands wired (New Message, Preferences, Add Account, About, Quit,
//   Check for New Mail, Search) and a CommandRouter on its root;
// - its activation (Core's WindowActivation): the window that is active
//   now, none while another application has the foreground (GTK's
//   w.IsActive(), macOS isKeyWindow), and the one that was active last,
//   which gets the application's toasts and owns their dialogs;
// - its toasts: the router's presenter while it is the last active window;
// - its life: a secondary window counts for the GApplication rule
//   (WindowLifetime) until it closes, and LastWindowClosed tells the app
//   when nothing holds it any more.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Commands;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>The app's windows: theme, commands, keys, toasts and life.</summary>
public sealed partial class WindowTracker : IDisposable
{
    private readonly List<TrackedWindow> windows = [];
    private readonly SettingsStore settings;
    private readonly AppHooks hooks;
    private readonly ToastRouter toasts;
    private readonly WindowLifetime lifetime;
    private readonly ILogger logger;
    private readonly SettingsChangeToken themeToken;
    private readonly WindowActivation activation = new();

    internal WindowTracker(SettingsStore settings, AppHooks hooks, ToastRouter toasts, WindowLifetime lifetime, ILogger logger)
    {
        this.settings = settings;
        this.hooks = hooks;
        this.toasts = toasts;
        this.lifetime = lifetime;
        this.logger = logger;
        themeToken = settings.OnChange(SettingsKey.ColorScheme, ApplyTheme);
        hooks.Changed += (_, _) => RefreshCommands();
    }

    /// <summary>A secondary window closed and nothing holds the app any more (the GApplication rule): quit.</summary>
    public event EventHandler? LastWindowClosed;

    /// <summary>Another tracked window became the last active one, or the last active one closed.</summary>
    public event EventHandler? ActiveWindowChanged;

    /// <summary>The alerts, for the About command and to keep keys away from an open dialog.</summary>
    internal IAlerts? Alerts { get; set; }

    /// <summary>Quit (app.quit), set by the app.</summary>
    internal Action? Quit { get; set; }

    /// <summary>The windows being tracked, the main window first.</summary>
    public IReadOnlyList<TrackedWindow> Windows => windows;

    /// <summary>
    /// The tracked window that was active last, also while another
    /// application is in the foreground: where the application's toasts go,
    /// the owner of what it opens.
    /// </summary>
    public TrackedWindow? Active => activation.LastActive as TrackedWindow;

    /// <summary>
    /// Tracks <paramref name="window"/>, a window of <paramref name="kind"/>
    /// whose content root is <paramref name="root"/> and whose toast overlay
    /// is <paramref name="windowToasts"/>. A window other than the main one
    /// counts for the app's life until it closes. Call it on the UI thread
    /// right after creating the window, before showing it.
    /// </summary>
    public TrackedWindow Track(Window window, WindowKind kind, UIElement root, IToasts? windowToasts = null)
    {
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(root);
        var commands = new WindowCommands();
        var tracked = new TrackedWindow(window, kind, commands, windowToasts);
        WireApplicationCommands(tracked);
        var router = new CommandRouter(
            kind, commands, settings, WindowPresenter.Handle(window), window.DispatcherQueue ?? DispatcherQueue.GetForCurrentThread(),
            () => Alerts?.IsShowing(window) == true, logger);
        router.Attach(root);
        tracked.Router = router;
        windows.Add(tracked);
        ApplyTheme(tracked);

        window.Activated += (_, e) =>
        {
            if (!windows.Contains(tracked))
            {
                // Measured: a window closed while it is not the active one
                // (its caption button invoked through UI Automation, a
                // compose window closing itself after Send) reports an
                // activation after Closed. It must not become the last
                // active window again: its AppWindow is gone, and the next
                // compose window placed by it failed, and the app with it.
                LogActivatedAfterClose(logger, tracked.Kind);
                return;
            }
            if (e.WindowActivationState == WindowActivationState.Deactivated)
            {
                activation.Deactivated(tracked);
            }
            else if (activation.Activated(tracked))
            {
                toasts.Presenter = tracked.Toasts;
                ActiveWindowChanged?.Invoke(this, EventArgs.Empty);
            }
        };
        window.Closed += (_, _) => Forget(tracked);
        if (kind != WindowKind.Main)
        {
            lifetime.WindowOpened(window);
        }
        return tracked;
    }

    /// <summary>The tracked window of <paramref name="window"/>, if it is tracked.</summary>
    public TrackedWindow? Find(Window window) => windows.FirstOrDefault(w => w.Window == window);

    /// <summary>
    /// Whether <paramref name="window"/> is the active window now: false
    /// while another application is in the foreground (GTK's
    /// w.IsActive()).
    /// </summary>
    public bool IsActive(Window window) => Find(window) is { } tracked && activation.IsActive(tracked);

    /// <summary>Stops following the colour scheme.</summary>
    public void Dispose() => themeToken.Cancel();

    /// <summary>The colour scheme's WinUI theme (style.Apply: follow the system, or force one).</summary>
    internal static ElementTheme ThemeOf(ColorScheme scheme) => scheme switch
    {
        ColorScheme.Light => ElementTheme.Light,
        ColorScheme.Dark => ElementTheme.Dark,
        _ => ElementTheme.Default,
    };

    /// <summary>The caption buttons' theme for a colour scheme.</summary>
    internal static TitleBarTheme TitleBarThemeOf(ColorScheme scheme) => scheme switch
    {
        ColorScheme.Light => TitleBarTheme.Light,
        ColorScheme.Dark => TitleBarTheme.Dark,
        _ => TitleBarTheme.UseDefaultAppMode,
    };

    private void ApplyTheme()
    {
        foreach (var w in windows)
        {
            ApplyTheme(w);
        }
    }

    private void ApplyTheme(TrackedWindow tracked)
    {
        var scheme = settings.ColorScheme;
        if (tracked.Window.Content is FrameworkElement root)
        {
            root.RequestedTheme = ThemeOf(scheme);
        }
        try
        {
            tracked.Window.AppWindow.TitleBar.PreferredTheme = TitleBarThemeOf(scheme);
        }
        catch (Exception e) when (e is InvalidOperationException or System.Runtime.InteropServices.COMException)
        {
            // A window on its way out has no title bar to theme.
            LogThemeFailed(logger, tracked.Kind, e.Message);
        }
    }

    private void WireApplicationCommands(TrackedWindow tracked)
    {
        var c = tracked.Commands;
        var window = tracked.Window;
        c.NewMessage.Handler = () => hooks.ComposeNew?.Invoke();
        // New Message needs an account that writes mail (CanComposeNew): a
        // Jira account only comments.
        c.NewMessage.CanExecute = () => hooks.ComposeNew is not null && (hooks.CanComposeNew?.Invoke() ?? true);
        c.Preferences.Handler = () => hooks.OpenPreferences?.Invoke();
        c.Preferences.CanExecute = () => hooks.OpenPreferences is not null;
        c.AddAccount.Handler = () => hooks.AddAccount?.Invoke(window);
        c.AddAccount.CanExecute = () => hooks.AddAccount is not null;
        c.AddJiraAccount.Handler = () => hooks.AddJiraAccount?.Invoke(window);
        c.AddJiraAccount.CanExecute = () => hooks.AddJiraAccount is not null;
        c.CheckForNewMail.Handler = () => hooks.CheckForNewMail?.Invoke();
        c.CheckForNewMail.CanExecute = () => hooks.CheckForNewMail is not null;
        c.Search.Handler = () => hooks.FocusSearch?.Invoke();
        c.Search.CanExecute = () => hooks.FocusSearch is not null;
        c.About.Handler = () => _ = Alerts?.ShowAboutAsync(window);
        c.Quit.Handler = () => Quit?.Invoke();
        if (tracked.Kind != WindowKind.Main)
        {
            // Escape and Ctrl+W; a window with a veto (compose) sets its own.
            c.CloseWindow.Handler = window.Close;
        }
    }

    private void RefreshCommands()
    {
        foreach (var w in windows)
        {
            w.Commands.RefreshAll();
        }
    }

    private void Forget(TrackedWindow tracked)
    {
        windows.Remove(tracked);
        tracked.Router?.Dispose();
        if (activation.Closed(tracked))
        {
            toasts.Presenter = null;
            ActiveWindowChanged?.Invoke(this, EventArgs.Empty);
        }
        if (tracked.Kind != WindowKind.Main && lifetime.WindowClosed(tracked.Window))
        {
            LastWindowClosed?.Invoke(this, EventArgs.Empty);
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "a closed {Kind} window reported its activation")]
    private static partial void LogActivatedAfterClose(ILogger logger, WindowKind kind);

    [LoggerMessage(Level = LogLevel.Debug, Message = "the title bar of a {Kind} window takes no theme: {Reason}")]
    private static partial void LogThemeFailed(ILogger logger, WindowKind kind, string reason);
}
